// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ocr "github.com/getcharzp/go-ocr"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/songmatch"
)

// ─────────────────────────────────────────────────────────────
// Song OCR engine (lazy singleton)
// ─────────────────────────────────────────────────────────────

var (
	ocrMu       sync.Mutex
	ocrEngine   ocr.Engine
	ocrInitErr  error
	ocrInitOnce sync.Once
)

type ocrModelPaths struct{ lib, det, rec, dict string }

func firstExistingPath(paths ...string) string {
	for _, p := range paths {
		if p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func resolveOCRPaths() (ocrModelPaths, error) {
	env := ocrModelPaths{
		lib:  os.Getenv("SSM_GO_OCR_LIB"),
		det:  os.Getenv("SSM_GO_OCR_DET"),
		rec:  os.Getenv("SSM_GO_OCR_REC"),
		dict: os.Getenv("SSM_GO_OCR_DICT"),
	}
	if env.lib != "" && env.det != "" && env.rec != "" && env.dict != "" {
		return env, nil
	}
	fallback := ocrModelPaths{
		lib:  firstExistingPath("./paddle_weights/onnxruntime.dll", "./paddle_weights/onnxruntime_maa.dll", "./onnxruntime.dll"),
		det:  firstExistingPath("./paddle_weights/det.onnx", "./det.onnx"),
		rec:  firstExistingPath("./paddle_weights/rec.onnx", "./rec.onnx"),
		dict: firstExistingPath("./paddle_weights/keys.txt", "./paddle_weights/dict.txt", "./dict.txt"),
	}
	if env.lib != "" {
		fallback.lib = env.lib
	}
	if env.det != "" {
		fallback.det = env.det
	}
	if env.rec != "" {
		fallback.rec = env.rec
	}
	if env.dict != "" {
		fallback.dict = env.dict
	}
	if fallback.lib == "" || fallback.det == "" || fallback.rec == "" || fallback.dict == "" {
		return ocrModelPaths{}, fmt.Errorf(
			"OCR models not found: need paddle_weights/{onnxruntime.dll,det.onnx,rec.onnx,keys.txt}")
	}
	return fallback, nil
}

func getOCREngine() (ocr.Engine, error) {
	ocrMu.Lock()
	defer ocrMu.Unlock()
	if ocrEngine != nil {
		return ocrEngine, nil
	}
	if ocrInitErr != nil {
		return nil, ocrInitErr
	}
	ocrInitOnce.Do(func() {
		paths, err := resolveOCRPaths()
		if err != nil {
			ocrInitErr = err
			return
		}
		start := time.Now()
		eng, err := ocr.NewPaddleOcrEngine(ocr.Config{
			OnnxRuntimeLibPath: paths.lib,
			DetModelPath:       paths.det,
			RecModelPath:       paths.rec,
			DictPath:           paths.dict,
			DetMaxSideLen:      480,
			NumThreads:         2,
		})
		if err != nil {
			ocrInitErr = fmt.Errorf("init go-ocr engine: %w", err)
			return
		}
		ocrEngine = eng
		log.Infof("[SONGOCR] engine ready in %dms (lib=%s det=%s rec=%s)",
			time.Since(start).Milliseconds(), filepath.Base(paths.lib), filepath.Base(paths.det), filepath.Base(paths.rec))
	})
	if ocrInitErr != nil {
		return nil, ocrInitErr
	}
	return ocrEngine, nil
}

// ocrImageTexts runs OCR on an in-memory image (no PNG round-trip).
func ocrImageTextsImage(img image.Image) ([]string, time.Duration, error) {
	eng, err := getOCREngine()
	if err != nil {
		return nil, 0, err
	}
	ocrMu.Lock()
	defer ocrMu.Unlock()
	start := time.Now()
	results, err := eng.RunOCR(img)
	if err != nil {
		return nil, 0, err
	}
	elapsed := time.Since(start)
	texts := make([]string, 0, len(results))
	for _, r := range results {
		t := strings.TrimSpace(r.Text)
		if t != "" {
			texts = append(texts, t)
		}
	}
	return texts, elapsed, nil
}

// ─────────────────────────────────────────────────────────────
// Song candidates
// ─────────────────────────────────────────────────────────────

// songCandidate aliases the shared matcher type so the song-DB loading code
// below reads exactly as before the matching logic moved to package
// songmatch (which has no cgo dependencies and is unit tested on its own).
type songCandidate = songmatch.SongCandidate

var (
	candMu    sync.Mutex
	candCache = map[string][]songCandidate{}
)

// loadLocalFirst prefers the on-disk cache so detection latency never waits
// on the network; the cache is refreshed in the background instead.
func loadLocalFirst(localPath, url string) ([]byte, error) {
	if data, err := os.ReadFile(localPath); err == nil && len(data) > 0 {
		go func() { _, _ = fetchOrLoad(localPath, url) }()
		return data, nil
	}
	return fetchOrLoad(localPath, url)
}

func loadSongCandidates(mode string) ([]songCandidate, error) {
	candMu.Lock()
	defer candMu.Unlock()
	if c, ok := candCache[mode]; ok {
		return c, nil
	}
	var out []songCandidate
	if strings.EqualFold(mode, "pjsk") {
		data, err := loadLocalFirst("./sekai_master_db_diff_musics.json",
			"https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musics.json")
		if err != nil {
			return nil, err
		}
		var songs []struct {
			ID            int    `json:"id"`
			Title         string `json:"title"`
			Pronunciation string `json:"pronunciation"`
		}
		if err := json.Unmarshal(data, &songs); err != nil {
			return nil, err
		}
		for _, s := range songs {
			titles := uniqueNonEmpty([]string{s.Title, s.Pronunciation})
			if s.ID > 0 && len(titles) > 0 {
				out = append(out, songCandidate{SongID: s.ID, Titles: titles})
			}
		}
	} else {
		data, err := loadLocalFirst("./all.5.json", "https://bestdori.com/api/songs/all.5.json")
		if err != nil {
			return nil, err
		}
		var songs map[string]struct {
			MusicTitle []string `json:"musicTitle"`
		}
		if err := json.Unmarshal(data, &songs); err != nil {
			return nil, err
		}
		for idStr, s := range songs {
			var id int
			if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
				continue
			}
			titles := uniqueNonEmpty(s.MusicTitle)
			if len(titles) > 0 {
				out = append(out, songCandidate{SongID: id, Titles: titles})
			}
		}
	}
	candCache[mode] = out
	return out, nil
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// ─────────────────────────────────────────────────────────────
// Fuzzy title matching (ported from MaestroMiner game/songdetect) now lives
// in package songmatch: it has no cgo dependencies and is unit tested there.
// gui only feeds it the OCR texts plus the song DB loaded above.
// ─────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────
// HTTP handler
// ─────────────────────────────────────────────────────────────

var defaultSongROI = map[string][4]float64{
	"bang": {0.28, 0.05, 0.45, 0.18},
	"pjsk": {0.28, 0.05, 0.45, 0.18},
}

func parseROIParam(s string) ([4]float64, bool) {
	var roi [4]float64
	n, err := fmt.Sscanf(s, "%f,%f,%f,%f", &roi[0], &roi[1], &roi[2], &roi[3])
	if err != nil || n != 4 {
		return [4]float64{}, false
	}
	for _, v := range roi {
		if v < 0 || v > 1 {
			return [4]float64{}, false
		}
	}
	if roi[2] <= 0 || roi[3] <= 0 {
		return [4]float64{}, false
	}
	return roi, true
}

func (s *Server) handleDetectSong(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode != "pjsk" {
		mode = "bang"
	}
	debug := q.Get("debug") == "1"
	threshold := 0
	fmt.Sscanf(q.Get("threshold"), "%d", &threshold)

	roi, roiOK := parseROIParam(q.Get("roi"))
	if roiOK {
		// Echo the clamped ROI so the response matches the crop actually used.
		roi[0] = clampf(roi[0], 0, 1)
		roi[1] = clampf(roi[1], 0, 1)
		roi[2] = clampf(roi[2], 0.01, 1-roi[0])
		roi[3] = clampf(roi[3], 0.01, 1-roi[1])
	}
	if !roiOK {
		// No roi in the request: use the saved calibration (or defaults).
		if cfgROI, ok := s.conf.SongDetectROI[mode]; ok {
			roi, roiOK = cfgROI, true
		} else {
			roi = defaultSongROI[mode]
		}
	}
	if q.Get("save") == "1" {
		if s.conf.SongDetectROI == nil {
			s.conf.SongDetectROI = map[string][4]float64{}
		}
		s.conf.SongDetectROI[mode] = roi
		_ = s.conf.Save()
	}

	// ── 1. source image ──
	t0 := time.Now()
	var srcImg *image.Gray
	var fullGray *image.Gray
	source := "test"
	transport := "test"
	screencapMs := 0.0
	hidReleased := false
	if testPath := q.Get("test"); testPath != "" {
		data, err := os.ReadFile(testPath)
		if err != nil {
			http.Error(w, "test image: "+err.Error(), http.StatusBadRequest)
			return
		}
		img, _, err := image.Decode(strings.NewReader(string(data)))
		if err != nil {
			http.Error(w, "decode test image: "+err.Error(), http.StatusBadRequest)
			return
		}
		srcImg = imageToGray(img)
		fullGray = srcImg
	} else {
		s.mu.Lock()
		ctrl := s.controller
		s.mu.Unlock()
		var frame controllers.ScrcpyFrame
		var frameOK bool
		if sc, ok := ctrl.(*controllers.ScrcpyController); ok {
			frame, frameOK = sc.LatestFrame()
			frameOK = frameOK && len(frame.Plane0) > 0
		}
		if frameOK {
			gray := &image.Gray{Pix: frame.Plane0, Stride: frame.Width, Rect: image.Rect(0, 0, frame.Width, frame.Height)}
			srcImg = cropGray(gray, roi)
			fullGray = gray
			source = "scrcpy"
		} else {
			// HID backend or not armed: take a one-shot adb screencap. The adb
			// server must be alive only for this call (it fights libusb for
			// the ADB interface HID needs), so it is stopped again right after.
			s.mu.Lock()
			st := s.state
			armed := s.controller
			s.mu.Unlock()
			if st == StatePlaying {
				http.Error(w, `{"error":"playback in progress; detect after it ends"}`, http.StatusConflict)
				return
			}
			if hid, isHID := armed.(*controllers.HIDController); isHID && hid != nil {
				// The open libusb handle holds the WinUSB interface, which
				// makes adb blind to the device. Release it for the capture.
				// This also covers the Ready state: in multiplayer the user
				// sits armed while matchmaking and needs re-detection — the
				// stale chart is discarded and re-armed via a fresh Load.
				_ = hid.Close()
				s.mu.Lock()
				s.controller = nil
				if s.state == StateDone || s.state == StateReady {
					if s.state == StateReady {
						select {
						case <-s.stopCh:
						default:
							close(s.stopCh)
						}
					}
					s.state = StateIdle
				}
				s.mu.Unlock()
				s.broadcastState()
				hidReleased = true
			}
			pngBytes, capDur, via, capErr := s.adbScreencapForDetect(q.Get("serial"))
			if capErr != nil {
				http.Error(w, `{"error":"`+capErr.Error()+`"}`, http.StatusConflict)
				return
			}
			screencapMs = capDur.Seconds() * 1000
			transport = via
			img, _, err := image.Decode(bytes.NewReader(pngBytes))
			if err != nil {
				http.Error(w, `{"error":"decode screencap: `+err.Error()+`"}`, http.StatusInternalServerError)
				return
			}
			fullGray = imageToGray(img)
			srcImg = cropGray(fullGray, roi)
			source = "screencap"
		}
	}
	if srcImg == nil {
		dfw, dfh := 0, 0
		if fullGray != nil {
			dfw, dfh = fullGray.Bounds().Dx(), fullGray.Bounds().Dy()
		}
		http.Error(w, fmt.Sprintf(`{"error":"empty crop: frame %dx%d, roi %v (config %v default %v) -> pw=%d ph=%d; adjust roi"}`,
			dfw, dfh, roi, s.conf.SongDetectROI, defaultSongROI[mode],
			int(clampf(roi[2], 0, 1)*float64(dfw)), int(clampf(roi[3], 0, 1)*float64(dfh))), http.StatusBadRequest)
		return
	}
	// frameMs covers decode+crop only; screencapMs is reported separately.
	frameMs := time.Since(t0).Seconds()*1000 - screencapMs

	// ── 2. OCR ──
	texts, ocrDur, err := ocrImageTextsImage(srcImg)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	ocrMs := ocrDur.Seconds() * 1000

	// ── 3. match ──
	t2 := time.Now()
	cands, err := loadSongCandidates(mode)
	if err != nil {
		http.Error(w, `{"error":"load song db: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	best, top, confident := songmatch.Detect(texts, cands, threshold)
	matchMs := time.Since(t2).Seconds() * 1000

	resp := map[string]interface{}{
		"ok":          true,
		"mode":        mode,
		"source":      source,
		"via":         transport,
		"hidReleased": hidReleased,
		"matched":     confident,
		"songId":      best.SongID,
		"title":       best.Title,
		"score":       best.Score,
		"candidates":  top,
		"roi":         roi,
		"timings": map[string]float64{
			"screencapMs": screencapMs,
			"frameMs":     frameMs,
			"ocrMs":       ocrMs,
			"matchMs":     matchMs,
			"totalMs":     screencapMs + frameMs + ocrMs + matchMs,
		},
	}
	if debug {
		resp["texts"] = texts
	}
	// Blank-crop heuristic: a nearly uniform crop means the device screen is
	// probably off, locked, or not on the game screen.
	if srcImg != nil {
		minV, maxV := 255, 0
		for _, v := range srcImg.Pix {
			if int(v) < minV {
				minV = int(v)
			}
			if int(v) > maxV {
				maxV = int(v)
			}
		}
		if maxV-minV < 12 {
			resp["blank"] = true
		}
	}
	if debug && q.Get("render") != "" && fullGray != nil {
		if q.Get("render") == "plain" {
			resp["frameJpeg"] = grayToJpegBase64(fullGray)
		} else {
			resp["frameJpeg"] = jpegWithROI(fullGray, roi)
		}
	}
	if debug && q.Get("cropImg") != "0" {
		resp["cropPng"] = grayToBase64(srcImg)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// adbCaptureMu serializes the whole start-capture-kill adb cycle: overlapping
// detect requests used to kill the adb server under each other's screencap
// ("wsarecv: connection forcibly closed").
var adbCaptureMu sync.Mutex

// adbScreencapForDetect grabs a one-shot screenshot for the given (or
// auto-selected) device. When wireless debugging is configured it captures
// over WiFi first (never touching the USB interface HID owns) and falls back
// to the USB path on any failure. Either way the adb server is started for
// the capture and stopped afterwards so it cannot linger on USB.
func (s *Server) adbScreencapForDetect(serial string) ([]byte, time.Duration, string, error) {
	adbCaptureMu.Lock()
	defer adbCaptureMu.Unlock()
	start := time.Now()
	// Remember whether this capture started the server; a server that was
	// already running belongs to a live session and must not be stopped.
	startedByUs := !adb.IsADBServerRunning("localhost", 5037)
	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		return nil, 0, "usb", fmt.Errorf("start adb server: %w", err)
	}
	client := adb.NewDefaultClient()

	// A cold adb server takes ~2s to enumerate USB devices, and a server can
	// also die mid-poll (killed by a previous capture's cleanup) — so every
	// iteration re-checks that a server is alive before asking for devices.
	var device *adb.Device
	var lastDevices []*adb.Device
	deadline := time.Now().Add(8 * time.Second)
	for {
		if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
			return nil, 0, "usb", fmt.Errorf("start adb server: %w", err)
		}
		if devices, err := client.Devices(); err == nil {
			lastDevices = devices
			device = pickDetectDevice(devices, serial, s.conf.Snapshot())
		}
		if device != nil {
			break
		}
		if time.Now().After(deadline) {
			seen := make([]string, 0, len(lastDevices))
			for _, d := range lastDevices {
				state, _ := d.State()
				seen = append(seen, fmt.Sprintf("%s(%s)", d.Serial(), state))
			}
			s.stopAdbServerIfSafe(startedByUs)
			if len(seen) == 0 {
				return nil, 0, "usb", fmt.Errorf("no adb device found after waiting; is the device connected with USB debugging on?")
			}
			return nil, 0, "usb", fmt.Errorf("no usable adb device (seen: %s); only devices added in Settings are used — run Auto Detect to add one", strings.Join(seen, ", "))
		}
		time.Sleep(300 * time.Millisecond)
	}

	pngBytes, err := device.RawSh("screencap", "-p")
	if err != nil {
		s.stopAdbServerIfSafe(startedByUs)
		return nil, 0, "usb", fmt.Errorf("screencap: %w", err)
	}
	// Restore HID safety when safe: the server must not keep holding the ADB
	// interface unless a live adb session still needs it.
	s.stopAdbServerIfSafe(startedByUs)
	return pngBytes, time.Since(start), "usb", nil
}

// stopAdbServerIfSafe stops the shared adb server only when this capture
// started it and nothing else depends on it: no loaded/playing scrcpy session
// and no in-flight controller open. The old unconditional stop could tear
// down an already-armed session, after which "Start" appeared to do nothing.
func (s *Server) stopAdbServerIfSafe(startedByUs bool) {
	if !startedByUs || s.adbBusy.Load() {
		return
	}
	s.mu.Lock()
	st := s.state
	ctrl := s.controller
	s.mu.Unlock()
	if st != StateIdle {
		return
	}
	if _, isScrcpy := ctrl.(*controllers.ScrcpyController); isScrcpy {
		return
	}
	_ = adb.StopADBServer("localhost", 5037)
}

// pickDetectDevice: an explicit serial is used when it is connected and
// authorized; otherwise selection is limited to connected, authorized
// devices registered in Device Management — unregistered devices are ignored
// on purpose, so add them via Auto Detect or Settings first.
func pickDetectDevice(devices []*adb.Device, serial string, configured map[string]config.DeviceConfig) *adb.Device {
	serial = strings.TrimSpace(serial)
	if serial != "" {
		for _, d := range devices {
			if d.Serial() == serial && d.Authorized() {
				return d
			}
		}
		return nil
	}
	recorded := make(map[string]struct{}, len(configured))
	for s := range configured {
		recorded[s] = struct{}{}
	}
	device, _, err := adb.PickDevice(devices, "", recorded)
	if err != nil {
		return nil
	}
	return device
}

// imageToGray converts any decoded image to grayscale (stdlib only).
func imageToGray(img image.Image) *image.Gray {
	b := img.Bounds()
	gray := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			gray.Pix[y*gray.Stride+x] = uint8((299*r + 587*g + 114*bl) / 1000 >> 8)
		}
	}
	return gray
}

func grayToBase64(img *image.Gray) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// grayToJpegBase64 encodes a grayscale view as JPEG.
func grayToJpegBase64(src *image.Gray) string {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// jpegWithROI renders a grayscale view as JPEG with the normalized ROI drawn
// as a red rectangle, for visual calibration in the debug panel.
func jpegWithROI(src *image.Gray, roi [4]float64) string {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w]
		for x := 0; x < w; x++ {
			o := out.Pix[y*out.Stride+x*4:]
			g := row[x]
			o[0], o[1], o[2], o[3] = g, g, g, 0xff
		}
	}
	x0 := int(clampf(roi[0], 0, 1) * float64(w))
	y0 := int(clampf(roi[1], 0, 1) * float64(h))
	x1 := x0 + int(clampf(roi[2], 0, 1)*float64(w))
	y1 := y0 + int(clampf(roi[3], 0, 1)*float64(h))
	drawLine := func(xa, ya, xb, yb int) {
		xa = max(min(xa, w-1), 0)
		xb = max(min(xb, w-1), 0)
		ya = max(min(ya, h-1), 0)
		yb = max(min(yb, h-1), 0)
		if xa == xb {
			for y := min(ya, yb); y <= max(ya, yb); y++ {
				o := out.Pix[y*out.Stride+xa*4:]
				o[0], o[1], o[2] = 0xff, 0x30, 0x30
			}
		} else {
			for x := min(xa, xb); x <= max(xa, xb); x++ {
				o := out.Pix[ya*out.Stride+x*4:]
				o[0], o[1], o[2] = 0xff, 0x30, 0x30
			}
		}
	}
	drawLine(x0, y0, x1, y0)
	drawLine(x0, y1-1, x1, y1-1)
	drawLine(x0, y0, x0, y1)
	drawLine(x1-1, y0, x1-1, y1)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func cropGray(src *image.Gray, roi [4]float64) *image.Gray {
	b := src.Bounds()
	fw, fh := b.Dx(), b.Dy()
	x0 := int(clampf(roi[0], 0, 1) * float64(fw))
	y0 := int(clampf(roi[1], 0, 1) * float64(fh))
	pw := int(clampf(roi[2], 0, 1) * float64(fw))
	ph := int(clampf(roi[3], 0, 1) * float64(fh))
	if x0+pw > fw {
		pw = fw - x0
	}
	if y0+ph > fh {
		ph = fh - y0
	}
	if pw < 8 || ph < 8 {
		return nil
	}
	out := image.NewGray(image.Rect(0, 0, pw, ph))
	for y := 0; y < ph; y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+pw], src.Pix[(y0+y)*src.Stride+x0:(y0+y)*src.Stride+x0+pw])
	}
	return out
}
