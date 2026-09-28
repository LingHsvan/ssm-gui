// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"net/http"
	"os"
)

// handleExtract unpacks game assets from a directory pulled from the device.
func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Path == "" {
		http.Error(w, "path field is required", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(body.Path); err != nil {
		http.Error(w, "path not found: "+body.Path, http.StatusBadRequest)
		return
	}
	if err := s.backend.Extract(body.Path); err != nil {
		http.Error(w, "extraction failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
