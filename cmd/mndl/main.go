// mndl — download one file over several networks at once.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jeamxn/multinet-dl/internal/config"
	"github.com/jeamxn/multinet-dl/internal/engine"
	"github.com/jeamxn/multinet-dl/internal/netif"
)

var version = "dev"

const usage = `mndl — 네트워크 여러 개로 파일 하나를 쪼개 동시에 받기

사용법:
  mndl get [옵션] URL [URL...]   다운로드 (중간에 끊기면 같은 명령으로 이어받기)
  mndl nets [--all] [--json]      쓸 수 있는 네트워크 목록
  mndl test [-n 목록] [--json]    네트워크별 인터넷 연결·공인 IP 확인
  mndl config [key [value]]       기본값 보기/바꾸기 (dir, networks, conns)
  mndl discard [-d 폴더] 이름    이어받기용 임시 파일(.mndl.part/.mndl.json) 지우기
  mndl version

get 옵션:
  -n, --net   en0,en7     쓸 네트워크(쉼표). 없으면 설정값, 그것도 없으면 물리 네트워크 전부
  -c, --conns 8           네트워크당 동시 연결 수 1~64 (aria2c -x, aria2c는 16이 한계)
  -d, --dir   폴더        저장 위치
  -o, --out   이름        파일 이름 (URL 하나일 때)
  -H, --header "K: V"     요청 헤더 추가 (여러 번 가능)
      --json              진행상황을 JSON 한 줄씩 출력, stdin으로 pause/cancel 받음
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "get", "download", "dl":
		err = cmdGet(os.Args[2:])
	case "nets", "networks", "ls":
		err = cmdNets(os.Args[2:])
	case "test":
		err = cmdTest(os.Args[2:])
	case "config":
		err = cmdConfig(os.Args[2:])
	case "discard":
		err = cmdDiscard(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("mndl", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		if strings.Contains(os.Args[1], "://") {
			err = cmdGet(os.Args[1:])
		} else {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if err != nil {
		var ex exitErr
		if errors.As(err, &ex) {
			os.Exit(int(ex))
		}
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

type exitErr int

func (e exitErr) Error() string { return "exit " + strconv.Itoa(int(e)) }

// parse lets flags and positional args mix: `mndl get URL -n en0`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// ---------- nets ----------

func cmdNets(args []string) error {
	fs := flag.NewFlagSet("nets", flag.ContinueOnError)
	all := fs.Bool("all", false, "VPN·가상 인터페이스도 포함")
	asJSON := fs.Bool("json", false, "JSON 출력")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	list, err := netif.List()
	if err != nil {
		return err
	}
	if !*all {
		var phys []netif.Interface
		for _, it := range list {
			if !it.Virtual {
				phys = append(phys, it)
			}
		}
		list = phys
	}
	if *asJSON {
		if list == nil {
			list = []netif.Interface{}
		}
		return json.NewEncoder(os.Stdout).Encode(list)
	}
	if n := netif.BindingNote(); n != "" {
		fmt.Fprintln(os.Stderr, "주의:", n)
	}
	if len(list) == 0 {
		fmt.Println("연결된 네트워크 없음 (--all 로 VPN·가상 포함)")
		return nil
	}
	fmt.Printf("%-12s %-22s %-9s %s\n", "ID", "이름", "종류", "주소")
	for _, it := range list {
		fmt.Printf("%-12s %-22s %-9s %s\n", it.ID, it.Label, it.Kind, strings.Join(it.Addrs, ", "))
	}
	return nil
}

// ---------- test ----------

type testResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	IP    string `json:"ip,omitempty"`
	Loc   string `json:"loc,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Error string `json:"error,omitempty"`
}

func cmdTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	nets := fs.String("n", "", "네트워크(쉼표)")
	fs.StringVar(nets, "net", "", "")
	asJSON := fs.Bool("json", false, "JSON 출력")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	ids := splitList(*nets)
	ids = append(ids, pos...)
	var list []netif.Interface
	if len(ids) == 0 {
		all, err := netif.List()
		if err != nil {
			return err
		}
		for _, it := range all {
			if !it.Virtual {
				list = append(list, it)
			}
		}
	} else {
		for _, id := range ids {
			it, err := netif.Find(id)
			if err != nil {
				list = append(list, netif.Interface{ID: id, Label: id})
				continue
			}
			list = append(list, *it)
		}
	}
	res := make([]testResult, len(list))
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i] = testOne(&list[i])
		}(i)
	}
	wg.Wait()
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	for _, r := range res {
		if r.OK {
			fmt.Printf("✓ %-10s %-20s 공인IP %-16s %s %dms\n", r.ID, r.Label, r.IP, r.Loc, r.Ms)
		} else {
			fmt.Printf("✗ %-10s %-20s %s\n", r.ID, r.Label, r.Error)
		}
	}
	return nil
}

func testOne(it *netif.Interface) testResult {
	r := testResult{ID: it.ID, Label: it.Label}
	if it.Index == 0 {
		r.Error = "네트워크를 찾을 수 없음"
		return r
	}
	c := it.Client(1)
	c.Timeout = 8 * time.Second
	t0 := time.Now()
	resp, err := c.Get("https://www.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	r.Ms = time.Since(t0).Milliseconds()
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "ip="); ok {
			r.IP = v
		} else if v, ok := strings.CutPrefix(l, "loc="); ok {
			r.Loc = v
		}
	}
	r.OK = resp.StatusCode == 200 && r.IP != ""
	if !r.OK {
		r.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return r
}

// ---------- config ----------

func cmdConfig(args []string) error {
	c := config.Load()
	switch len(args) {
	case 0:
		fmt.Println("파일:", config.Path())
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Println(string(b))
		return nil
	case 1:
		switch args[0] {
		case "dir":
			fmt.Println(c.Dir)
		case "networks":
			fmt.Println(strings.Join(c.Networks, ","))
		case "conns":
			fmt.Println(c.Conns)
		case "--json":
			return json.NewEncoder(os.Stdout).Encode(struct {
				config.Config
				Note string `json:"note,omitempty"`
			}{c, netif.BindingNote()})
		default:
			return fmt.Errorf("모르는 키 %q (dir, networks, conns)", args[0])
		}
		return nil
	}
	val := strings.Join(args[1:], " ")
	switch args[0] {
	case "dir":
		abs, err := filepath.Abs(expandHome(val))
		if err != nil {
			return err
		}
		c.Dir = abs
	case "networks":
		c.Networks = splitList(val)
	case "conns":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > config.MaxConns {
			return fmt.Errorf("conns 는 1~%d", config.MaxConns)
		}
		c.Conns = n
	default:
		return fmt.Errorf("모르는 키 %q (dir, networks, conns)", args[0])
	}
	return config.Save(c)
}

// ---------- discard ----------

func cmdDiscard(args []string) error {
	fs := flag.NewFlagSet("discard", flag.ContinueOnError)
	dir := fs.String("d", "", "")
	fs.StringVar(dir, "dir", "", "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *dir == "" {
		*dir = config.Load().Dir
	}
	d, _ := filepath.Abs(expandHome(*dir))
	for _, n := range names {
		n = engine.SanitizeName(n)
		if n == "" {
			continue
		}
		for _, suf := range []string{".mndl.part", ".mndl.json"} {
			if err := os.Remove(filepath.Join(d, n+suf)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// ---------- get ----------

func cmdGet(args []string) error {
	cfg := config.Load()
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	var nets, dir, out string
	var conns int
	var headers multi
	fs.StringVar(&nets, "n", "", "")
	fs.StringVar(&nets, "net", "", "")
	fs.IntVar(&conns, "c", 0, "")
	fs.IntVar(&conns, "conns", 0, "")
	fs.StringVar(&dir, "d", "", "")
	fs.StringVar(&dir, "dir", "", "")
	fs.StringVar(&out, "o", "", "")
	fs.StringVar(&out, "out", "", "")
	fs.Var(&headers, "H", "")
	fs.Var(&headers, "header", "")
	asJSON := fs.Bool("json", false, "")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	urls, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return errors.New("다운로드 주소가 없음")
	}
	if out != "" && len(urls) > 1 {
		return errors.New("-o 는 URL 하나일 때만 쓸 수 있음")
	}
	if conns <= 0 {
		conns = cfg.Conns
	}
	if conns > config.MaxConns {
		conns = config.MaxConns
	}
	if dir == "" {
		dir = cfg.Dir
	}
	dir, _ = filepath.Abs(expandHome(dir))
	ids := splitList(nets)
	if len(ids) == 0 {
		ids = cfg.Networks
	}
	networks, warn, err := buildNetworks(ids, conns)
	if err != nil {
		return err
	}
	h := http.Header{}
	for _, kv := range headers {
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			return fmt.Errorf("헤더 형식은 \"이름: 값\" (%q)", kv)
		}
		h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}

	emit := newEmitter(*asJSON)
	if n := netif.BindingNote(); n != "" && len(networks) > 1 {
		warn = append(warn, n)
	}
	for _, w := range warn {
		emit.warn(w)
	}

	// control: Ctrl+C / SIGTERM => pause (keeps .part for resume); stdin lines in --json mode
	ctl := make(chan string, 4)
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sig {
			ctl <- "pause"
		}
	}()
	if *asJSON {
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				if cmd := strings.TrimSpace(sc.Text()); cmd != "" {
					ctl <- cmd
				}
			}
		}()
	}

	failed := 0
	for i, u := range urls {
		j := engine.New(fmt.Sprint(i+1), engine.Options{URL: u, Dir: dir, FileName: out, Networks: networks, ConnsPerNetwork: conns, Header: h})
		j.Start()
		st := runJob(j, emit, ctl)
		switch st {
		case engine.StatePaused:
			return exitErr(130)
		case engine.StateCanceled:
			return exitErr(130)
		case engine.StateError:
			failed++
		}
	}
	if failed > 0 {
		return exitErr(1)
	}
	return nil
}

func runJob(j *engine.Job, emit *emitter, ctl chan string) engine.State {
	finished := make(chan struct{})
	go func() { waitRun(j); close(finished) }()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			emit.progress(j.Snapshot())
		case c := <-ctl:
			switch c {
			case "pause", "stop", "quit":
				j.Pause()
			case "cancel":
				j.Cancel()
			}
		case <-finished:
			s := j.Snapshot()
			emit.final(s)
			return s.State
		}
	}
}

// waitRun blocks until the job leaves the running state.
func waitRun(j *engine.Job) {
	for {
		j.Wait()
		if !j.Running() {
			return
		}
	}
}

func buildNetworks(ids []string, conns int) ([]engine.Network, []string, error) {
	all, err := netif.List()
	if err != nil {
		return nil, nil, err
	}
	var picked []netif.Interface
	var warn []string
	if len(ids) == 0 || (len(ids) == 1 && ids[0] == "all") {
		for _, it := range all {
			if !it.Virtual {
				picked = append(picked, it)
			}
		}
	} else {
		for _, id := range ids {
			found := false
			for _, it := range all {
				if it.ID == id || strings.EqualFold(it.Label, id) {
					picked = append(picked, it)
					found = true
					break
				}
			}
			if !found {
				warn = append(warn, fmt.Sprintf("네트워크 %q 는 지금 연결돼 있지 않아서 뺌", id))
			}
		}
	}
	if len(picked) == 0 {
		return nil, warn, errors.New("쓸 수 있는 네트워크가 없음 (mndl nets 로 확인)")
	}
	var out []engine.Network
	for i := range picked {
		it := picked[i]
		out = append(out, engine.Network{ID: it.ID, Label: it.Label, Client: it.Client(conns)})
	}
	return out, warn, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

// ---------- output ----------

type emitter struct {
	json    bool
	enc     *json.Encoder
	lastLen int
	mu      sync.Mutex
	_       context.Context
}

func newEmitter(asJSON bool) *emitter {
	return &emitter{json: asJSON, enc: json.NewEncoder(os.Stdout)}
}

type event struct {
	Type string `json:"type"`
	*engine.Snapshot
	Message string `json:"message,omitempty"`
}

func (e *emitter) warn(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.json {
		e.enc.Encode(event{Type: "warn", Message: msg})
		return
	}
	fmt.Fprintln(os.Stderr, "주의:", msg)
}

func (e *emitter) progress(s engine.Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.json {
		e.enc.Encode(event{Type: "progress", Snapshot: &s})
		return
	}
	line := progressLine(s)
	pad := ""
	if n := e.lastLen - len([]rune(line)); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	e.lastLen = len([]rune(line))
	fmt.Fprint(os.Stderr, "\r"+line+pad)
}

func (e *emitter) final(s engine.Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.json {
		e.enc.Encode(event{Type: string(s.State), Snapshot: &s})
		return
	}
	if e.lastLen > 0 {
		fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", e.lastLen)+"\r")
		e.lastLen = 0
	}
	switch s.State {
	case engine.StateDone:
		dur := time.Duration(s.FinishedAt-s.CreatedAt) * time.Millisecond
		avg := 0.0
		if dur > 0 {
			avg = float64(s.Size) / dur.Seconds()
		}
		fmt.Fprintf(os.Stderr, "✓ %s  %s  %s 평균 %s/s\n", s.Path, human(s.Size), dur.Round(100*time.Millisecond), human(int64(avg)))
		for _, n := range s.Networks {
			pct := 0.0
			if s.Size > 0 {
				pct = float64(n.Bytes) / float64(s.Size) * 100
			}
			fmt.Fprintf(os.Stderr, "   %-20s %9s (%4.1f%%)\n", n.Label, human(n.Bytes), pct)
		}
	case engine.StatePaused:
		fmt.Fprintf(os.Stderr, "⏸ 일시정지 %s (%s/%s) — 같은 명령으로 다시 실행하면 이어받음 (-o %q)\n", s.FileName, human(s.Done), human(s.Size), s.FileName)
	case engine.StateCanceled:
		fmt.Fprintf(os.Stderr, "✗ 취소됨 %s\n", s.FileName)
	default:
		fmt.Fprintf(os.Stderr, "✗ 실패 %s: %s\n", s.URL, s.Error)
	}
}

func progressLine(s engine.Snapshot) string {
	if s.State == engine.StateProbing {
		return "주소 확인 중…"
	}
	pct := ""
	eta := ""
	if s.Size > 0 {
		pct = fmt.Sprintf("%5.1f%% ", float64(s.Done)/float64(s.Size)*100)
		if s.Speed > 0 {
			left := time.Duration(float64(s.Size-s.Done)/s.Speed) * time.Second
			eta = " 남음 " + left.Round(time.Second).String()
		}
	}
	var parts []string
	for _, n := range s.Networks {
		mark := ""
		if n.Failed {
			mark = "✗"
		}
		parts = append(parts, fmt.Sprintf("%s%s %s/s×%d", mark, n.Label, human(int64(n.Speed)), n.Active))
	}
	return fmt.Sprintf("%s%s/%s  %s/s%s | %s", pct, human(s.Done), human(s.Size), human(int64(s.Speed)), eta, strings.Join(parts, "  "))
}

func human(b int64) string {
	if b < 0 {
		return "?"
	}
	const u = 1024
	if b < u {
		return fmt.Sprintf("%dB", b)
	}
	f := float64(b)
	for _, s := range []string{"KB", "MB", "GB", "TB"} {
		f /= u
		if f < u {
			return fmt.Sprintf("%.1f%s", f, s)
		}
	}
	return fmt.Sprintf("%.1fPB", f/u)
}
