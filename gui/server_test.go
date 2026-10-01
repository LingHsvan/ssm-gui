// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
)

const testPort = 8765

type fakeBackend struct {
	runs      chan RunRequest
	extracted []string
}

func (b *fakeBackend) Run(req RunRequest)        { b.runs <- req }
func (b *fakeBackend) Extract(path string) error { b.extracted = append(b.extracted, path); return nil }

type fakeController struct {
	mu     sync.Mutex
	sent   [][]byte
	resets int
}

func (c *fakeController) Send(data []byte) {
	c.mu.Lock()
	c.sent = append(c.sent, data)
	c.mu.Unlock()
}

func (c *fakeController) ResetTouch() {
	c.mu.Lock()
	c.resets++
	c.mu.Unlock()
}

func newTestServer(t *testing.T) (*Server, *fakeBackend, http.Handler) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{runs: make(chan RunRequest, 8)}
	s := NewServer(testPort, conf, backend)
	h, err := s.handler(testPort)
	if err != nil {
		t.Fatal(err)
	}
	return s, backend, h
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func status(t *testing.T, h http.Handler) Status {
	t.Helper()
	var st Status
	if err := json.Unmarshal(do(h, "GET", "/api/status", "").Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func waitRun(t *testing.T, b *fakeBackend) RunRequest {
	t.Helper()
	select {
	case req := <-b.runs:
		return req
	case <-time.After(3 * time.Second):
		t.Fatal("backend.Run was not called")
		return RunRequest{}
	}
}

func TestRejectsForeignHost(t *testing.T) {
	_, _, h := newTestServer(t)
	r := httptest.NewRequest("GET", "/api/status", nil)
	r.Host = "evil.example:8765"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestDeviceAPI(t *testing.T) {
	_, _, h := newTestServer(t)
	if w := do(h, "POST", "/api/device", `{"serial":"A","width":2400,"height":1080}`); w.Code != 200 {
		t.Fatalf("save: %d", w.Code)
	}
	if w := do(h, "POST", "/api/device", `{"serial":"B","width":0,"height":1080}`); w.Code != 400 {
		t.Fatalf("save without width: %d, want 400", w.Code)
	}
	if got := do(h, "GET", "/api/device", "").Body.String(); got != `{"A":{"width":2400,"height":1080}}` {
		t.Fatalf("list: %s", got)
	}
	if w := do(h, "DELETE", "/api/device", `{"serial":"A"}`); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if got := do(h, "GET", "/api/device", "").Body.String(); got != `{}` {
		t.Fatalf("list after delete: %s", got)
	}
}

func TestRunRecordsRequestAndResetsGreatStats(t *testing.T) {
	s, b, h := newTestServer(t)
	s.SetGreatStats(5, 5)
	if w := do(h, "POST", "/api/run", `{"songId":42,"greatCount":3}`); w.Code != 200 {
		t.Fatalf("run: %d", w.Code)
	}
	if req := waitRun(t, b); req.SongID != 42 {
		t.Fatalf("backend got song %d", req.SongID)
	}
	if st := status(t, h); st.GreatReq != 3 || st.GreatApply != 0 {
		t.Fatalf("great stats %d/%d, want 3/0", st.GreatReq, st.GreatApply)
	}
	if w := do(h, "POST", "/api/run", `{`); w.Code != 400 {
		t.Fatalf("bad json: %d, want 400", w.Code)
	}
}

func TestStartNeedsReadySong(t *testing.T) {
	_, _, h := newTestServer(t)
	if w := do(h, "POST", "/api/start", ""); w.Code != http.StatusConflict {
		t.Fatalf("start while idle: %d, want 409", w.Code)
	}
}

func events(timestamps ...int64) []common.ViscousEventItem {
	var out []common.ViscousEventItem
	for _, ts := range timestamps {
		out = append(out, common.ViscousEventItem{Timestamp: ts, Data: []byte{byte(ts)}})
	}
	return out
}

// TestPlaysSongAndRearmsIt walks through load → ready → start → play → done
// → automatic re-arm.
func TestPlaysSongAndRearmsIt(t *testing.T) {
	s, b, h := newTestServer(t)
	do(h, "POST", "/api/run", `{"songId":7}`)
	waitRun(t, b)

	ctrl := &fakeController{}
	s.SetReady(ctrl, events(0, 10, 20), NowPlaying{Title: "song"})
	if st := status(t, h); st.State != StateReady || st.NowPlaying.Title != "song" {
		t.Fatalf("after SetReady: %+v", st)
	}

	if w := do(h, "POST", "/api/start", ""); w.Code != 200 {
		t.Fatalf("start: %d", w.Code)
	}
	if !s.WaitForStart(context.Background()) {
		t.Fatal("WaitForStart returned false")
	}
	if st := status(t, h); st.State != StatePlaying {
		t.Fatalf("state %d, want playing", st.State)
	}

	s.Autoplay(context.Background(), time.Now())
	if len(ctrl.sent) != 3 || ctrl.resets != 2 {
		t.Fatalf("sent %d events, %d resets; want 3 and 2", len(ctrl.sent), ctrl.resets)
	}
	if st := status(t, h); st.State != StateDone {
		t.Fatalf("state %d, want done", st.State)
	}
	if req := waitRun(t, b); req.SongID != 7 {
		t.Fatalf("re-armed song %d, want 7", req.SongID)
	}
	if st := status(t, h); st.State != StateIdle {
		t.Fatalf("state %d, want idle while re-arming", st.State)
	}
}

func TestRestartInterruptsPlayback(t *testing.T) {
	s, b, h := newTestServer(t)
	do(h, "POST", "/api/run", `{"songId":9}`)
	waitRun(t, b)

	ctrl := &fakeController{}
	s.SetReady(ctrl, events(0, 60_000), NowPlaying{})
	do(h, "POST", "/api/start", "")
	s.WaitForStart(context.Background())

	played := make(chan struct{})
	go func() {
		s.Autoplay(context.Background(), time.Now())
		close(played)
	}()
	time.Sleep(50 * time.Millisecond)

	do(h, "POST", "/api/restart", "")
	select {
	case <-played:
	case <-time.After(2 * time.Second):
		t.Fatal("restart did not interrupt playback")
	}
	if req := waitRun(t, b); req.SongID != 9 {
		t.Fatalf("restarted song %d, want 9", req.SongID)
	}
	if len(ctrl.sent) != 1 {
		t.Fatalf("sent %d events before restart, want 1", len(ctrl.sent))
	}
}

func TestOffsetIsReportedAndApplied(t *testing.T) {
	s, _, h := newTestServer(t)
	do(h, "POST", "/api/offset", `{"delta":-20}`)
	if st := status(t, h); st.Offset != -20 {
		t.Fatalf("offset %d, want -20", st.Offset)
	}
	if got := <-s.song.offset; got != -20 {
		t.Fatalf("playback got delta %d", got)
	}
}

func TestEventsStartWithCurrentStatus(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetError("boom")

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleEvents(w, r)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	first, _, _ := strings.Cut(w.Body.String(), "\n\n")
	var st Status
	if err := json.Unmarshal([]byte(strings.TrimPrefix(first, "data: ")), &st); err != nil {
		t.Fatalf("first event %q: %v", first, err)
	}
	if st.State != StateError || st.Error != "boom" {
		t.Fatalf("first event %+v", st)
	}
}

func TestExtractChecksPath(t *testing.T) {
	_, b, h := newTestServer(t)
	if w := do(h, "POST", "/api/extract", `{"path":"/no/such/dir"}`); w.Code != 400 {
		t.Fatalf("missing dir: %d, want 400", w.Code)
	}
	dir := t.TempDir()
	body, _ := json.Marshal(map[string]string{"path": dir})
	if w := do(h, "POST", "/api/extract", string(body)); w.Code != 200 || len(b.extracted) != 1 {
		t.Fatalf("extract: %d, calls %v", w.Code, b.extracted)
	}
}

func TestCachedSourceFallsBackToCache(t *testing.T) {
	reply := `{"v":1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reply == "" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	src := cachedSource{cacheFile: filepath.Join(t.TempDir(), "cache.json"), url: srv.URL}

	if got, err := src.load(); err != nil || string(got) != `{"v":1}` {
		t.Fatalf("online load: %s, %v", got, err)
	}
	reply = ""
	if got, err := src.load(); err != nil || string(got) != `{"v":1}` {
		t.Fatalf("offline load: %s, %v", got, err)
	}
	reply = "<html>captive portal</html>"
	if got, err := src.load(); err != nil || string(got) != `{"v":1}` {
		t.Fatalf("non-JSON reply must not replace the cache: %s, %v", got, err)
	}
}
