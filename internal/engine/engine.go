// Package engine downloads one file over several networks at once. Every
// network gets its own pool of HTTP range connections (aria2-style), and idle
// connections steal half of the biggest unfinished piece, so faster networks
// end up carrying more of the file.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Network is one way out to the internet.
type Network struct {
	ID     string
	Label  string
	Client *http.Client
}

// Options for one download.
type Options struct {
	URL             string
	Dir             string
	FileName        string // optional; taken from the server when empty
	Networks        []Network
	ConnsPerNetwork int
	Header          http.Header
	MinSplit        int64         // smallest piece worth splitting (default 1 MiB)
	StallTimeout    time.Duration // no bytes for this long => reconnect (default 30s)
	MaxRetries      int           // consecutive failures before a connection gives up (default 6)
}

type State string

const (
	StateQueued      State = "queued"
	StateProbing     State = "probing"
	StateDownloading State = "downloading"
	StatePaused      State = "paused"
	StateDone        State = "done"
	StateError       State = "error"
	StateCanceled    State = "canceled"
)

type NetSnapshot struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Bytes     int64   `json:"bytes"`
	Speed     float64 `json:"speed"`
	Active    int     `json:"active"`
	Failed    bool    `json:"failed"`
	LastError string  `json:"lastError,omitempty"`
}

type Snapshot struct {
	ID         string        `json:"id"`
	URL        string        `json:"url"`
	FileName   string        `json:"fileName"`
	Dir        string        `json:"dir"`
	Path       string        `json:"path"`
	Size       int64         `json:"size"`
	Done       int64         `json:"done"`
	Speed      float64       `json:"speed"`
	State      State         `json:"state"`
	Error      string        `json:"error,omitempty"`
	RangeOK    bool          `json:"rangeOK"`
	Throttled  int           `json:"throttled,omitempty"` // connections dropped because the server limited them
	Networks   []NetSnapshot `json:"networks"`
	CreatedAt  int64         `json:"createdAt"`
	FinishedAt int64         `json:"finishedAt,omitempty"`
}

type segment struct {
	pos, end int64 // still needed: [pos, end)
	owner    int   // worker id, -1 = free
}

type netStat struct {
	id, label string
	bytes     atomic.Int64
	active    atomic.Int32
	failed    atomic.Bool
	lastErr   atomic.Value
	alive     atomic.Int32
	speed     float64
	lastBytes int64
}

// Job is one file download. Safe for concurrent use.
type Job struct {
	ID   string
	opts Options

	mu         sync.Mutex
	state      State
	errMsg     string
	fileName   string
	size       int64
	rangeOK    bool
	finalURL   string
	etag       string
	lastMod    string
	segs       []*segment
	prepared   bool
	nets       []*netStat
	speed      float64
	createdAt  time.Time
	finishedAt time.Time
	fatal      error

	done      atomic.Int64
	throttled atomic.Int32
	cancel    context.CancelFunc
	running   chan struct{}
}

var (
	errFatal     = errors.New("fatal")
	errThrottled = errors.New("서버가 동시 연결을 제한함(429/503)")
)

func New(id string, o Options) *Job {
	if o.ConnsPerNetwork < 1 {
		o.ConnsPerNetwork = 8
	}
	if o.MinSplit <= 0 {
		o.MinSplit = 1 << 20
	}
	if o.MinSplit < 256<<10 { // must stay above the 128 KiB read buffer
		o.MinSplit = 256 << 10
	}
	if o.StallTimeout <= 0 {
		o.StallTimeout = 30 * time.Second
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 6
	}
	j := &Job{ID: id, opts: o, state: StateQueued, size: -1, createdAt: time.Now(), fileName: SanitizeName(o.FileName)}
	j.setNetworksLocked(o.Networks)
	return j
}

// Restore makes a job that shows an old state (after app restart).
func Restore(id string, o Options, st State, size, done int64, created, finished time.Time, errMsg string) *Job {
	j := New(id, o)
	j.state, j.size, j.createdAt, j.finishedAt, j.errMsg = st, size, created, finished, errMsg
	j.done.Store(done)
	return j
}

func (j *Job) setNetworksLocked(ns []Network) {
	j.opts.Networks = ns
	old := map[string]int64{}
	for _, n := range j.nets {
		old[n.id] = n.bytes.Load()
	}
	j.nets = nil
	for _, n := range ns {
		st := &netStat{id: n.ID, label: n.Label}
		st.bytes.Store(old[n.ID])
		st.lastBytes = old[n.ID]
		j.nets = append(j.nets, st)
	}
}

// SetNetworks swaps the networks used next time the job starts.
func (j *Job) SetNetworks(ns []Network, conns int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if conns > 0 {
		j.opts.ConnsPerNetwork = conns
	}
	j.setNetworksLocked(ns)
}

func (j *Job) Running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running != nil
}

// Start (or resume) in the background.
func (j *Job) Start() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running != nil || j.state == StateDone {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.running = make(chan struct{})
	j.errMsg = ""
	j.fatal = nil
	for _, n := range j.nets {
		n.failed.Store(false)
		n.lastErr.Store("")
	}
	go j.run(ctx, j.running)
}

// Wait until the current run ends.
func (j *Job) Wait() {
	j.mu.Lock()
	ch := j.running
	j.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (j *Job) stop(st State) {
	j.mu.Lock()
	cancel, ch := j.cancel, j.running
	if ch == nil {
		if j.state != StateDone {
			j.state = st
		}
		j.mu.Unlock()
		return
	}
	j.state = st
	j.mu.Unlock()
	cancel()
	<-ch
}

// Pause keeps the partial file so it can resume.
func (j *Job) Pause() { j.stop(StatePaused) }

// Cancel stops and deletes the partial file.
func (j *Job) Cancel() {
	j.stop(StateCanceled)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state == StateDone || j.fileName == "" {
		return
	}
	os.Remove(j.partPath())
	os.Remove(j.ctlPath())
	j.segs, j.prepared = nil, false
	j.done.Store(0)
	for _, n := range j.nets {
		n.bytes.Store(0)
		n.lastBytes = 0
	}
}

func (j *Job) partPath() string  { return filepath.Join(j.opts.Dir, j.fileName+".mndl.part") }
func (j *Job) ctlPath() string   { return filepath.Join(j.opts.Dir, j.fileName+".mndl.json") }
func (j *Job) finalPath() string { return filepath.Join(j.opts.Dir, j.fileName) }

func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := Snapshot{
		ID: j.ID, URL: j.opts.URL, FileName: j.fileName, Dir: j.opts.Dir, Size: j.size,
		Done: j.done.Load(), Speed: j.speed, State: j.state, Error: j.errMsg, RangeOK: j.rangeOK,
		CreatedAt: j.createdAt.UnixMilli(), Throttled: int(j.throttled.Load()),
	}
	if j.fileName != "" {
		s.Path = j.finalPath()
	}
	if !j.finishedAt.IsZero() {
		s.FinishedAt = j.finishedAt.UnixMilli()
	}
	for _, n := range j.nets {
		ns := NetSnapshot{ID: n.id, Label: n.label, Bytes: n.bytes.Load(), Speed: n.speed, Active: int(n.active.Load()), Failed: n.failed.Load()}
		if e, _ := n.lastErr.Load().(string); e != "" {
			ns.LastError = e
		}
		s.Networks = append(s.Networks, ns)
	}
	if j.state != StateDownloading {
		s.Speed = 0
		for i := range s.Networks {
			s.Networks[i].Speed = 0
		}
	}
	return s
}

func (j *Job) setState(st State) {
	j.mu.Lock()
	j.state = st
	j.mu.Unlock()
}

func (j *Job) run(ctx context.Context, running chan struct{}) {
	var err error
	defer func() {
		j.mu.Lock()
		switch {
		case err == nil:
			j.state = StateDone
			j.finishedAt = time.Now()
		case ctx.Err() != nil && (j.state == StatePaused || j.state == StateCanceled):
			// user stopped it
		default:
			j.state = StateError
			j.errMsg = err.Error()
		}
		j.running, j.cancel = nil, nil
		j.mu.Unlock()
		close(running)
	}()

	j.mu.Lock()
	if len(j.opts.Networks) == 0 {
		j.mu.Unlock()
		err = errors.New("사용할 네트워크를 하나 이상 골라야 함")
		return
	}
	prepared := j.prepared
	if !prepared {
		j.state = StateProbing
	}
	j.mu.Unlock()

	if !prepared {
		if err = j.prepare(ctx); err != nil {
			return
		}
	}
	j.setState(StateDownloading)
	stopSampler := j.sampler()
	defer stopSampler()
	if j.rangeOK {
		err = j.multi(ctx)
	} else {
		err = j.single(ctx)
	}
	if err != nil {
		return
	}
	err = j.finalize()
}

// prepare probes the URL, picks the file name and resumes from a control file.
func (j *Job) prepare(ctx context.Context) error {
	var info *probeInfo
	var perr error
	for _, n := range j.opts.Networks {
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		info, perr = probe(pctx, n.Client, j.opts.URL, j.opts.Header)
		cancel()
		if perr == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if perr != nil {
		return fmt.Errorf("주소 확인 실패: %w", perr)
	}
	if err := os.MkdirAll(j.opts.Dir, 0o755); err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	j.size, j.rangeOK, j.finalURL, j.etag, j.lastMod = info.size, info.rangeOK, info.finalURL, info.etag, info.lastMod
	if j.fileName == "" {
		j.fileName = info.name
	}

	if j.rangeOK {
		if c, err := loadControl(j.ctlPath()); err == nil && c.matches(j.opts.URL, info) {
			if fi, err := os.Stat(j.partPath()); err == nil && fi.Size() == info.size {
				j.segs = nil
				var remaining int64
				for _, r := range c.Remaining {
					if r[1] > r[0] {
						j.segs = append(j.segs, &segment{pos: r[0], end: r[1], owner: -1})
						remaining += r[1] - r[0]
					}
				}
				j.done.Store(info.size - remaining)
				for _, n := range j.nets {
					if b, ok := c.NetBytes[n.id]; ok && n.bytes.Load() == 0 {
						n.bytes.Store(b)
						n.lastBytes = b
					}
				}
				j.prepared = true
				return nil
			}
		}
	}

	// fresh start: never overwrite an existing file
	j.fileName = uniqueName(j.opts.Dir, j.fileName)
	f, err := os.Create(j.partPath())
	if err != nil {
		return err
	}
	if j.size > 0 && j.rangeOK {
		if err := f.Truncate(j.size); err != nil {
			f.Close()
			return err
		}
	}
	f.Close()
	j.done.Store(0)
	for _, n := range j.nets {
		n.bytes.Store(0)
		n.lastBytes = 0
	}
	j.segs = nil
	if j.rangeOK && j.size > 0 {
		workers := int64(len(j.opts.Networks) * j.opts.ConnsPerNetwork)
		k := j.size / j.opts.MinSplit
		if k > workers {
			k = workers
		}
		if k < 1 {
			k = 1
		}
		step := j.size / k
		for i := int64(0); i < k; i++ {
			end := (i + 1) * step
			if i == k-1 {
				end = j.size
			}
			j.segs = append(j.segs, &segment{pos: i * step, end: end, owner: -1})
		}
	}
	j.prepared = true
	return nil
}

func uniqueName(dir, name string) string {
	exists := func(n string) bool {
		for _, p := range []string{n, n + ".mndl.part", n + ".mndl.json"} {
			if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
				return true
			}
		}
		return false
	}
	if !exists(name) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		n := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !exists(n) {
			return n
		}
	}
}

// sampler updates speeds twice a second.
func (j *Job) sampler() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				dt := now.Sub(last).Seconds()
				last = now
				j.mu.Lock()
				var total float64
				for _, n := range j.nets {
					b := n.bytes.Load()
					inst := float64(b-n.lastBytes) / dt
					n.lastBytes = b
					n.speed = n.speed*0.6 + inst*0.4
					if n.speed < 1 {
						n.speed = 0
					}
					total += n.speed
				}
				j.speed = total
				j.mu.Unlock()
			}
		}
	}()
	return func() { close(stop) }
}

func (j *Job) multi(ctx context.Context) error {
	f, err := os.OpenFile(j.partPath(), os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	saveStop := make(chan struct{})
	saveDone := make(chan struct{})
	go func() {
		defer close(saveDone)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-saveStop:
				return
			case <-t.C:
				j.saveControl()
			}
		}
	}()
	defer func() {
		close(saveStop)
		<-saveDone
		j.saveControl()
	}()

	wid := 0
	for round := 0; ; round++ {
		if j.complete() {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var alive []int
		for i, n := range j.nets {
			if !n.failed.Load() {
				alive = append(alive, i)
			}
		}
		if len(alive) == 0 {
			return j.allFailedErr()
		}
		if round > 30 {
			return errors.New("다운로드를 끝내지 못함 (재시도 초과)")
		}
		rctx, rcancel := context.WithCancel(ctx)
		j.mu.Lock()
		for _, s := range j.segs {
			s.owner = -1
		}
		j.mu.Unlock()
		var wg sync.WaitGroup
		// interleave so every network gets a piece first
		for c := 0; c < j.opts.ConnsPerNetwork; c++ {
			for _, ni := range alive {
				j.nets[ni].alive.Add(1)
				wid++
				wg.Add(1)
				go func(ni, id int) {
					defer wg.Done()
					j.worker(rctx, rcancel, f, ni, id)
				}(ni, wid)
			}
		}
		wg.Wait()
		rcancel()
		j.mu.Lock()
		fatal := j.fatal
		j.mu.Unlock()
		if fatal != nil {
			return fatal
		}
	}
	return f.Sync()
}

func (j *Job) allFailedErr() error {
	var parts []string
	for _, n := range j.nets {
		if e, _ := n.lastErr.Load().(string); e != "" {
			parts = append(parts, n.label+": "+e)
		}
	}
	if len(parts) == 0 {
		return errors.New("모든 네트워크에서 실패")
	}
	return errors.New("모든 네트워크에서 실패 — " + strings.Join(parts, " / "))
}

func (j *Job) complete() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, s := range j.segs {
		if s.pos < s.end {
			return false
		}
	}
	return true
}

// next hands a worker a free piece, or splits the biggest running one.
func (j *Job) next(wid int) *segment {
	j.mu.Lock()
	defer j.mu.Unlock()
	// drop finished pieces
	keep := j.segs[:0]
	for _, s := range j.segs {
		if s.pos < s.end || s.owner >= 0 {
			keep = append(keep, s)
		}
	}
	j.segs = keep
	for _, s := range j.segs {
		if s.owner < 0 && s.pos < s.end {
			s.owner = wid
			return s
		}
	}
	var best *segment
	for _, s := range j.segs {
		rem := s.end - s.pos
		if s.owner >= 0 && rem >= 2*j.opts.MinSplit && (best == nil || rem > best.end-best.pos) {
			best = s
		}
	}
	if best == nil {
		return nil
	}
	mid := best.pos + (best.end-best.pos)/2
	ns := &segment{pos: mid, end: best.end, owner: wid}
	best.end = mid
	j.segs = append(j.segs, ns)
	return ns
}

func (j *Job) release(s *segment, wid int) {
	j.mu.Lock()
	if s.owner == wid {
		s.owner = -1
	}
	j.mu.Unlock()
}

func (j *Job) worker(ctx context.Context, cancelRound context.CancelFunc, f *os.File, ni, wid int) {
	ns := j.nets[ni]
	defer func() {
		if ns.alive.Add(-1) == 0 && ctx.Err() == nil && !j.complete() {
			// every connection on this network gave up
			if e, _ := ns.lastErr.Load().(string); e != "" {
				ns.failed.Store(true)
			}
		}
	}()
	fails := 0
	buf := make([]byte, 128<<10)
	for ctx.Err() == nil {
		s := j.next(wid)
		if s == nil {
			return
		}
		ns.active.Add(1)
		got, err := j.fetch(ctx, f, ns, s, buf)
		ns.active.Add(-1)
		j.release(s, wid)
		if err == nil {
			fails = 0
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errFatal) {
			j.mu.Lock()
			if j.fatal == nil {
				j.fatal = errors.Unwrap(err)
				if j.fatal == nil {
					j.fatal = err
				}
			}
			j.mu.Unlock()
			cancelRound()
			return
		}
		if errors.Is(err, errThrottled) && ns.alive.Load() > 1 {
			// the server wants fewer connections: this one bows out quietly
			j.throttled.Add(1)
			return
		}
		ns.lastErr.Store(err.Error())
		if got > 0 {
			fails = 0 // made progress, just a dropped connection
		}
		fails++
		if fails >= j.opts.MaxRetries {
			return
		}
		back := time.Duration(1<<min(fails-1, 4)) * 500 * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(back):
		}
	}
}

type fatalErr struct{ err error }

func (e fatalErr) Error() string        { return e.err.Error() }
func (e fatalErr) Is(target error) bool { return target == errFatal }
func (e fatalErr) Unwrap() error        { return e.err }

func (j *Job) fetch(ctx context.Context, f *os.File, ns *netStat, s *segment, buf []byte) (int64, error) {
	j.mu.Lock()
	from, to := s.pos, s.end
	j.mu.Unlock()
	if from >= to {
		return 0, nil
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := newRequest(rctx, j.finalURL, j.opts.Header)
	if err != nil {
		return 0, fatalErr{err}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to-1))
	if j.etag != "" {
		req.Header.Set("If-Range", j.etag)
	} else if j.lastMod != "" {
		req.Header.Set("If-Range", j.lastMod)
	}
	var stalled atomic.Bool
	timer := time.AfterFunc(j.opts.StallTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()

	c := j.clientFor(ns)
	resp, err := c.Do(req)
	if err != nil {
		if stalled.Load() {
			return 0, errors.New("응답 없음(타임아웃)")
		}
		return 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		return 0, fatalErr{errors.New("서버 파일이 바뀌었거나 이어받기를 거부함(HTTP 200) — 취소 후 다시 받아야 함")}
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return 0, errThrottled
	default:
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if st, ok := parseContentRangeStart(resp.Header.Get("Content-Range")); !ok || st != from {
		return 0, fmt.Errorf("잘못된 Content-Range %q", resp.Header.Get("Content-Range"))
	}
	var got int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(j.opts.StallTimeout)
			j.mu.Lock()
			pos, lim := s.pos, s.end-s.pos
			j.mu.Unlock()
			w := int64(n)
			if w > lim {
				w = lim
			}
			if w > 0 {
				if _, err := f.WriteAt(buf[:w], pos); err != nil {
					return got, fatalErr{fmt.Errorf("디스크 쓰기 실패: %w", err)}
				}
			}
			// A thief can only cut s.end at >= pos+MinSplit (>= 256 KiB) and buf is
			// 128 KiB, so this write never lands inside someone else's piece.
			j.mu.Lock()
			s.pos += w
			finished := s.pos >= s.end
			j.mu.Unlock()
			got += w
			j.done.Add(w)
			ns.bytes.Add(w)
			if finished {
				return got, nil
			}
		}
		if rerr != nil {
			if stalled.Load() {
				return got, errors.New("속도 멈춤(타임아웃)")
			}
			if rerr == io.EOF {
				return got, io.ErrUnexpectedEOF
			}
			return got, rerr
		}
	}
}

func (j *Job) clientFor(ns *netStat) *http.Client {
	for _, n := range j.opts.Networks {
		if n.ID == ns.id {
			return n.Client
		}
	}
	return http.DefaultClient
}

// single is for servers without range support: one stream, restart on failure.
func (j *Job) single(ctx context.Context) error {
	var lastErr error
	for attempt := 0; attempt < j.opts.MaxRetries; attempt++ {
		ns := j.nets[attempt%len(j.nets)]
		err := j.singleOnce(ctx, ns)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errFatal) {
			return errors.Unwrap(err)
		}
		ns.lastErr.Store(err.Error())
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("다운로드 실패: %w", lastErr)
}

func (j *Job) singleOnce(ctx context.Context, ns *netStat) error {
	f, err := os.Create(j.partPath())
	if err != nil {
		return fatalErr{err}
	}
	defer f.Close()
	j.done.Store(0)
	for _, n := range j.nets {
		n.bytes.Store(0)
		n.lastBytes = 0
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := newRequest(rctx, j.finalURL, j.opts.Header)
	if err != nil {
		return fatalErr{err}
	}
	var stalled atomic.Bool
	timer := time.AfterFunc(j.opts.StallTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	ns.active.Add(1)
	defer ns.active.Add(-1)
	resp, err := j.clientFor(ns).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 {
		j.mu.Lock()
		j.size = resp.ContentLength
		j.mu.Unlock()
	}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(j.opts.StallTimeout)
			if _, err := f.Write(buf[:n]); err != nil {
				return fatalErr{fmt.Errorf("디스크 쓰기 실패: %w", err)}
			}
			j.done.Add(int64(n))
			ns.bytes.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if stalled.Load() {
				return errors.New("속도 멈춤(타임아웃)")
			}
			return rerr
		}
	}
	if resp.ContentLength >= 0 && j.done.Load() != resp.ContentLength {
		return io.ErrUnexpectedEOF
	}
	j.mu.Lock()
	j.size = j.done.Load()
	j.mu.Unlock()
	return f.Sync()
}

func (j *Job) finalize() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	final := j.finalPath()
	if _, err := os.Stat(final); err == nil {
		// someone created it meanwhile
		ext := filepath.Ext(j.fileName)
		base := strings.TrimSuffix(j.fileName, ext)
		for i := 1; ; i++ {
			n := fmt.Sprintf("%s (%d)%s", base, i, ext)
			if _, err := os.Stat(filepath.Join(j.opts.Dir, n)); err != nil {
				part, ctl := j.partPath(), j.ctlPath()
				j.fileName = n
				if err := os.Rename(part, j.finalPath()); err != nil {
					return err
				}
				os.Remove(ctl)
				return nil
			}
		}
	}
	if err := os.Rename(j.partPath(), final); err != nil {
		return err
	}
	os.Remove(j.ctlPath())
	if j.size < 0 {
		j.size = j.done.Load()
	}
	return nil
}
