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
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ocr "github.com/getcharzp/go-ocr"
	xdraw "golang.org/x/image/draw"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/db"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/songmatch"
)

// ─────────────────────────────────────────────────────────────
// Song OCR engine (lazy singleton)
// ─────────────────────────────────────────────────────────────

// detMaxSideLen is the long-side budget handed to the OCR engine's detector.
// Keep it in sync with the Config passed to NewPaddleOcrEngine below: the
// detector resizes the long side to exactly this, and detInputSize() has to
// predict that resize.
const detMaxSideLen = 960

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
			DetMaxSideLen:      detMaxSideLen,
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
	sortReadingOrder(results)
	texts := make([]string, 0, len(results))
	for _, r := range results {
		t := strings.TrimSpace(r.Text)
		if t != "" {
			texts = append(texts, t)
		}
	}
	return texts, elapsed, nil
}

// sortReadingOrder orders OCR results the way a human reads them: top to
// bottom, then left to right. The engine returns the boxes in heatmap-discovery
// order, not geometric order, and songmatch.Rank scores their concatenation —
// which only helps when they are in reading order. #100039「Choir ‘S’ Choir」
// came back as ["s","Choir","Choir"], and the join has to read「choirschoir」
// to equal the title.
func sortReadingOrder(results []ocr.RecResult) {
	if len(results) < 2 {
		return
	}
	heights := make([]int, 0, len(results))
	for _, r := range results {
		heights = append(heights, r.Box[3]-r.Box[1])
	}
	sort.Ints(heights)
	// Boxes whose vertical centres are within half a line height belong to the
	// same line and are ordered by x instead.
	tolerance := heights[len(heights)/2] / 2
	if tolerance < 1 {
		tolerance = 1
	}
	midY := func(b [4]int) int { return (b[1] + b[3]) / 2 }
	sort.SliceStable(results, func(i, j int) bool {
		yi, yj := midY(results[i].Box), midY(results[j].Box)
		if yi-yj > tolerance || yj-yi > tolerance {
			return yi < yj
		}
		return results[i].Box[0] < results[j].Box[0]
	})
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
// on the network; the cache is refreshed in the background instead. The
// fetching side is upstream's cachedSource, so the 30s timeout, the JSON
// validation and the atomic cache write are shared with /api/songdb.
func loadLocalFirst(localPath, url string) ([]byte, error) {
	src := cachedSource{localPath, url}
	if data, err := os.ReadFile(localPath); err == nil && len(data) > 0 {
		go func() { _, _ = src.load() }()
		return data, nil
	}
	return src.load()
}

func loadSongCandidates(mode string) ([]songCandidate, error) {
	mode = common.NormalizeMode(mode)

	candMu.Lock()
	defer candMu.Unlock()
	if c, ok := candCache[mode]; ok {
		return c, nil
	}
	var out []songCandidate
	switch mode {
	case common.ModeOurNotes:
		notesDB, err := db.NewOurNotesDB()
		if err != nil {
			return nil, err
		}
		for _, s := range notesDB.Songs() {
			// Every localisation: the pre-live screen draws the Japanese
			// title while songs.json's `title` is the simplified-Chinese one.
			titles := s.SearchTitles()
			if s.ID > 0 && len(titles) > 0 {
				out = append(out, songCandidate{SongID: s.ID, Titles: titles})
			}
		}
	case common.ModePjsk:
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
	default: // common.ModeBang
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
	// Our Notes draws the song title as a strip in the bottom-left corner of
	// the pre-live screen ("乐队确认"), not at the top centre like the other two
	// games. Measured from a real 2608x1200 frame; recalibrate via Detect Song.
	"ournotes": {0.15, 0.81, 0.36, 0.08},
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

// chooseSongROI resolves the crop to use, in priority order: an explicit ROI
// from the request (the debug panel's sliders while they are being dragged),
// this device's saved calibration, the shared mode-level calibration kept for
// configs predating per-device storage, and finally the built-in default.
// reqROI must already have been clamped by the caller.
func chooseSongROI(reqROI [4]float64, reqOK bool,
	devROI [4]float64, devOK bool,
	modeROI [4]float64, modeOK bool,
	def [4]float64) [4]float64 {
	switch {
	case reqOK:
		return reqROI
	case devOK:
		return devROI
	case modeOK:
		return modeROI
	default:
		return def
	}
}

func (s *Server) handleDetectSong(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := common.NormalizeMode(q.Get("mode"))
	debug := q.Get("debug") == "1"
	threshold := 0
	fmt.Sscanf(q.Get("threshold"), "%d", &threshold)

	// The song DB is loaded up front on purpose: its cold start hits the
	// network, and the device session opened below holds adbCaptureMu for as
	// long as this request lives. Failing here also avoids arming an adb
	// session only to return 502.
	cands, err := loadSongCandidates(mode)
	if err != nil {
		http.Error(w, `{"error":"load song db: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	// ── 0. pick the frame source, and with it the device this run belongs to ──
	// The ROI is calibrated per device, so the crop cannot be chosen until the
	// device is known. That is why the capture is no longer a single black box
	// that both picks the device and crops: the source is resolved once, here,
	// and reports the serial it will capture from.
	type capturePlan struct {
		source      string // "test" | "scrcpy" | "screencap"
		sc          *controllers.ScrcpyController
		sess        *detectDeviceSession
		serial      string
		hidReleased bool
	}
	var plan capturePlan

	if testPath := q.Get("test"); testPath != "" {
		// A local test image has no device: the ROI falls back to the shared
		// mode-level bucket and a save lands there too. This path deliberately
		// keeps working exactly as before.
		plan.source = "test"
	} else {
		s.mu.Lock()
		ctrl := s.song.controller
		st := s.status.State
		s.mu.Unlock()

		// A live scrcpy session already knows its device, so no adb round trip
		// is needed to key the ROI. Prefer it when it has a frame to give.
		if sc, ok := ctrl.(*controllers.ScrcpyController); ok && sc != nil {
			if frame, ok := sc.LatestFrame(); ok && len(frame.Plane0) > 0 {
				plan.source = "scrcpy"
				plan.sc = sc
				plan.serial = sc.DeviceSerial()
			}
		}

		if plan.source == "" {
			// HID backend, not armed, or a scrcpy session with no frame yet:
			// take a one-shot adb screencap. Playing is the one state where
			// touching adb is off limits.
			if st == StatePlaying {
				http.Error(w, `{"error":"playback in progress; detect after it ends"}`, http.StatusConflict)
				return
			}
			// The open libusb handle holds the WinUSB interface, which makes adb
			// blind to the device, so release it before opening the session.
			// This also covers the Ready state: in multiplayer the user sits
			// armed while matchmaking and needs re-detection — the stale chart
			// is discarded and re-armed via a fresh Load.
			plan.hidReleased = s.releaseHIDForDetect(ctrl)

			sess, openErr := s.openDetectDeviceSession(q.Get("serial"))
			if openErr != nil {
				http.Error(w, `{"error":"`+openErr.Error()+`"}`, http.StatusConflict)
				return
			}
			defer sess.Close()
			plan.source = "screencap"
			plan.sess = sess
			plan.serial = sess.Serial()
		}
	}

	// ── 1. ROI: request param, then this device, then the shared mode bucket ──
	roi, roiOK := parseROIParam(q.Get("roi"))
	if roiOK {
		// Echo the clamped ROI so the response matches the crop actually used.
		roi[0] = clampf(roi[0], 0, 1)
		roi[1] = clampf(roi[1], 0, 1)
		roi[2] = clampf(roi[2], 0.01, 1-roi[0])
		roi[3] = clampf(roi[3], 0.01, 1-roi[1])
	}
	var devROI, modeROI [4]float64
	var devOK, modeOK bool
	if plan.serial != "" {
		devROI, devOK = s.conf.SongDetectROIFor(plan.serial, mode)
	}
	modeROI, modeOK = s.conf.SongDetectROIMode(mode)
	roi = chooseSongROI(roi, roiOK, devROI, devOK, modeROI, modeOK, defaultSongROI[mode])

	if q.Get("save") == "1" {
		// Bucketed by the device that was actually captured, never by whatever
		// the caller claimed. An empty serial (test image, nothing pickable)
		// lands in the shared bucket instead of nowhere.
		_ = s.conf.SetSongDetectROI(plan.serial, mode, roi)
	}

	// ── 2. source image ──
	// A one-shot capture can land while the game is still drawing the title bar
	// — the strip wipes in from the left, and the frame that produced
	// "OCR 文本: [Mas?u]" was taken ~20% through「Mas?uerade Rhapsody Re?uest」
	// (cropping the real strip to 16% reproduces exactly that reading). Nothing
	// is wrong with the OCR there, and no matcher can recover a title that was
	// never on screen, so when the capture is unusable we take another one.
	type captureOut struct {
		crop        *image.Gray
		full        *image.Gray
		source      string
		transport   string
		screencapMs float64
		hidReleased bool
	}
	capture := func() (captureOut, int, string) {
		// The source was decided in step 0 and never changes mid-request, so
		// the crop below always uses the ROI of the device this run captured.
		out := captureOut{source: plan.source, hidReleased: plan.hidReleased}
		switch plan.source {
		case "test":
			out.transport = "test"
			data, readErr := os.ReadFile(q.Get("test"))
			if readErr != nil {
				return out, http.StatusBadRequest, "test image: " + readErr.Error()
			}
			img, _, decErr := image.Decode(strings.NewReader(string(data)))
			if decErr != nil {
				return out, http.StatusBadRequest, "decode test image: " + decErr.Error()
			}
			out.crop = imageToGray(img)
			out.full = out.crop
			return out, 0, ""

		case "scrcpy":
			frame, frameOK := plan.sc.LatestFrame()
			if !frameOK || len(frame.Plane0) == 0 {
				// The session can die between the plan and this capture; the
				// old code silently fell through to adb here, which would have
				// cropped with a different device's ROI.
				return out, http.StatusConflict, `{"error":"scrcpy frame unavailable"}`
			}
			gray := &image.Gray{Pix: frame.Plane0, Stride: frame.Width, Rect: image.Rect(0, 0, frame.Width, frame.Height)}
			out.crop = cropGray(gray, roi)
			out.full = gray
			return out, 0, ""
		}

		// One-shot adb screencap through the session opened in step 0. The adb
		// server stays alive for the whole request (it fights libusb for the ADB
		// interface HID needs) and is stopped again by sess.Close().
		out.transport = "usb"
		pngBytes, capDur, capErr := plan.sess.Screencap()
		if capErr != nil {
			return out, http.StatusConflict, `{"error":"` + capErr.Error() + `"}`
		}
		out.screencapMs = capDur.Seconds() * 1000
		img, _, decErr := image.Decode(bytes.NewReader(pngBytes))
		if decErr != nil {
			return out, http.StatusInternalServerError, `{"error":"decode screencap: ` + decErr.Error() + `"}`
		}
		out.full = imageToGray(img)
		out.crop = cropGray(out.full, roi)
		return out, 0, ""
	}

	const detectAttempts = 2
	var (
		out         captureOut
		texts       []string
		best        songmatch.Match
		top         []songmatch.Match
		confident   bool
		blank       bool
		attempts    int
		dur         time.Duration
		cropW       int
		cropH       int
		ocrW        int
		ocrH        int
		screencapMs float64
		frameMs     float64
		ocrMs       float64
		matchMs     float64
	)
	for attempts = 1; attempts <= detectAttempts; attempts++ {
		t0 := time.Now()
		var status int
		var msg string
		out, status, msg = capture()
		if status != 0 {
			http.Error(w, msg, status)
			return
		}
		if out.crop == nil {
			dfw, dfh := 0, 0
			if out.full != nil {
				dfw, dfh = out.full.Bounds().Dx(), out.full.Bounds().Dy()
			}
			http.Error(w, fmt.Sprintf(`{"error":"empty crop: frame %dx%d, roi %v (serial %q device %v shared %v default %v) -> pw=%d ph=%d; adjust roi"}`,
				dfw, dfh, roi, plan.serial, devROI, modeROI, defaultSongROI[mode],
				int(clampf(roi[2], 0, 1)*float64(dfw)), int(clampf(roi[3], 0, 1)*float64(dfh))), http.StatusBadRequest)
			return
		}
		// frameMs covers decode+crop only; screencapMs is reported separately.
		screencapMs += out.screencapMs
		frameMs += time.Since(t0).Seconds()*1000 - out.screencapMs

		// ── 2. OCR ──
		// Feed the engine an image it will not resize any further: its detector
		// floors the short side to a multiple of 32, which mangles thin strips
		// (see detInputSize). out.crop stays the raw crop for the debug preview
		// and the blank-frame heuristic.
		// These are declared outside the loop on purpose: with `:=` here they
		// would shadow the outer result variables and the response would report
		// an empty text list no matter what the OCR actually read.
		cropW, cropH = out.crop.Bounds().Dx(), out.crop.Bounds().Dy()
		ocrW, ocrH = detInputSize(cropW, cropH)
		texts, dur, err = ocrImageTextsImage(resampleGray(out.crop, ocrW, ocrH))
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		ocrMs += dur.Seconds() * 1000

		// ── 3. match ──
		t2 := time.Now()
		best, top, confident = songmatch.Detect(texts, cands, threshold)
		matchMs += time.Since(t2).Seconds() * 1000

		blank = cropIsBlank(out.crop)
		// Nothing scored at all: retry once unless the crop is a flat colour
		// (screen off / locked), where re-capturing cannot help.
		if len(top) > 0 || blank || attempts == detectAttempts {
			break
		}
		time.Sleep(350 * time.Millisecond)
	}

	resp := map[string]interface{}{
		"ok":          true,
		"mode":        mode,
		"source":      out.source,
		"via":         out.transport,
		"hidReleased": out.hidReleased,
		"attempts":    attempts,
		"matched":     confident,
		"songId":      best.SongID,
		"title":       best.Title,
		"score":       best.Score,
		"candidates":  top,
		"roi":         roi,
		// Sizes make a "no match" report actionable: a crop of the wrong shape
		// means the ROI is off, while a sane crop with empty texts means the OCR
		// found nothing and the library is suspect.
		"cropSize": fmt.Sprintf("%dx%d", cropW, cropH),
		"ocrSize":  fmt.Sprintf("%dx%d", ocrW, ocrH),
		// The device whose calibration this crop belongs to, so the debug panel
		// can show it and reload the sliders when the user swaps devices.
		"serial": plan.serial,
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
	if blank {
		resp["blank"] = true
	}
	if debug && q.Get("render") != "" && out.full != nil {
		if q.Get("render") == "plain" {
			resp["frameJpeg"] = grayToJpegBase64(out.full)
		} else {
			resp["frameJpeg"] = jpegWithROI(out.full, roi)
		}
	}
	if debug && q.Get("cropImg") != "0" {
		resp["cropPng"] = grayToBase64(out.crop)
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

// adbCaptureMu serializes the whole detect-capture cycle: overlapping detect
// requests used to kill the adb server under each other's screencap
// ("wsarecv: connection forcibly closed"). A detectDeviceSession holds it from
// open until Close, so it now spans one full request rather than one capture.
var adbCaptureMu sync.Mutex

// detectDeviceSession owns the shared adb server for the lifetime of one
// detect request. It is opened before the ROI is resolved — the crop is
// calibrated per device, so the device has to be known first — and the server
// is stopped again in Close so it cannot linger on the USB interface HID needs.
//
// Holding adbCaptureMu for the whole request is deliberate: releasing it right
// after the pick would let a second detect request open its own session with
// startedByUs=false, and this session's Close would then stop the very server
// the other request is still using.
type detectDeviceSession struct {
	s           *Server
	device      *adb.Device
	startedByUs bool
	closed      bool
}

// openDetectDeviceSession starts the adb server and polls for a usable device
// (an explicitly requested serial when it is connected and authorized,
// otherwise a registered + authorized one). On error adbCaptureMu has already
// been released; every successful open must be paired with a Close.
func (s *Server) openDetectDeviceSession(serial string) (*detectDeviceSession, error) {
	adbCaptureMu.Lock()
	// Remember whether this session started the server; a server that was
	// already running belongs to a live session and must not be stopped.
	startedByUs := !adb.IsADBServerRunning("localhost", 5037)
	// Every failure has to stop the server if we started it and release the
	// lock, so route them all through here.
	fail := func(err error) (*detectDeviceSession, error) {
		s.stopAdbServerIfSafe(startedByUs)
		adbCaptureMu.Unlock()
		return nil, err
	}

	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		return fail(fmt.Errorf("start adb server: %w", err))
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
			return fail(fmt.Errorf("start adb server: %w", err))
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
			if len(seen) == 0 {
				return fail(fmt.Errorf("no adb device found after waiting; is the device connected with USB debugging on?"))
			}
			return fail(fmt.Errorf("no usable adb device (seen: %s); only devices added in Settings are used — run Auto Detect to add one", strings.Join(seen, ", ")))
		}
		time.Sleep(300 * time.Millisecond)
	}

	return &detectDeviceSession{s: s, device: device, startedByUs: startedByUs}, nil
}

// Serial reports the device this session captures from, or "" when none was
// resolved. This is the key a ROI calibration is stored under.
func (d *detectDeviceSession) Serial() string {
	if d == nil || d.device == nil {
		return ""
	}
	return d.device.Serial()
}

// Screencap takes one frame from the session's device. Only the RawSh itself is
// timed: the adb server is already up, so this is no longer the "whole
// start-capture-kill cycle" the old one-shot helper used to report.
func (d *detectDeviceSession) Screencap() ([]byte, time.Duration, error) {
	if d == nil || d.device == nil {
		return nil, 0, fmt.Errorf("no device in session")
	}
	start := time.Now()
	pngBytes, err := d.device.RawSh("screencap", "-p")
	if err != nil {
		return nil, 0, fmt.Errorf("screencap: %w", err)
	}
	return pngBytes, time.Since(start), nil
}

// Close stops the adb server when safe and releases adbCaptureMu. It is
// idempotent, so a deferred call plus an explicit one cannot unlock twice.
func (d *detectDeviceSession) Close() {
	if d == nil || d.closed {
		return
	}
	d.closed = true
	d.s.stopAdbServerIfSafe(d.startedByUs)
	adbCaptureMu.Unlock()
}

// releaseHIDForDetect closes an armed HID controller so adb can see the device,
// resets the server to Idle and broadcasts the change. Returns true when a
// controller was actually released. It must run before the adb session is
// opened: the libusb handle holds the WinUSB interface, which makes adb blind
// to the device. This also covers the Ready state — in multiplayer the user
// sits armed while matchmaking and needs re-detection, so the stale chart is
// discarded and re-armed via a fresh Load.
func (s *Server) releaseHIDForDetect(ctrl controllers.Controller) bool {
	hid := hidControllerOf(ctrl)
	if hid == nil {
		return false
	}
	_ = hid.Close()
	s.mu.Lock()
	s.song.controller = nil
	if s.status.State == StateDone || s.status.State == StateReady {
		if s.status.State == StateReady {
			s.song.interrupt()
		}
		s.status.State = StateIdle
	}
	s.publishLocked()
	s.mu.Unlock()
	return true
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
	st := s.status.State
	ctrl := s.song.controller
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

// cropIsBlank reports whether a crop is a flat colour, which means the screen is
// off, locked, or not showing the game — re-capturing cannot help there.
func cropIsBlank(img *image.Gray) bool {
	if img == nil {
		return true
	}
	minV, maxV := 255, 0
	for _, v := range img.Pix {
		if int(v) < minV {
			minV = int(v)
		}
		if int(v) > maxV {
			maxV = int(v)
		}
	}
	return maxV-minV < 12
}

// cropGray returns the normalized ROI of a grayscale frame.
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

// detInputSize returns the size to resample an OCR crop to so that the OCR
// engine's detector preprocessing becomes an identity transform.
//
// go-ocr scales the long side to detMaxSideLen and then floors *both* sides to a
// multiple of 32 (engine_paddle.go, preprocessDetImage). On a wide, thin title
// strip that floor is destructive: the 938x96 crop of an Our Notes title was
// resized to 480x32 — 35% of the rows thrown away, glyphs squashed 1.5x
// vertically — and the detector then returned zero boxes, which surfaced in the
// UI as "未匹配到歌曲, OCR 文本: []". Handing the engine an image that is already
// the exact size it would resize to keeps the glyph aspect ratio and restores
// detection (verified: the same crop is read correctly at 960x96).
func detInputSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	if h > w {
		return quantize32(float64(w) * float64(detMaxSideLen) / float64(h)), detMaxSideLen
	}
	return detMaxSideLen, quantize32(float64(h) * float64(detMaxSideLen) / float64(w))
}

// quantize32 snaps v to the nearest multiple of 32, rounding up when rounding
// down would cost more than 5% of the resolution: losing more than that squashes
// the glyphs, which is exactly what breaks detection.
func quantize32(v float64) int {
	k := int(math.Round(v / 32))
	if k < 1 {
		k = 1
	}
	if float64(32*k) < 0.95*v {
		k++
	}
	return 32 * k
}

// resampleGray scales a grayscale crop with the same kernel the OCR engine uses
// internally, returning the source untouched when no scaling is needed.
func resampleGray(src *image.Gray, w, h int) *image.Gray {
	if src == nil || w <= 0 || h <= 0 {
		return src
	}
	if src.Bounds().Dx() == w && src.Bounds().Dy() == h {
		return src
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}
