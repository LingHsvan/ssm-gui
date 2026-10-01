// Copyright (C) 2026 LingHsvan
// SPDX-License-Identifier: GPL-3.0-or-later

// Our Notes song list and artwork (fork-only).
//
// Unlike bang/pjsk, this mode has no web source: the chart data is read from
// the locally unpacked assets/ournotes directory, so these endpoints never
// touch the network.

package gui

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/db"
)

// handleOurNotesSongDB serves the Our Notes song list and reports whether it
// answered the request. Upstream's handleSongDB calls it first, so an
// unknown/empty mode still falls through to the bang path.
func handleOurNotesSongDB(w http.ResponseWriter, r *http.Request) bool {
	if common.NormalizeMode(r.URL.Query().Get("mode")) != common.ModeOurNotes {
		return false
	}

	notesDB, err := db.NewOurNotesDB()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ournotes database: " + err.Error()})
		return true
	}
	payload, err := notesDB.Payload()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ournotes payload: " + err.Error()})
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(payload)
	return true
}

// handleOurNotesJacket serves a locally unpacked Our Notes jacket image:
//
//	GET /api/ournotes-jacket?id=<songId>&size=full|thumb
//
// Requests for an unknown song (or a song without artwork) fall back to the
// placeholder jacket so the UI always has an image to show.
func (s *Server) handleOurNotesJacket(w http.ResponseWriter, r *http.Request) {
	var id int
	if _, err := fmt.Sscanf(r.URL.Query().Get("id"), "%d", &id); err != nil {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	thumb := r.URL.Query().Get("size") == "thumb"

	file := ""
	if notesDB, err := db.NewOurNotesDB(); err == nil {
		if p, ok := notesDB.JacketPath(id, thumb); ok {
			file = p
		}
	}
	if file == "" || !fileExists(file) {
		file = filepath.Join(db.OurNotesDir, "jackets", "jacket_temporary.png")
		if thumb {
			file = filepath.Join(db.OurNotesDir, "jackets", "small", "jacket_temporary.png")
		}
	}
	if !fileExists(file) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, file)
}
