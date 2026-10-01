// Copyright (C) 2026 LingHsvan
// SPDX-License-Identifier: GPL-3.0-or-later

// Auto Detect / Kill ADB (fork-only).
//
// Upstream's versions are deliberately minimal. This fork made Auto Detect
// self-healing: a cold adb server (one that `adb devices` was never run
// against) used to make detection fail, so the handler starts the server
// itself, polls until the device list settles, and registers the found
// device's resolution so playback can start without a trip to Settings.

package gui

import (
	"context"
	"net/http"
	"os/exec"
	"time"

	"github.com/kvarenzn/ssm/adb"
)

// MarkAdbBusy records whether an adb-based controller (scrcpy) is being opened
// or used. While busy, Auto Detect must not stop the shared adb server the
// playback path depends on.
func (s *Server) MarkAdbBusy(busy bool) {
	s.adbBusy.Store(busy)
}

// killADB stops the adb server, which clears "device offline" and "more than
// one device" states. The call is bounded because a wedged adb binary must not
// hang this HTTP request; kill is best-effort, so errors are ignored.
func (s *Server) killADB(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "adb", "kill-server").Run()

	w.WriteHeader(http.StatusOK)
}

// detectADB powers the "Auto Detect" button: it starts the adb server when
// needed, waits for the device list to settle, registers the found device's
// resolution, and restores the previous server state before replying.
func (s *Server) detectADB(w http.ResponseWriter, r *http.Request) {
	// Serialize with the detect-song screencap cycle (songocr.go): both
	// start/stop the one global adb server.
	adbCaptureMu.Lock()
	defer adbCaptureMu.Unlock()

	// Only restore "server off" when the server was off before this call; a
	// server the user (or a capture cycle) already had running is left alone.
	// Never stop it while an adb controller (scrcpy) is being opened or a
	// run is not idle -- that yanks the server out from under a transfer
	// (forward / push / screencap) that is still using it.
	startedByUs := !adb.IsADBServerRunning("localhost", 5037)
	defer func() {
		if !startedByUs || s.adbBusy.Load() {
			return
		}
		s.mu.Lock()
		idle := s.status.State == StateIdle
		s.mu.Unlock()
		if idle {
			_ = adb.StopADBServer("localhost", 5037)
		}
	}()

	client := adb.NewDefaultClient()

	// A cold adb server takes ~2s to enumerate USB devices, and a server can
	// also die mid-poll -- so every iteration (re)starts it and re-lists.
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
	// the first authorized device -- Auto Detect is how new devices get added.
	device := adb.PickRecordedOrFirstAuthorized(devices, s.conf.RecordedSerials())
	if device == nil {
		writeJSON(w, http.StatusOK, map[string]any{"serial": "", "seen": len(devices)})
		return
	}

	// Register (or refresh) the device's resolution so playback can start
	// right away -- the same data Settings' "Add / Update Device" stores.
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

	writeJSON(w, http.StatusOK, map[string]any{
		"serial": device.Serial(),
		"width":  width,
		"height": height,
		"saved":  saved,
		"seen":   len(devices),
	})
}
