package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// throttled conn: limits read speed to emulate a slow network.
type slowConn struct {
	net.Conn
	bps  int
	fail *atomic.Bool
}

func (c slowConn) Read(p []byte) (int, error) {
	if len(p) > 16<<10 {
		p = p[:16<<10]
	}
	if c.fail != nil && c.fail.Load() {
		c.Conn.Close()
		return 0, errors.New("network down")
	}
	n, err := c.Conn.Read(p)
	if n > 0 && c.bps > 0 {
		time.Sleep(time.Duration(float64(n) / float64(c.bps) * float64(time.Second)))
	}
	return n, err
}

func fakeNet(id string, bps int, fail *atomic.Bool) Network {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, nw, addr string) (net.Conn, error) {
			if fail != nil && fail.Load() {
				return nil, errors.New("network down")
			}
			c, err := (&net.Dialer{}).DialContext(ctx, nw, addr)
			if err != nil {
				return nil, err
			}
			return slowConn{c, bps, fail}, nil
		},
		DisableKeepAlives: false,
	}
	return Network{ID: id, Label: id, Client: &http.Client{Transport: tr}}
}

func server(t *testing.T, data []byte, ranges bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ranges {
			r.Header.Del("Range")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(data)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "blob.bin", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(s.Close)
	return s
}

func randData(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func sum(b []byte) [32]byte { return sha256.Sum256(b) }

func waitDone(t *testing.T, j *Job, d time.Duration) Snapshot {
	t.Helper()
	done := make(chan struct{})
	go func() { j.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("timeout: %+v", j.Snapshot())
	}
	return j.Snapshot()
}

func TestMultiNetworkSplit(t *testing.T) {
	data := randData(32 << 20)
	s := server(t, data, true)
	dir := t.TempDir()
	j := New("a", Options{URL: s.URL + "/files/blob.bin", Dir: dir, ConnsPerNetwork: 4, MinSplit: 512 << 10,
		Networks: []Network{fakeNet("fast", 3<<20, nil), fakeNet("slow", 1<<20, nil)}})
	j.Start()
	snap := waitDone(t, j, 60*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state %s err %s", snap.State, snap.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "blob.bin"))
	if err != nil || sum(got) != sum(data) {
		t.Fatalf("hash mismatch err=%v len=%d", err, len(got))
	}
	fast, slow := snap.Networks[0].Bytes, snap.Networks[1].Bytes
	t.Logf("fast=%d slow=%d", fast, slow)
	if fast+slow != int64(len(data)) || slow == 0 || fast <= slow {
		t.Fatalf("bad split fast=%d slow=%d", fast, slow)
	}
	if len(snap.Map) != MapCells {
		t.Fatalf("map cells %d", len(snap.Map))
	}
	owners := map[int]int{}
	for _, c := range snap.Map {
		if c[1] != 100 {
			t.Fatalf("cell not full after done: %v", c)
		}
		owners[c[0]]++
	}
	if owners[0] == 0 || owners[1] == 0 || owners[0] <= owners[1] {
		t.Fatalf("map owners %v", owners)
	}
	if _, err := os.Stat(filepath.Join(dir, "blob.bin.mndl.json")); err == nil {
		t.Fatal("control file left behind")
	}
}

func TestNetworkDiesMidway(t *testing.T) {
	data := randData(16 << 20)
	s := server(t, data, true)
	dir := t.TempDir()
	var down atomic.Bool
	j := New("b", Options{URL: s.URL + "/x.bin", Dir: dir, ConnsPerNetwork: 3, MaxRetries: 3,
		Networks: []Network{fakeNet("ok", 1<<20, nil), fakeNet("flaky", 1<<20, &down)}})
	j.Start()
	time.Sleep(2 * time.Second)
	down.Store(true)
	// kill existing connections of the flaky network
	j.opts.Networks[1].Client.Transport.(*http.Transport).CloseIdleConnections()
	snap := waitDone(t, j, 90*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state %s err %s", snap.State, snap.Error)
	}
	if !snap.Networks[1].Failed || snap.Networks[1].Bytes == 0 {
		t.Fatalf("flaky should have helped then failed: %+v", snap.Networks[1])
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.bin"))
	if sum(got) != sum(data) {
		t.Fatal("hash mismatch")
	}
	t.Logf("ok=%d flaky=%d", snap.Networks[0].Bytes, snap.Networks[1].Bytes)
}

func TestPauseResumeAcrossProcess(t *testing.T) {
	data := randData(20 << 20)
	s := server(t, data, true)
	dir := t.TempDir()
	opts := Options{URL: s.URL + "/r.bin", Dir: dir, ConnsPerNetwork: 4, Networks: []Network{fakeNet("n", 1<<20, nil)}}
	j := New("c", opts)
	j.Start()
	time.Sleep(1200 * time.Millisecond)
	j.Pause()
	first := j.Snapshot()
	if first.State != StatePaused || first.Done == 0 || first.Done >= int64(len(data)) {
		t.Fatalf("pause snapshot %+v", first)
	}
	// new job (like a restarted app/cli) with the same file name resumes
	opts.FileName = first.FileName
	j2 := New("c2", opts)
	j2.Start()
	snap := waitDone(t, j2, 60*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state %s %s", snap.State, snap.Error)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "r.bin"))
	if sum(got) != sum(data) {
		t.Fatal("hash mismatch after resume")
	}
	t.Logf("paused at %d, total net bytes after resume %d", first.Done, snap.Networks[0].Bytes)
	if snap.Networks[0].Bytes != int64(len(data)) {
		t.Fatalf("resume re-downloaded data: %d", snap.Networks[0].Bytes)
	}
	prev := 0
	for _, c := range snap.Map {
		if c[0] == -2 {
			prev++
		}
	}
	if prev == 0 {
		t.Fatal("map should show the part from the earlier run")
	}
}

func TestNoRangeServer(t *testing.T) {
	data := randData(3 << 20)
	s := server(t, data, false)
	dir := t.TempDir()
	j := New("d", Options{URL: s.URL + "/plain", Dir: dir, Networks: []Network{fakeNet("a", 0, nil), fakeNet("b", 0, nil)}})
	j.Start()
	snap := waitDone(t, j, 30*time.Second)
	if snap.State != StateDone || snap.RangeOK {
		t.Fatalf("%+v", snap)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "plain"))
	if sum(got) != sum(data) {
		t.Fatal("hash mismatch")
	}
}

func TestNoOverwrite(t *testing.T) {
	data := randData(2 << 20)
	s := server(t, data, true)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.bin"), []byte("keep"), 0o644)
	j := New("e", Options{URL: s.URL + "/f.bin", Dir: dir, Networks: []Network{fakeNet("a", 0, nil)}})
	j.Start()
	snap := waitDone(t, j, 30*time.Second)
	if snap.FileName != "f (1).bin" {
		t.Fatalf("name %q", snap.FileName)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin")); string(b) != "keep" {
		t.Fatal("overwrote")
	}
}

func TestServerLimitsConnections(t *testing.T) {
	data := randData(12 << 20)
	var inflight atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inflight.Add(1) > 3 {
			inflight.Add(-1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		defer inflight.Add(-1)
		http.ServeContent(w, r, "l.bin", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(s.Close)
	dir := t.TempDir()
	j := New("f", Options{URL: s.URL + "/l.bin", Dir: dir, ConnsPerNetwork: 8,
		Networks: []Network{fakeNet("a", 2<<20, nil), fakeNet("b", 2<<20, nil)}})
	j.Start()
	snap := waitDone(t, j, 60*time.Second)
	if snap.State != StateDone {
		t.Fatalf("state %s %s", snap.State, snap.Error)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "l.bin"))
	if sum(got) != sum(data) {
		t.Fatal("hash mismatch")
	}
	t.Logf("throttled=%d a=%d b=%d", snap.Throttled, snap.Networks[0].Bytes, snap.Networks[1].Bytes)
	if snap.Throttled == 0 {
		t.Fatal("expected throttled connections")
	}
}

func TestLiveCursors(t *testing.T) {
	data := randData(16 << 20)
	s := server(t, data, true)
	j := New("g", Options{URL: s.URL + "/c.bin", Dir: t.TempDir(), ConnsPerNetwork: 3,
		Networks: []Network{fakeNet("a", 512<<10, nil), fakeNet("b", 512<<10, nil)}})
	j.Start()
	time.Sleep(1500 * time.Millisecond)
	mid := j.Snapshot()
	if len(mid.Cursors) != 6 {
		t.Fatalf("want 6 live connections, got %d", len(mid.Cursors))
	}
	for _, c := range mid.Cursors {
		if c.Pos < c.Start || c.Pos > c.End {
			t.Fatalf("bad cursor %+v", c)
		}
	}
	if mid.StartedAt == 0 || mid.Networks[0].Conns != 3 {
		t.Fatalf("%+v", mid)
	}
	if snap := waitDone(t, j, 60*time.Second); snap.State != StateDone || snap.Peak <= 0 {
		t.Fatalf("%s peak=%f", snap.State, snap.Peak)
	}
}
