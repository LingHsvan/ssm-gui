// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/db"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/scores"
)

// player is the GUI's gui.Backend: it loads the requested song, opens the
// device and plays the song when the page presses Start. One song at a time.
type player struct {
	conf *config.Config
	srv  *gui.Server

	mu      sync.Mutex
	current *playRun
}

// playRun is one song being loaded or played.
type playRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// runStopTimeout bounds how long a new request waits for the previous run to
// release the device (scrcpy.Close included). A wedged session must not block
// the queue forever.
const runStopTimeout = 45 * time.Second

// Run stops the song in progress, waits until it has released the device and
// starts loading req.
func (p *player) Run(req gui.RunRequest) {
	p.mu.Lock()
	// Requests can arrive concurrently (Load, Restart and the automatic
	// re-arm), so re-check after waiting: if another request started a song
	// meanwhile, stop that one too. The last request wins and songs never
	// overlap.
	for p.current != nil {
		prev := p.current
		prev.cancel()
		p.mu.Unlock()
		select {
		case <-prev.done:
		case <-time.After(runStopTimeout):
			log.Infof("[GUI] previous run did not stop within %v; starting the new request anyway", runStopTimeout)
		}
		p.mu.Lock()
		if p.current == prev {
			p.current = nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &playRun{cancel: cancel, done: make(chan struct{})}
	p.current = run
	p.mu.Unlock()

	go func() {
		defer close(run.done)
		defer cancel()
		p.play(ctx, req)
	}()
}

func (p *player) Extract(path string) error {
	_, err := Extract(path, extractAssetFilter)
	return err
}

// play loads req, arms it and plays it once Start is pressed. Failures are
// shown in the page.
func (p *player) play(ctx context.Context, req gui.RunRequest) {
	// log.Fatal/Die panic with log.FatalErr; show it instead of exiting.
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(log.FatalErr); !ok {
				panic(r)
			}
			p.srv.SetError("Playback aborted by a fatal error. See the console log for details.")
		}
	}()

	ctrl, events, err := p.prepare(req)
	if err != nil {
		p.srv.SetError(err.Error())
		return
	}
	defer ctrl.Close()

	p.srv.SetReady(ctrl, events, nowPlaying(req))
	if !p.srv.WaitForStart(ctx) {
		return
	}
	start := time.Now().Add(-time.Duration(events[0].Timestamp) * time.Millisecond)
	p.srv.Autoplay(ctx, start)
	time.Sleep(300 * time.Millisecond) // let the last touches reach the device
}

// prepare turns req into touch events and opens the device to play them on.
func (p *player) prepare(req gui.RunRequest) (playController, []common.ViscousEventItem, error) {
	mode := common.NormalizeMode(req.Mode)
	chart, err := loadChart(req, mode)
	if err != nil {
		return nil, nil, err
	}

	genConfig, humanize := touchConfig(req, mode)
	raw, greatApplied := scores.GenerateHumanizedTouchEvent(genConfig, humanize, chart)
	p.srv.SetGreatStats(req.GreatCount, int64(greatApplied))

	// Auto Detect may stop the adb server when it finishes; flag the server as
	// busy so an in-flight open (forward / push / shell) is not cut off
	// mid-transfer, then always release the flag.
	p.srv.MarkAdbBusy(true)
	ctrl, events, err := openController(p.conf, req, mode, raw)
	p.srv.MarkAdbBusy(false)
	if err != nil {
		return nil, nil, err
	}
	if len(events) == 0 {
		ctrl.Close()
		return nil, nil, errors.New("no playable events were generated for this chart")
	}
	return ctrl, events, nil
}

// loadChart reads and parses the chart for req: the custom chart file if one
// is given, otherwise the extracted chart of the song and difficulty.
func loadChart(req gui.RunRequest, mode string) (scores.Chart, error) {
	path := req.ChartPath
	if path == "" {
		matches, err := findMusicscorePath(mode, req.SongID, req.Diff)
		if err != nil || len(matches) == 0 {
			return nil, errors.New("musicscore not found: extract the assets first or use a custom chart path")
		}
		path = matches[0]
	}

	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read musicscore: %w", err)
	}

	switch mode {
	case common.ModePjsk:
		chart, err := scores.ParseSUS(string(text))
		if err != nil {
			return nil, fmt.Errorf("failed to parse SUS: %w", err)
		}
		return chart, nil
	case common.ModeOurNotes:
		// Our Notes charts are gzip-compressed JSON; the parser also accepts
		// an already-decoded file.
		chart, err := scores.ParseOurNotes(text)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Our Notes chart: %w", err)
		}
		return chart, nil
	default:
		return scores.ParseBMS(string(text)), nil
	}
}

// findMusicscorePath returns the chart files matching a song id and difficulty
// under the extracted assets directory for the given game mode. Shared by the
// GUI and the CLI.
func findMusicscorePath(mode string, songID int, difficulty string) ([]string, error) {
	switch mode {
	case common.ModePjsk:
		return filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
			fmt.Sprintf("%04d_01/%s.txt", songID, difficulty)))
	case common.ModeOurNotes:
		d, err := db.NewOurNotesDB()
		if err != nil {
			return nil, err
		}
		path, ok := d.ChartPath(songID, difficulty)
		if !ok {
			return nil, nil
		}
		return []string{path}, nil
	default:
		return filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
			fmt.Sprintf("musicscore*/%03d/*_%s.txt", songID, difficulty)))
	}
}

// touchConfig returns the touch generation and humanization settings for req.
// Generation parameters left at 0 keep the game mode's default.
func touchConfig(req gui.RunRequest, mode string) (*scores.VTEGenerateConfig, scores.HumanizeConfig) {
	c := newDefaultVTEConfig(mode)
	setIfPositive(&c.TapDuration, req.TapDuration)
	setIfPositive(&c.FlickDuration, req.FlickDuration)
	setIfPositive(&c.FlickReportInterval, req.FlickReportInterval)
	setIfPositive(&c.SlideReportInterval, req.SlideReportInterval)
	setIfPositive(&c.FlickFactor, req.FlickFactor)
	setIfPositive(&c.FlickPow, req.FlickPow)
	// Unlike the sliders above, 0 is a meaningful value here (no lead), so
	// this one is a pointer: nil keeps the mode's default.
	if req.FlickLeadMs != nil {
		c.FlickLeadMs = max(*req.FlickLeadMs, 0)
	}

	greatOffset := req.GreatOffsetMs
	if greatOffset < 0 {
		greatOffset = -greatOffset
	}
	if greatOffset == 0 {
		greatOffset = 10
	}
	return c, scores.HumanizeConfig{
		TimingJitter:     req.TimingJitter,
		PositionJitter:   req.PositionJitter,
		TapDurJitter:     req.TapDurJitter,
		GreatOffsetMs:    greatOffset,
		GreatTargetCount: max(req.GreatCount, 0),
	}
}

// newDefaultVTEConfig returns the baseline touch-event generation config for a
// game mode. Callers (GUI / CLI) layer their own jitter/advanced overrides on
// top, so the defaults live in exactly one place.
//
// Our Notes shares BanG's tap/flick shape but reaches further (1/4 is about one
// size-6 note width) and leads the whole gesture by 30ms: the game only
// recognises a swipe once enough travel has accumulated, so a flick drawn from
// its exact note time lands late. Its lane narrows towards the top of the
// screen, so a long upward travel can shift the finger's effective lane; if
// flicks get misjudged as a neighbouring lane, lower FlickFactor (e.g. 1/5).
func newDefaultVTEConfig(mode string) *scores.VTEGenerateConfig {
	c := &scores.VTEGenerateConfig{
		TapDuration:         10,
		FlickDuration:       60,
		FlickReportInterval: 5,
		FlickFactor:         1.0 / 5,
		FlickPow:            1,
		SlideReportInterval: 10,
	}
	switch mode {
	case common.ModePjsk:
		c.FlickFactor = 1.0 / 6
		c.FlickDuration = 20
	case common.ModeOurNotes:
		c.FlickFactor = 1.0 / 4
		c.FlickLeadMs = 30
	}
	return c
}

func setIfPositive[T int64 | float64](dst *T, v T) {
	if v > 0 {
		*dst = v
	}
}

// nowPlaying describes the loaded song for the player card.
func nowPlaying(req gui.RunRequest) gui.NowPlaying {
	np := req.NowPlaying
	np.SongID = req.SongID
	np.Diff = req.Diff
	np.Mode = req.Mode
	return np
}

// extractAssetFilter selects which asset-bundle paths Extract unpacks: chart,
// jacket and in-game skin assets under startapp (audio .acb excluded).
// BanG Dream! Our Notes (sirius) keeps its charts under
// Assets/AddressableResources/Live/MusicScore/* and its jackets under
// Assets/Image/Jacket/*, i.e. outside startapp, so those are allowed directly.
func extractAssetFilter(p string) bool {
	lower := strings.ToLower(p)
	if strings.HasSuffix(lower, ".acb.bytes") {
		return false
	}

	if strings.Contains(lower, "musicscore") || strings.Contains(lower, "jacket") {
		return true
	}

	if !strings.Contains(p, "startapp") {
		return false
	}

	return strings.Contains(lower, "music_score/") || strings.Contains(lower, "musicjacket/") ||
		strings.Contains(lower, "jacket/") || strings.Contains(lower, "ingameskin")
}
