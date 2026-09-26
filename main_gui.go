// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// GUI entry point. Everything specific to the browser GUI lives in this file so
// that main.go stays identical to upstream (kvarenzn/ssm) apart from a single
// `if guiRequested()` hook, which keeps upstream merges conflict-free.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/scores"
)

var (
	guiMode bool
	guiPort int
)

func init() {
	flag.BoolVar(&guiMode, "gui", false, "Start the graphical interface (browser GUI)")
	flag.IntVar(&guiPort, "port", 8765, "Port used by the GUI (default 8765)")
}

// guiRequested reports whether to run the GUI: either -gui was given, or the
// program was started without any arguments (e.g. double-clicked).
func guiRequested() bool {
	return guiMode || len(os.Args) == 1
}

const guiConfigPath = "./config.json"

// ensureConfigFile seeds an empty JSON object when the config file is missing
// or empty. Upstream config.Load creates an empty file on first run and then
// fails to parse it, which would stop the GUI from ever starting.
func ensureConfigFile(path string) error {
	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte("{}"), 0o600)
}

func runGUI() {
	if err := ensureConfigFile(guiConfigPath); err != nil {
		log.Die(err)
	}
	conf, err := config.Load(guiConfigPath)
	if err != nil {
		log.Die(err)
	}

	srv := gui.NewServer(guiPort, conf)

	// Ensure only one playback goroutine runs at a time.
	var (
		runMu         sync.Mutex
		currentCancel context.CancelFunc
		doneCh        chan struct{}
	)

	runOnce := func(req gui.RunRequest) {
		// Cancel the previous run and wait for it to finish (including scrcpy.Close).
		runMu.Lock()
		if currentCancel != nil {
			currentCancel()
			old := doneCh
			runMu.Unlock()
			<-old
			runMu.Lock()
		}

		ctx, cancel := context.WithCancel(context.Background())
		currentCancel = cancel
		thisDone := make(chan struct{})
		doneCh = thisDone
		runMu.Unlock()

		go func() {
			defer func() {
				cancel()
				close(thisDone)
			}()
			// log.Fatal/Die panic with log.FatalErr; report it in the UI
			// instead of taking the whole GUI down.
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(log.FatalErr); ok {
						srv.SetError("Playback aborted by a fatal error. See the console log for details.")
						return
					}
					panic(r)
				}
			}()

			backend = req.Backend
			songID = req.SongID
			difficulty = req.Diff
			direction = req.Orient
			chartPath = req.ChartPath
			deviceSerial = req.DeviceSerial
			pjskMode = req.Mode == "pjsk"

			var chartText []byte
			var err error
			if chartPath == "" {
				pathResults, globErr := findMusicscorePath(pjskMode, songID, difficulty)
				if globErr != nil || len(pathResults) < 1 {
					srv.SetError("Musicscore not found. Please extract assets first or use a custom chart path.")
					return
				}
				chartText, err = os.ReadFile(pathResults[0])
			} else {
				chartText, err = os.ReadFile(chartPath)
			}
			if err != nil {
				srv.SetError("Failed to read musicscore: " + err.Error())
				return
			}

			var chart scores.Chart
			if pjskMode {
				chart, err = scores.ParseSUS(string(chartText))
				if err != nil {
					srv.SetError("Failed to parse SUS: " + err.Error())
					return
				}
			} else {
				chart = scores.ParseBMS(string(chartText))
			}

			genConfig := newDefaultVTEConfig(pjskMode)
			// Override defaults with user-supplied advanced params (0 = keep default)
			if req.TapDuration > 0 {
				genConfig.TapDuration = req.TapDuration
			}
			if req.FlickDuration > 0 {
				genConfig.FlickDuration = req.FlickDuration
			}
			if req.FlickReportInterval > 0 {
				genConfig.FlickReportInterval = req.FlickReportInterval
			}
			if req.SlideReportInterval > 0 {
				genConfig.SlideReportInterval = req.SlideReportInterval
			}
			if req.FlickFactor > 0 {
				genConfig.FlickFactor = req.FlickFactor
			}
			if req.FlickPow > 0 {
				genConfig.FlickPow = req.FlickPow
			}

			humanize := scores.HumanizeConfig{
				TimingJitter:   req.TimingJitter,
				PositionJitter: req.PositionJitter,
				TapDurJitter:   req.TapDurJitter,
				GreatOffsetMs: func() int64 {
					v := req.GreatOffsetMs
					if v < 0 {
						v = -v
					}
					if v == 0 {
						v = 10
					}
					return v
				}(),
				GreatTargetCount: max(req.GreatCount, 0),
			}
			rawEvents, greatApplied := scores.GenerateHumanizedTouchEvent(genConfig, humanize, chart)
			srv.SetGreatStats(req.GreatCount, int64(greatApplied))

			ctrl, events, err := openController(conf, backend, deviceSerial, direction == "right", rawEvents)
			if err != nil {
				srv.SetError(err.Error())
				return
			}
			defer ctrl.Close()

			if len(events) == 0 {
				srv.SetError("No playable events were generated for this chart.")
				return
			}

			np := gui.NowPlaying{
				SongID:    req.SongID,
				Diff:      req.Diff,
				Mode:      req.Mode,
				Title:     req.NowPlaying.Title,
				Artist:    req.NowPlaying.Artist,
				DiffLevel: req.NowPlaying.DiffLevel,
				JacketURL: req.NowPlaying.JacketURL,
			}

			srv.SetReady(ctrl, events, np)

			if !srv.WaitForStart(ctx) {
				return
			}

			start := time.Now().Add(-time.Duration(events[0].Timestamp) * time.Millisecond)
			srv.Autoplay(ctx, start)

			time.Sleep(300 * time.Millisecond)
		}()
	}

	srv.OnRunRequest = func(req gui.RunRequest) {
		runOnce(req)
	}

	srv.OnExtractRequest = func(path string) error {
		_, err := Extract(path, extractAssetFilter)
		return err
	}

	addr, err := srv.Start()
	if err != nil {
		log.Die("Failed to start GUI server:", err)
	}

	fmt.Printf("\n  SSM GUI started\n")
	fmt.Printf("   Open this URL in your browser: %s\n\n", addr)

	openBrowser(addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nSSM GUI closed")
}

// findMusicscorePath returns the chart .txt files matching a song id and
// difficulty under the extracted assets directory for the given game mode.
func findMusicscorePath(pjsk bool, songID int, difficulty string) ([]string, error) {
	if pjsk {
		return filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
			fmt.Sprintf("%04d_01/%s.txt", songID, difficulty)))
	}
	return filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
		fmt.Sprintf("musicscore*/%03d/*_%s.txt", songID, difficulty)))
}

// extractAssetFilter selects which asset-bundle paths Extract should unpack:
// chart, jacket and in-game skin assets under startapp (audio .acb excluded).
func extractAssetFilter(p string) bool {
	if strings.HasSuffix(p, ".acb.bytes") || !strings.Contains(p, "startapp") {
		return false
	}
	return strings.Contains(p, "musicscore/") || strings.Contains(p, "music_score/") ||
		strings.Contains(p, "musicjacket/") || strings.Contains(p, "jacket/") ||
		strings.Contains(p, "ingameskin")
}

// newDefaultVTEConfig returns the baseline touch-event generation config for a
// game mode; the GUI layers its advanced overrides on top.
func newDefaultVTEConfig(pjsk bool) *scores.VTEGenerateConfig {
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
	return c
}

// playController is a playback backend the GUI can also shut down.
type playController interface {
	controllers.Controller
	Close() error
}

// openController selects and opens the playback backend (adb or hid) and
// returns a ready controller plus the preprocessed events. The caller owns
// Close() and shows the error in the UI.
func openController(conf *config.Config, backend, serial string, turnRight bool, rawEvents common.RawVirtualEvents) (playController, []common.ViscousEventItem, error) {
	switch backend {
	case "adb":
		checkOrDownload()
		if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
			return nil, nil, fmt.Errorf("failed to start ADB server: %w", err)
		}
		devices, err := adb.NewDefaultClient().Devices()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list ADB devices: %w", err)
		}
		if len(devices) == 0 {
			return nil, nil, fmt.Errorf("%s", errNoDevice)
		}
		log.Debugln("ADB devices:", devices)
		var device *adb.Device
		if serial == "" {
			device = adb.FirstAuthorizedDevice(devices)
			if device == nil {
				return nil, nil, fmt.Errorf("no authorized ADB device found")
			}
		} else {
			for _, d := range devices {
				if d.Serial() == serial {
					device = d
					break
				}
			}
			if device == nil {
				return nil, nil, fmt.Errorf("no device has serial %q", serial)
			}
			if !device.Authorized() {
				return nil, nil, fmt.Errorf("device %q is not authorized", serial)
			}
		}
		log.Debugln("Selected device:", device)
		scrcpy := controllers.GUIScrcpy{ScrcpyController: controllers.NewScrcpyController(device)}
		if err := scrcpy.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
			scrcpy.Close()
			return nil, nil, fmt.Errorf("failed to connect to device: %w", err)
		}
		dc := conf.Lookup(device.Serial())
		if dc == nil {
			scrcpy.Close()
			return nil, nil, fmt.Errorf("device [%s] not configured. Please add it in Settings first", device.Serial())
		}
		return scrcpy, scrcpy.PreprocessGUI(rawEvents, turnRight, dc, getJudgeLineCalculator()), nil

	case "hid":
		if serial == "" {
			serials := controllers.FindHIDDevices()
			log.Debugln("Recognized devices:", serials)
			if len(serials) == 0 {
				return nil, nil, fmt.Errorf("%s", errNoDevice)
			}
			serial = serials[0]
		}
		dc := conf.Lookup(serial)
		if dc == nil {
			return nil, nil, fmt.Errorf("device [%s] not configured. Please add it in Settings first", serial)
		}
		hid, err := controllers.OpenGUIHID(dc)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize HID: %w", err)
		}
		return hid, hid.PreprocessGUI(rawEvents, turnRight, getJudgeLineCalculator()), nil

	default:
		return nil, nil, fmt.Errorf("unknown backend: %q", backend)
	}
}
