package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---- types mirrored from the CLI's JSON ----

type NetInfo struct {
	ID      string   `json:"id"`
	Index   int      `json:"index"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Virtual bool     `json:"virtual"`
	MAC     string   `json:"mac"`
	Addrs   []string `json:"addrs"`
}

type TestResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	IP    string `json:"ip,omitempty"`
	Loc   string `json:"loc,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Error string `json:"error,omitempty"`
}

type Config struct {
	Dir      string   `json:"dir"`
	Networks []string `json:"networks"`
	Conns    int      `json:"conns"`
	Note     string   `json:"note,omitempty"` // e.g. Linux needs setcap for strict binding
}

type NetProgress struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Bytes     int64   `json:"bytes"`
	Speed     float64 `json:"speed"`
	Peak      float64 `json:"peak"`
	Active    int     `json:"active"`
	Conns     int     `json:"conns"`
	Retries   int     `json:"retries"`
	Failed    bool    `json:"failed"`
	LastError string  `json:"lastError,omitempty"`
}

type cliEvent struct {
	Type       string        `json:"type"`
	Message    string        `json:"message"`
	FileName   string        `json:"fileName"`
	Path       string        `json:"path"`
	Size       int64         `json:"size"`
	Done       int64         `json:"done"`
	Speed      float64       `json:"speed"`
	State      string        `json:"state"`
	Error      string        `json:"error"`
	RangeOK    bool          `json:"rangeOK"`
	Throttled  int           `json:"throttled"`
	Networks   []NetProgress `json:"networks"`
	FinishedAt int64         `json:"finishedAt"`
	Map        [][2]int      `json:"map"`
	Cursors    []Cursor      `json:"cursors"`
	Pieces     int           `json:"pieces"`
	StartedAt  int64         `json:"startedAt"`
	Peak       float64       `json:"peak"`
	FinalURL   string        `json:"finalUrl"`
}

type Cursor struct {
	Net   int   `json:"net"`
	Start int64 `json:"start"`
	Pos   int64 `json:"pos"`
	End   int64 `json:"end"`
}

// Item is one download row in the UI.
type Item struct {
	ID         string        `json:"id"`
	URL        string        `json:"url"`
	FileName   string        `json:"fileName"`
	Dir        string        `json:"dir"`
	Path       string        `json:"path"`
	Networks   []string      `json:"networks"`
	Conns      int           `json:"conns"`
	Headers    []string      `json:"headers"`
	State      string        `json:"state"` // starting|probing|downloading|pausing|paused|done|error|canceled
	Size       int64         `json:"size"`
	Done       int64         `json:"done"`
	Speed      float64       `json:"speed"`
	RangeOK    bool          `json:"rangeOK"`
	Throttled  int           `json:"throttled"`
	NetStats   []NetProgress `json:"netStats"`
	Map        [][2]int      `json:"map"`
	Cursors    []Cursor      `json:"cursors"`
	Pieces     int           `json:"pieces"`
	StartedAt  int64         `json:"startedAt"`
	Peak       float64       `json:"peak"`
	FinalURL   string        `json:"finalUrl"`
	Error      string        `json:"error"`
	Warnings   []string      `json:"warnings"`
	CreatedAt  int64         `json:"createdAt"`
	FinishedAt int64         `json:"finishedAt"`
	Command    string        `json:"command"` // the CLI line, for transparency

	proc  *exec.Cmd
	stdin io.WriteCloser
	exit  chan struct{}
}

type App struct {
	ctx     context.Context
	mu      sync.Mutex
	items   []*Item
	seq     int
	dirty   bool
	cli     string
	cliErr  string
	listTag string
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cli, a.cliErr = findCLI()
	a.load()
	go a.ticker()
}

func (a *App) secondInstance(options.SecondInstanceData) {
	wr.WindowUnminimise(a.ctx)
	wr.WindowShow(a.ctx)
}

// beforeClose pauses running downloads so they resume next time.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	var running []*Item
	for _, it := range a.items {
		if it.proc != nil {
			running = append(running, it)
		}
	}
	a.mu.Unlock()
	for _, it := range running {
		a.stopProc(it, "pause")
	}
	a.save()
	return false
}

// ---------- CLI plumbing ----------

func cliName() string {
	if runtime.GOOS == "windows" {
		return "mndl.exe"
	}
	return "mndl"
}

func findCLI() (string, string) {
	if p := os.Getenv("MNDL_CLI"); p != "" {
		return p, ""
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		d := filepath.Dir(exe)
		cands = append(cands, filepath.Join(d, cliName()), filepath.Join(d, "..", "Resources", cliName()))
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, ""
		}
	}
	if p, err := exec.LookPath("mndl"); err == nil {
		return p, ""
	}
	return "", "mndl CLI 를 찾을 수 없음 — 앱과 같은 폴더에 두거나 PATH 에 넣어야 함"
}

func (a *App) command(args ...string) (*exec.Cmd, error) {
	if a.cli == "" {
		return nil, errors.New(a.cliErr)
	}
	c := exec.Command(a.cli, args...)
	hideWindow(c)
	return c, nil
}

func (a *App) runJSON(out any, args ...string) error {
	c, err := a.command(args...)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	b, err := c.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(strings.TrimPrefix(msg, "오류: "))
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// ---------- bound methods: info, networks, config ----------

type AppInfo struct {
	Note       string `json:"note"`
	Version    string `json:"version"`
	CLI        string `json:"cli"`
	CLIVersion string `json:"cliVersion"`
	CLIError   string `json:"cliError"`
	OS         string `json:"os"`
}

func (a *App) Info() AppInfo {
	info := AppInfo{Version: version, CLI: a.cli, CLIError: a.cliErr, OS: runtime.GOOS}
	if c, err := a.command("version"); err == nil {
		if b, err := c.Output(); err == nil {
			info.CLIVersion = strings.TrimSpace(strings.TrimPrefix(string(b), "mndl "))
		}
	}
	return info
}

func (a *App) Networks() ([]NetInfo, error) {
	var list []NetInfo
	err := a.runJSON(&list, "nets", "--all", "--json")
	return list, err
}

func (a *App) TestNetworks(ids []string) ([]TestResult, error) {
	var res []TestResult
	if len(ids) == 0 {
		return res, nil
	}
	err := a.runJSON(&res, "test", "--json", "-n", strings.Join(ids, ","))
	return res, err
}

func (a *App) GetConfig() (Config, error) {
	var c Config
	err := a.runJSON(&c, "config", "--json")
	return c, err
}

func (a *App) SetConfig(c Config) (Config, error) {
	if c.Dir != "" {
		if err := a.runJSON(nil, "config", "dir", c.Dir); err != nil {
			return c, err
		}
	}
	if err := a.runJSON(nil, "config", "networks", strings.Join(c.Networks, ",")); err != nil {
		return c, err
	}
	if c.Conns > 0 {
		if err := a.runJSON(nil, "config", "conns", strconv.Itoa(c.Conns)); err != nil {
			return c, err
		}
	}
	return a.GetConfig()
}

func (a *App) PickDir(current string) (string, error) {
	return wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "저장 위치 선택", DefaultDirectory: current, CanCreateDirectories: true})
}

func (a *App) OpenDir(dir string) error { return reveal(dir, false) }

// ---------- bound methods: downloads ----------

type AddRequest struct {
	URLs     []string `json:"urls"`
	FileName string   `json:"fileName"`
	Headers  []string `json:"headers"`
}

func (a *App) Add(req AddRequest) ([]string, error) {
	cfg, err := a.GetConfig()
	if err != nil {
		return nil, err
	}
	if len(cfg.Networks) == 0 {
		return nil, errors.New("왼쪽에서 네트워크를 하나 이상 골라줘")
	}
	var urls []string
	for _, u := range req.URLs {
		if u = strings.TrimSpace(u); u != "" {
			if !strings.Contains(u, "://") {
				return nil, fmt.Errorf("주소가 이상함: %s", u)
			}
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("다운로드 주소를 넣어줘")
	}
	name := strings.TrimSpace(req.FileName)
	if len(urls) > 1 {
		name = ""
	}
	var ids []string
	a.mu.Lock()
	for _, u := range urls {
		a.seq++
		it := &Item{
			ID: fmt.Sprintf("%d-%d", time.Now().UnixMilli(), a.seq), URL: u, FileName: name, Dir: cfg.Dir,
			Networks: cfg.Networks, Conns: cfg.Conns, Headers: req.Headers, State: "starting",
			CreatedAt: time.Now().UnixMilli(), Size: -1,
		}
		a.items = append([]*Item{it}, a.items...)
		ids = append(ids, it.ID)
		a.startLocked(it)
	}
	a.dirty = true
	a.mu.Unlock()
	a.save()
	return ids, nil
}

func (a *App) startLocked(it *Item) {
	args := []string{"get", "--json", "-n", strings.Join(it.Networks, ","), "-c", strconv.Itoa(it.Conns), "-d", it.Dir}
	if it.FileName != "" {
		args = append(args, "-o", it.FileName)
	}
	for _, h := range it.Headers {
		if strings.TrimSpace(h) != "" {
			args = append(args, "-H", h)
		}
	}
	args = append(args, it.URL)
	it.Command = "mndl " + shellJoin(args)
	c, err := a.command(args...)
	if err != nil {
		it.State, it.Error = "error", err.Error()
		return
	}
	stdout, _ := c.StdoutPipe()
	stdin, _ := c.StdinPipe()
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		it.State, it.Error = "error", err.Error()
		return
	}
	it.proc, it.stdin, it.exit = c, stdin, make(chan struct{})
	it.State, it.Error, it.Warnings = "starting", "", nil
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			var ev cliEvent
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			a.apply(it, ev)
		}
		err := c.Wait()
		a.mu.Lock()
		if it.State == "starting" || it.State == "probing" || it.State == "downloading" || it.State == "pausing" {
			// exited without a final event
			msg := strings.TrimSpace(stderr.String())
			if msg == "" && err != nil {
				msg = err.Error()
			}
			if it.State == "pausing" {
				it.State = "paused"
			} else {
				it.State, it.Error = "error", strings.TrimPrefix(msg, "오류: ")
			}
		}
		it.Speed, it.Cursors = 0, nil
		for i := range it.NetStats {
			it.NetStats[i].Speed, it.NetStats[i].Active = 0, 0
		}
		it.proc, it.stdin = nil, nil
		close(it.exit)
		a.dirty = true
		a.mu.Unlock()
		a.save()
	}()
}

func (a *App) apply(it *Item, ev cliEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ev.Type == "warn" {
		it.Warnings = append(it.Warnings, ev.Message)
		a.dirty = true
		return
	}
	if ev.FileName != "" {
		it.FileName = ev.FileName
	}
	if ev.Path != "" {
		it.Path = ev.Path
	}
	it.Size, it.Done, it.Speed, it.RangeOK, it.Throttled = ev.Size, ev.Done, ev.Speed, ev.RangeOK, ev.Throttled
	if ev.Networks != nil {
		it.NetStats = ev.Networks
	}
	if ev.Map != nil {
		it.Map = ev.Map
	}
	it.Cursors, it.Pieces, it.Peak = ev.Cursors, ev.Pieces, ev.Peak
	if ev.StartedAt > 0 {
		it.StartedAt = ev.StartedAt
	}
	if ev.FinalURL != "" {
		it.FinalURL = ev.FinalURL
	}
	if it.State != "pausing" || ev.Type != "progress" {
		it.State = ev.State
	}
	if ev.Type != "progress" {
		it.Error = ev.Error
		it.FinishedAt = ev.FinishedAt
	}
	a.dirty = true
}

func (a *App) find(id string) *Item {
	for _, it := range a.items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// stopProc sends a command to the CLI (pause/cancel) and waits for it to exit.
func (a *App) stopProc(it *Item, cmd string) {
	a.mu.Lock()
	proc, stdin, exit := it.proc, it.stdin, it.exit
	if proc == nil {
		a.mu.Unlock()
		return
	}
	it.State = "pausing"
	a.dirty = true
	a.mu.Unlock()
	io.WriteString(stdin, cmd+"\n")
	select {
	case <-exit:
	case <-time.After(8 * time.Second):
		proc.Process.Kill()
		<-exit
	}
}

func (a *App) Pause(id string) {
	a.mu.Lock()
	it := a.find(id)
	a.mu.Unlock()
	if it != nil {
		a.stopProc(it, "pause")
	}
}

func (a *App) Resume(id string) error {
	cfg, err := a.GetConfig()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	it := a.find(id)
	if it == nil || it.proc != nil || it.State == "done" {
		return nil
	}
	// resume with the networks/conns picked now; same dir + file name => CLI resumes .part
	if len(cfg.Networks) > 0 {
		it.Networks, it.Conns = cfg.Networks, cfg.Conns
	}
	if it.State == "canceled" {
		it.Done, it.NetStats = 0, nil
	}
	a.startLocked(it)
	a.dirty = true
	return nil
}

func (a *App) Cancel(id string) {
	a.mu.Lock()
	it := a.find(id)
	a.mu.Unlock()
	if it == nil {
		return
	}
	if it.proc != nil {
		a.stopProc(it, "cancel")
	}
	a.mu.Lock()
	name, dir, st := it.FileName, it.Dir, it.State
	if st != "done" {
		it.State, it.Done, it.Speed, it.NetStats, it.Map, it.Cursors = "canceled", 0, 0, nil, nil, nil
	}
	a.dirty = true
	a.mu.Unlock()
	if st != "done" && name != "" {
		a.runJSON(nil, "discard", "-d", dir, name)
	}
	a.save()
}

func (a *App) Remove(id string) {
	a.mu.Lock()
	it := a.find(id)
	a.mu.Unlock()
	if it == nil {
		return
	}
	if it.State != "done" {
		a.Cancel(id)
	}
	a.mu.Lock()
	for i, x := range a.items {
		if x.ID == id {
			a.items = append(a.items[:i], a.items[i+1:]...)
			break
		}
	}
	a.dirty = true
	a.mu.Unlock()
	a.save()
}

func (a *App) ClearFinished() {
	a.mu.Lock()
	keep := a.items[:0]
	for _, it := range a.items {
		if it.State != "done" && it.State != "canceled" {
			keep = append(keep, it)
		}
	}
	a.items = keep
	a.dirty = true
	a.mu.Unlock()
	a.save()
}

func (a *App) Reveal(id string) error {
	a.mu.Lock()
	it := a.find(id)
	a.mu.Unlock()
	if it == nil {
		return nil
	}
	if it.State == "done" && it.Path != "" {
		if _, err := os.Stat(it.Path); err == nil {
			return reveal(it.Path, true)
		}
	}
	return reveal(it.Dir, false)
}

func (a *App) List() []Item {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Item, 0, len(a.items))
	for _, it := range a.items {
		out = append(out, it.copy())
	}
	return out
}

func (it *Item) copy() Item {
	c := *it
	c.proc, c.stdin, c.exit = nil, nil, nil
	c.NetStats = append([]NetProgress(nil), it.NetStats...)
	c.Map = append([][2]int(nil), it.Map...)
	c.Cursors = append([]Cursor(nil), it.Cursors...)
	return c
}

func (a *App) ticker() {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	lastSave := time.Now()
	for range t.C {
		a.mu.Lock()
		dirty := a.dirty
		a.dirty = false
		a.mu.Unlock()
		if dirty {
			wr.EventsEmit(a.ctx, "downloads", a.List())
			if time.Since(lastSave) > 5*time.Second {
				a.save()
				lastSave = time.Now()
			}
		}
	}
}

// ---------- persistence ----------

func listPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "multinet-dl", "ui-downloads.json")
}

func (a *App) save() {
	list := a.List()
	b, _ := json.MarshalIndent(list, "", " ")
	p := listPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p+".tmp", b, 0o644)
	os.Rename(p+".tmp", p)
}

func (a *App) load() {
	b, err := os.ReadFile(listPath())
	if err != nil {
		return
	}
	var list []Item
	if json.Unmarshal(b, &list) != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range list {
		it := list[i]
		switch it.State {
		case "starting", "probing", "downloading", "pausing":
			it.State = "paused"
		}
		it.Speed = 0
		a.items = append(a.items, &it)
	}
}

func shellJoin(args []string) string {
	var parts []string
	for _, s := range args {
		if s == "" || strings.ContainsAny(s, " \t\"'&|;<>()$`\\*?[]#~") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}
