// Copyright (C) 2026 LingHsvan
// SPDX-License-Identifier: GPL-3.0-or-later

// Fine-tuning and startup-preference endpoints (fork-only): the values the GUI
// sliders hold are persisted to config.json so tuning survives a restart.

package gui

import (
	"net/http"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
)

// handleTuning reads and writes the GUI's fine-tuning panels (humanization
// jitter + advanced parameters) so the user's tuning survives a restart.
//
// GET returns both panels; POST stores the jitter panel and, when a mode is
// given, that mode's advanced parameters. Advanced values are per-game-mode
// because each mode ships its own defaults.
func (s *Server) handleTuning(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jitter, advanced := s.conf.Tuning()
		writeJSON(w, http.StatusOK, map[string]any{
			"jitter":   jitter,
			"advanced": advanced,
		})
	case http.MethodPost:
		var body struct {
			Mode     string                 `json:"mode"`
			Jitter   *config.JitterConfig   `json:"jitter"`
			Advanced *config.AdvancedConfig `json:"advanced"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// An empty mode means "jitter only": NormalizeMode would fold it into
		// bang and overwrite that mode's bucket.
		mode := ""
		if body.Mode != "" {
			mode = common.NormalizeMode(body.Mode)
		}
		if err := s.conf.SetTuning(body.Jitter, mode, body.Advanced); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSettings reads and writes the GUI's startup preferences. It is kept
// apart from /api/tuning because that endpoint is about event generation, not
// application behaviour.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"autoOpenBrowser": s.conf.ShouldAutoOpenBrowser(),
		})
	case http.MethodPost:
		var body struct {
			AutoOpenBrowser *bool `json:"autoOpenBrowser"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// The pointer distinguishes "field omitted" from an explicit false, so a
		// malformed body cannot silently turn the setting off.
		if body.AutoOpenBrowser == nil {
			http.Error(w, "autoOpenBrowser required", http.StatusBadRequest)
			return
		}
		if err := s.conf.SetAutoOpenBrowser(*body.AutoOpenBrowser); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
