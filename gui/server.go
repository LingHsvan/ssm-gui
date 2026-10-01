// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/db"
	"github.com/kvarenzn/ssm/log"
)

//go:embed frontend/dist
var staticFiles embed.FS

type PlayState int

const (
	StateIdle    PlayState = iota // 0 Idle
	StateReady                    // 1 Ready (waiting to start)
	StatePlaying                  // 2 Playing
	StateDone                     // 3 Finished
	StateError                    // 4 Error
)

type NowPlaying struct {
	SongID    int    `json:"songId"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Diff      string `json:"diff"`
	DiffLevel int    `json:"diffLevel"`
	JacketURL string `json:"jacketUrl"`
	Mode      string `json:"mode"`
}

type RunRequest struct {
	Mode         string     `json:"mode"`
	Backend      string     `json:"backend"`
	Diff         string     `json:"diff"`
	Orient       string     `json:"orient"`
	SongID       int        `json:"songId"`
	ChartPath    string     `json:"chartPath"`
	DeviceSerial string     `json:"deviceSerial"`
	NowPlaying   NowPlaying `json:"nowPlaying"`

	// Jitter settings
	TimingJitter   int64   `json:"timingJitter"`   // Time jitter (ms), 0 = disabled
	PositionJitter float64 `json:"positionJitter"` // Position jitter (track units), 0 = disabled
	TapDurJitter   int64   `json:"tapDurJitter"`   // Tap duration jitter (ms), 0 = disabled
	GreatOffsetMs  int64   `json:"greatOffsetMs"`  // Absolute offset in ms
	GreatCount     int64   `json:"greatCount"`     // Exact number of tap notes to force as Great, 0 = probability mode

	// Advanced VTE parameters (0 = use mode default)
	TapDuration         int64   `json:"tapDuration"`
	FlickDuration       int64   `json:"flickDuration"`
	FlickReportInterval int64   `json:"flickReportInterval"`
	SlideReportInterval int64   `json:"slideReportInterval"`
	FlickFactor         float64 `json:"flickFactor"`
	FlickPow            float64 `json:"flickPow"`
	// FlickLeadMs advances the whole flick gesture by this many ms. Pointer so
	// "absent" (keep the mode default) is distinguishable from an explicit 0
	// (no lead): Our Notes defaults to 30 while bang/pjsk default to 0.
	FlickLeadMs *int64 `json:"flickLeadMs"`
}

type Server struct {
	port int
	conf *config.Config

	mu         sync.Mutex
	state      PlayState
	offset     int
	errMsg     string
	nowPlaying NowPlaying
	lastRunReq RunRequest
	greatReq   int64
	greatApply int64

	// adbBusy is set while an adb-based controller (scrcpy) is being opened
	// or is in use, so Auto Detect never stops the shared adb server under it.
	adbBusy atomic.Bool

	startCh  chan struct{}
	offsetCh chan int
	stopCh   chan struct{}

	controller controllers.Controller
	events     []common.ViscousEventItem

	clientsMu sync.Mutex
	clients   map[chan string]struct{}

	OnRunRequest     func(req RunRequest)
	OnExtractRequest func(path string) error
}

func NewServer(port int, conf *config.Config) *Server {
	s := &Server{
		port:    port,
		conf:    conf,
		state:   StateIdle,
		clients: make(map[chan string]struct{}),
	}
	s.startCh = make(chan struct{}, 1)
	s.stopCh = make(chan struct{})
	s.offsetCh = make(chan int, 32)
	return s
}

// MarkAdbBusy records whether an adb-based controller (scrcpy) is being
// opened or used. While busy, Auto Detect must not stop the shared adb
// server the playback path depends on.
func (s *Server) MarkAdbBusy(busy bool) {
	s.adbBusy.Store(busy)
}

// ─── SSE ───────────────────────────────────────

func (s *Server) addClient(ch chan string) {
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(ch chan string) {
	s.clientsMu.Lock()
	delete(s.clients, ch)
	s.clientsMu.Unlock()
}

func (s *Server) broadcast(msg string) {
	s.clientsMu.Lock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	s.clientsMu.Unlock()
}

func (s *Server) broadcastState() {
	s.mu.Lock()
	data := map[string]interface{}{
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
		"greatReq":   s.greatReq,
		"greatApply": s.greatApply,
	}
	s.mu.Unlock()
	b, _ := json.Marshal(data)
	s.broadcast("data: " + string(b) + "\n\n")
}

// ─── HTTP handlers ─────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	ch := make(chan string, 16)
	s.addClient(ch)
	defer s.removeClient(ch)
	s.broadcastState()
	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	data := map[string]interface{}{
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
		"greatReq":   s.greatReq,
		"greatApply": s.greatApply,
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.lastRunReq = req
	s.greatReq = req.GreatCount
	s.greatApply = 0
	s.mu.Unlock()

	if s.OnRunRequest != nil {
		// Never block the HTTP response on the run queue: a previous run
		// that is still stopping must not make "Load" hang. The state
		// advances over SSE as the run actually makes progress.
		go s.OnRunRequest(req)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	st := s.state
	ch := s.startCh
	s.mu.Unlock()

	if st != StateReady {
		http.Error(w, "not ready", http.StatusConflict)
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleOffset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Delta int `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.offset += body.Delta
	ch := s.offsetCh
	s.mu.Unlock()
	select {
	case ch <- body.Delta:
	default:
	}
	s.broadcastState()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	st := s.state
	req := s.lastRunReq
	s.mu.Unlock()

	if st != StatePlaying && st != StateDone {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Interrupt the in-flight playback, then re-prepare the same song back to
	// Ready so the user can re-sync and press Start again without re-loading.
	s.mu.Lock()
	oldStop := s.stopCh
	s.mu.Unlock()
	select {
	case <-oldStop:
	default:
		close(oldStop)
	}

	s.mu.Lock()
	s.state = StateIdle
	s.mu.Unlock()
	s.broadcastState()

	if s.OnRunRequest != nil {
		go s.OnRunRequest(req)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.conf.Snapshot())
	case http.MethodPost:
		var body struct {
			Serial string `json:"serial"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.conf.SetDevice(body.Serial, body.Width, body.Height)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		var body struct {
			Serial string `json:"serial"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.conf.DeleteDevice(body.Serial)
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleDeviceROI exposes the per-device song-detection calibrations that
// /api/detect-song stores:
//
//	GET    /api/device-roi                 -> {serial: {mode: [x,y,w,h]}}
//	DELETE /api/device-roi {serial, mode?} -> clear that device's calibration
//
// An empty mode clears every mode of the device. This is a separate endpoint
// on purpose: /api/device answers {serial: {width, height}} and that shape is
// consumed by pickDetectDevice and the run/settings UI, so it must not change.
func (s *Server) handleDeviceROI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.conf.DeviceROIs())
	case http.MethodDelete:
		var body struct {
			Serial string `json:"serial"`
			Mode   string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Serial == "" {
			http.Error(w, "serial required", http.StatusBadRequest)
			return
		}
		// A mode in the body selects one bucket; leaving it out means "all of
		// this device's modes". NormalizeMode would fold "" into bang, so it is
		// only applied when a mode was actually given.
		mode := ""
		if body.Mode != "" {
			mode = common.NormalizeMode(body.Mode)
		}
		if err := s.conf.ClearDeviceROI(body.Serial, mode); err != nil {
			http.Error(w, "persist config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTuning reads and writes the GUI's fine-tuning panels (humanization
// jitter + advanced parameters) so the user's tuning survives a restart.
//
// GET returns both panels; POST stores the jitter panel and, when a mode is
// given, that mode's advanced parameters. Advanced values are per-game-mode
// because each mode ships its own defaults.
func (s *Server) handleTuning(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jitter, advanced := s.conf.Tuning()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jitter":   jitter,
			"advanced": advanced,
		})
	case http.MethodPost:
		var body struct {
			Mode     string                 `json:"mode"`
			Jitter   *config.JitterConfig   `json:"jitter"`
			Advanced *config.AdvancedConfig `json:"advanced"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// An empty mode means "jitter only": NormalizeMode would fold it into
		// bang and overwrite that mode's bucket.
		mode := ""
		if body.Mode != "" {
			mode = common.NormalizeMode(body.Mode)
		}
		if err := s.conf.SetTuning(body.Jitter, mode, body.Advanced); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSongDB(w http.ResponseWriter, r *http.Request) {
	mode := common.NormalizeMode(r.URL.Query().Get("mode"))
	w.Header().Set("Content-Type", "application/json")

	switch mode {
	case common.ModeOurNotes:
		notesDB, err := db.NewOurNotesDB()
		if err != nil {
			http.Error(w, `{"error":"ournotes database: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		payload, err := notesDB.Payload()
		if err != nil {
			http.Error(w, `{"error":"ournotes payload: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		w.Write(payload)

	case common.ModePjsk:
		const sekaiMusicsURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musics.json"
		const sekaiMusicDifficultiesURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musicDifficulties.json"
		const sekaiMusicArtistsURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musicArtists.json"

		songs, err := loadLocalFirst("./sekai_master_db_diff_musics.json", sekaiMusicsURL)
		if err != nil {
			http.Error(w, `{"error":"songs EN fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		difficulties, err := loadLocalFirst("./sekai_master_db_diff_music_difficulties.json", sekaiMusicDifficultiesURL)
		if err != nil {
			http.Error(w, `{"error":"difficulties fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		artists, err := loadLocalFirst("./sekai_master_db_diff_music_artists.json", sekaiMusicArtistsURL)
		if err != nil {
			http.Error(w, `{"error":"artists fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"songs":%s,"songsJp":%s,"bands":{},"artists":%s,"musicDifficulties":%s}`, songs, songs, artists, difficulties)

	default: // common.ModeBang
		songs, err := loadLocalFirst("./all.5.json", "https://bestdori.com/api/songs/all.5.json")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		bands, err := loadLocalFirst("./all.1.json", "https://bestdori.com/api/bands/all.1.json")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"songs":%s,"bands":%s}`, songs, bands)
	}
}

// handleOurNotesJacket serves a locally unpacked Our Notes jacket image:
//
//	GET /api/ournotes-jacket?id=<songId>&size=full|thumb
//
// Requests for an unknown song (or a song without artwork) fall back to the
// placeholder jacket so the UI always has an image to show.
func (s *Server) handleOurNotesJacket(w http.ResponseWriter, r *http.Request) {
	var id int
	fmt.Sscanf(r.URL.Query().Get("id"), "%d", &id)
	thumb := r.URL.Query().Get("size") == "thumb"

	file := ""
	if notesDB, err := db.NewOurNotesDB(); err == nil {
		if p, ok := notesDB.JacketPath(id, thumb); ok {
			file = p
		}
	}
	if file == "" || !fileExists(file) {
		file = filepath.Join(db.OurNotesDir, "jackets", "jacket_temporary.png")
		if thumb {
			file = filepath.Join(db.OurNotesDir, "jackets", "small", "jacket_temporary.png")
		}
	}
	if !fileExists(file) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, file)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func fetchOrLoad(localPath, url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			data, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				if localData, localErr := os.ReadFile(localPath); localErr != nil || !bytes.Equal(localData, data) {
					writeFileAtomic(localPath, data)
				}
				return data, nil
			}
		}
	}
	if data, readErr := os.ReadFile(localPath); readErr == nil {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("failed to fetch %s and local cache missing", url)
}

// writeFileAtomic writes data to a temp file and renames it into place, so a
// concurrent reader never sees a half-written cache file. Best-effort: write
// errors are ignored because the cache is non-essential.
func writeFileAtomic(path string, data []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, "path field is required", http.StatusBadRequest)
		return
	}
	if s.OnExtractRequest == nil {
		http.Error(w, "extract not configured", http.StatusInternalServerError)
		return
	}
	if err := s.OnExtractRequest(body.Path); err != nil {
		http.Error(w, "Extraction failed:"+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Playback state control ───────────────────────────────

func (s *Server) SetReady(ctrl controllers.Controller, events []common.ViscousEventItem, np NowPlaying) {
	s.mu.Lock()
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	s.controller = ctrl
	s.events = events
	s.state = StateReady
	s.offset = 0
	s.errMsg = ""
	s.nowPlaying = np
	s.startCh = make(chan struct{}, 1)
	s.stopCh = make(chan struct{})
	s.offsetCh = make(chan int, 32)
	s.mu.Unlock()

	// Perform a reset when ready instead of at playback start
	if sc, ok := ctrl.(*controllers.ScrcpyController); ok {
		sc.ResetTouch()
	}

	s.broadcastState()
}

func (s *Server) SetError(msg string) {
	s.mu.Lock()
	s.state = StateError
	s.errMsg = msg
	s.mu.Unlock()
	s.broadcastState()
}

func (s *Server) SetGreatStats(requested, applied int64) {
	s.mu.Lock()
	if requested < 0 {
		requested = 0
	}
	if applied < 0 {
		applied = 0
	}
	s.greatReq = requested
	s.greatApply = applied
	s.mu.Unlock()
	s.broadcastState()
}

func (s *Server) WaitForStart(ctx context.Context) bool {
	s.mu.Lock()
	startCh := s.startCh
	stopCh := s.stopCh
	s.mu.Unlock()

	select {
	case <-startCh:
		if ctx.Err() != nil {
			return false
		}
		s.mu.Lock()
		s.state = StatePlaying
		s.mu.Unlock()
		go s.broadcastState()
		return true
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	}
}

func (s *Server) Autoplay(ctx context.Context, start time.Time) {
	s.mu.Lock()
	stopCh := s.stopCh
	events := s.events
	offsetCh := s.offsetCh
	ctrl := s.controller
	s.mu.Unlock()

	n := len(events)
	current := 0

	for current < n {
		select {
		case <-stopCh:
			goto done
		case <-ctx.Done():
			goto done
		default:
		}

		select {
		case delta := <-offsetCh:
			start = start.Add(time.Duration(-delta) * time.Millisecond)
		default:
		}

		now := time.Since(start).Milliseconds()
		event := events[current]
		remaining := event.Timestamp - now

		if remaining <= 0 {
			ctrl.Send(event.Data)
			if sc, ok := ctrl.(*controllers.ScrcpyController); ok && sc.Broken() {
				// The control connection died: abort instead of running through
				// the rest of the chart against a dead socket.
				log.Warnf("[GUI] autoplay aborted early: control connection to the device was lost")
				goto done
			}
			current++
			continue
		}

		if remaining > 10 {
			select {
			case <-stopCh:
				goto done
			case <-ctx.Done():
				goto done
			case <-time.After(time.Duration(remaining-5) * time.Millisecond):
			}
		} else if remaining > 4 {
			time.Sleep(1 * time.Millisecond)
		}
	}

done:
	s.mu.Lock()
	doneCtrl := s.controller
	s.mu.Unlock()
	if sc, ok := doneCtrl.(*controllers.ScrcpyController); ok {
		sc.ResetTouch()
	}
	s.mu.Lock()
	if s.state == StatePlaying {
		s.state = StateDone
		req := s.lastRunReq
		s.mu.Unlock()
		s.broadcastState()

		go func() {
			// Show "Done" briefly, then re-prepare the same song back to Ready
			// so it can be replayed with Start (no re-load needed), matching the
			// Restart button.
			time.Sleep(1000 * time.Millisecond)

			s.mu.Lock()
			if s.state != StateDone || s.lastRunReq != req {
				s.mu.Unlock()
				return
			}
			s.state = StateIdle
			s.mu.Unlock()
			s.broadcastState()

			if s.OnRunRequest != nil {
				s.OnRunRequest(req)
			}
		}()
	} else {
		s.mu.Unlock()
	}
}

// ─── ADB Utils ──────────────────────────────────────

func (s *Server) handleKillAdb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Bound the call: a wedged adb binary must not hang this HTTP request.
	// Errors are still ignored on purpose (kill is best-effort).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "adb", "kill-server")
	_ = cmd.Run()

	w.WriteHeader(http.StatusOK)
}

// handleDetectAdb powers the "Auto Detect" button: it starts the adb server
// when needed (a cold server not having been started by `adb devices` is
// exactly why detection used to fail), waits for the device list to settle,
// registers the found device's resolution, and restores the previous server
// state before replying.
func (s *Server) handleDetectAdb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// Serialize with the detect-song screencap cycle (songocr.go): both
	// start/stop the one global adb server.
	adbCaptureMu.Lock()
	defer adbCaptureMu.Unlock()

	// Only restore "server off" when the server was off before this call; a
	// server the user (or a capture cycle) already had running is left alone.
	// Never stop it while an adb controller (scrcpy) is being opened or a
	// run is not idle — that yanks the server out from under a transfer
	// (forward / push / screencap) that is still using it.
	startedByUs := !adb.IsADBServerRunning("localhost", 5037)
	defer func() {
		if !startedByUs || s.adbBusy.Load() {
			return
		}
		s.mu.Lock()
		idle := s.state == StateIdle
		s.mu.Unlock()
		if idle {
			_ = adb.StopADBServer("localhost", 5037)
		}
	}()

	client := adb.NewDefaultClient()

	// A cold adb server takes ~2s to enumerate USB devices, and a server can
	// also die mid-poll — so every iteration (re)starts it and re-lists.
	var devices []*adb.Device
	deadline := time.Now().Add(8 * time.Second)
	for {
		if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
			break
		}
		if ds, err := client.Devices(); err == nil {
			devices = ds
			if adb.FirstAuthorizedDevice(ds) != nil {
				break
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Prefer a device already registered in Device Management; fall back to
	// the first authorized device — Auto Detect is how new devices get added.
	device := adb.PickRecordedOrFirstAuthorized(devices, s.conf.RecordedSerials())
	if device == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"serial": "", "seen": len(devices)})
		return
	}

	// Register (or refresh) the device's resolution so playback can start
	// right away — the same data Settings' "Add / Update Device" stores.
	width, height := 0, 0
	if out, err := device.Sh("wm", "size"); err == nil {
		width, height = adb.ParseWMSize(out)
	}
	saved := "none"
	if width > 0 && height > 0 {
		_, existed := s.conf.Snapshot()[device.Serial()]
		if err := s.conf.SetDevice(device.Serial(), width, height); err == nil {
			if existed {
				saved = "updated"
			} else {
				saved = "added"
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"serial": device.Serial(),
		"width":  width,
		"height": height,
		"saved":  saved,
		"seen":   len(devices),
	})
}

// ─── Startup ──────────────────────────────────────

func (s *Server) Start() (string, error) {
	staticFS, err := fs.Sub(staticFiles, "frontend/dist")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Avoid stale frontend assets after rebuilding embedded dist files.
		if strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		staticHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/offset", s.handleOffset)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/device", s.handleDevice)
	mux.HandleFunc("/api/device-roi", s.handleDeviceROI)
	mux.HandleFunc("/api/tuning", s.handleTuning)
	mux.HandleFunc("/api/extract", s.handleExtract)
	mux.HandleFunc("/api/songdb", s.handleSongDB)
	mux.HandleFunc("/api/ournotes-jacket", s.handleOurNotesJacket)
	mux.HandleFunc("/api/kill-adb", s.handleKillAdb)
	mux.HandleFunc("/api/detect-adb", s.handleDetectAdb)
	mux.HandleFunc("/api/detect-song", s.handleDetectSong)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return "", err
	}

	port := ln.Addr().(*net.TCPAddr).Port
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Reject requests whose Host header isn't our loopback address. This
	// blocks DNS-rebinding attacks where a remote page resolves a hostname
	// to 127.0.0.1 and drives these side-effecting endpoints.
	allowedHosts := map[string]struct{}{
		fmt.Sprintf("127.0.0.1:%d", port): {},
		fmt.Sprintf("localhost:%d", port): {},
	}
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowedHosts[r.Host]; !ok {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Handler:           guarded,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go srv.Serve(ln)
	return addr, nil
}
