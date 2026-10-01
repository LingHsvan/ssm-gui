// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"net/http"
)

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.conf.Snapshot())
}

func (s *Server) handleSaveDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Serial string `json:"serial"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := decodeJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Serial == "" || body.Width <= 0 || body.Height <= 0 {
		http.Error(w, "serial, width and height are required", http.StatusBadRequest)
		return
	}
	if err := s.conf.SetDevice(body.Serial, body.Width, body.Height); err != nil {
		http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Serial string `json:"serial"`
	}
	if err := decodeJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.conf.DeleteDevice(body.Serial); err != nil {
		http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleDetectADB returns the serial of the first authorized adb device, or
// an empty serial when there is none.
func (s *Server) handleDetectADB(w http.ResponseWriter, r *http.Request) {
	// Hook: this fork's Auto Detect also cold-starts adb, waits for the
	// device list to settle and registers the found device's resolution, so
	// playback can start without a trip to Settings. See detectadb.go.
	s.detectADB(w, r)
}

// handleKillADB stops the adb server, which clears "device offline" and
// "more than one device" states. Best effort: adb may not be installed.
func (s *Server) handleKillADB(w http.ResponseWriter, r *http.Request) {
	// Hook: bounded so a wedged adb binary cannot hang this request forever.
	// See detectadb.go.
	s.killADB(w, r)
}
