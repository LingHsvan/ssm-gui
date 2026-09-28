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
		<-prev.done
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
	pjsk := req.Mode == "pjsk"
	chart, err := loadChart(req, pjsk)
	if err != nil {
		return nil, nil, err
	}

	genConfig, humanize := touchConfig(req, pjsk)
	raw, greatApplied := scores.GenerateHumanizedTouchEvent(genConfig, humanize, chart)
	p.srv.SetGreatStats(req.GreatCount, int64(greatApplied))

	ctrl, events, err := openController(p.conf, req, pjsk, raw)
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
func loadChart(req gui.RunRequest, pjsk bool) (scores.Chart, error) {
	path := req.ChartPath
	if path == "" {
		matches, err := findMusicscorePath(pjsk, req.SongID, req.Diff)
		if err != nil || len(matches) == 0 {
			return nil, errors.New("musicscore not found: extract the assets first or use a custom chart path")
		}
		path = matches[0]
	}

	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read musicscore: %w", err)
	}
	if !pjsk {
		return scores.ParseBMS(string(text)), nil
	}
	chart, err := scores.ParseSUS(string(text))
	if err != nil {
		return nil, fmt.Errorf("failed to parse SUS: %w", err)
	}
	return chart, nil
}

// findMusicscorePath returns the chart files matching a song id and
// difficulty under the extracted assets directory.
func findMusicscorePath(pjsk bool, songID int, difficulty string) ([]string, error) {
	if pjsk {
		return filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
			fmt.Sprintf("%04d_01/%s.txt", songID, difficulty)))
	}
	return filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
		fmt.Sprintf("musicscore*/%03d/*_%s.txt", songID, difficulty)))
}

// touchConfig returns the touch generation and humanization settings for req.
// Generation parameters left at 0 keep the game mode's default.
func touchConfig(req gui.RunRequest, pjsk bool) (*scores.VTEGenerateConfig, scores.HumanizeConfig) {
	c := &scores.VTEGenerateConfig{
		TapDuration:         10,
		FlickDuration:       60,
		FlickReportInterval: 5,
		FlickFactor:         1.0 / 5,
		FlickPow:            1,
		SlideReportInterval: 10,
	}
	if pjsk {
		c.FlickFactor = 1.0 / 6
		c.FlickDuration = 20
	}
	setIfPositive(&c.TapDuration, req.TapDuration)
	setIfPositive(&c.FlickDuration, req.FlickDuration)
	setIfPositive(&c.FlickReportInterval, req.FlickReportInterval)
	setIfPositive(&c.SlideReportInterval, req.SlideReportInterval)
	setIfPositive(&c.FlickFactor, req.FlickFactor)
	setIfPositive(&c.FlickPow, req.FlickPow)

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
func extractAssetFilter(p string) bool {
	if strings.HasSuffix(p, ".acb.bytes") || !strings.Contains(p, "startapp") {
		return false
	}
	return strings.Contains(p, "musicscore/") || strings.Contains(p, "music_score/") ||
		strings.Contains(p, "musicjacket/") || strings.Contains(p, "jacket/") ||
		strings.Contains(p, "ingameskin")
}
