// Copyright (C) 2026 LingHsvan
// SPDX-License-Identifier: GPL-3.0-or-later

// Song-detection ROI calibration endpoint (fork-only).
//
// The ROI is the normalized [x,y,w,h] crop handed to OCR. It is genuinely
// device-specific -- the same game on two phones puts the title bar at
// different places -- so it is stored per device serial and then per game
// mode. The shared mode-level map in config keeps working as the fallback.

package gui

import (
	"net/http"
	"os"

	"github.com/kvarenzn/ssm/common"
)

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
		writeJSON(w, http.StatusOK, s.conf.DeviceROIs())
	case http.MethodDelete:
		var body struct {
			Serial string `json:"serial"`
			Mode   string `json:"mode"`
		}
		if err := decodeJSON(r, &body); err != nil {
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

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
