// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"net/http"
	"os/exec"

	"github.com/kvarenzn/ssm/adb"
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
	serial := ""
	if devices, err := adb.NewDefaultClient().Devices(); err == nil {
		if d := adb.FirstAuthorizedDevice(devices); d != nil {
			serial = d.Serial()
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"serial": serial})
}

// handleKillADB stops the adb server, which clears "device offline" and
// "more than one device" states. Best effort: adb may not be installed.
func (s *Server) handleKillADB(w http.ResponseWriter, r *http.Request) {
	exec.Command("adb", "kill-server").Run()
	w.WriteHeader(http.StatusOK)
}
