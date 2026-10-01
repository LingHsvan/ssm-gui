// Copyright (C) 2026 LingHsvan
// SPDX-License-Identifier: GPL-3.0-or-later

// Endpoints and hook helpers this fork adds on top of upstream. Everything
// here lives in files upstream does not have, so upstream's own gui files stay
// mergeable -- see docs/UPSTREAM_SYNC.md.

package gui

import (
	"net/http"

	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/log"
)

// registerForkRoutes adds the endpoints upstream does not serve. It is called
// from Server.handler once upstream's own routes are registered, so the paths
// upstream already owns (/api/songdb, /api/detect-adb, /api/kill-adb) are
// hooked inside their handlers instead of being registered twice.
func (s *Server) registerForkRoutes(mux *http.ServeMux) {
	// Song-detection ROI calibration, keyed by device serial and game mode.
	mux.HandleFunc("GET /api/device-roi", s.handleDeviceROI)
	mux.HandleFunc("DELETE /api/device-roi", s.handleDeviceROI)

	// Persisted fine-tuning panels and startup preferences.
	mux.HandleFunc("GET /api/tuning", s.handleTuning)
	mux.HandleFunc("POST /api/tuning", s.handleTuning)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleSettings)

	// Our Notes song artwork, read from the unpacked assets directory.
	mux.HandleFunc("GET /api/ournotes-jacket", s.handleOurNotesJacket)

	// Screenshot + OCR song recognition.
	mux.HandleFunc("GET /api/detect-song", s.handleDetectSong)
}

// controlBroken reports whether the controller's outbound channel has failed
// for good, so a playback loop can stop instead of pushing the rest of the
// chart into a dead socket. Only the adb (scrcpy) backend can tell.
func controlBroken(ctrl controllers.Controller) bool {
	sc := scrcpyControllerOf(ctrl)
	if sc == nil || !sc.Broken() {
		return false
	}
	log.Warnf("[GUI] autoplay aborted early: control connection to the device was lost")
	return true
}

// scrcpyControllerOf returns the underlying scrcpy controller of ctrl, or nil.
// Both the concrete type and upstream's non-fatal GUI wrapper are recognised.
func scrcpyControllerOf(ctrl controllers.Controller) *controllers.ScrcpyController {
	switch c := ctrl.(type) {
	case *controllers.ScrcpyController:
		return c
	case controllers.GUIScrcpy:
		return c.ScrcpyController
	default:
		return nil
	}
}

// hidControllerOf returns the underlying HID controller of ctrl, or nil. The
// GUI opens controllers.GUIHID (its Send must not be log.Fatal), which wraps
// the concrete controller, so both forms have to be recognised.
func hidControllerOf(ctrl controllers.Controller) *controllers.HIDController {
	switch c := ctrl.(type) {
	case *controllers.HIDController:
		return c
	case controllers.GUIHID:
		return c.HIDController
	default:
		return nil
	}
}
