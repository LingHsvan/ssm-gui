// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// cachedSource is a JSON document fetched from the web and cached on disk, so
// the song list still works offline.
type cachedSource struct {
	cacheFile string
	url       string
}

const sekaiMasterDB = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/"

var (
	bestdoriSongs = cachedSource{"./all.5.json", "https://bestdori.com/api/songs/all.5.json"}
	bestdoriBands = cachedSource{"./all.1.json", "https://bestdori.com/api/bands/all.1.json"}

	sekaiMusics            = cachedSource{"./sekai_master_db_diff_musics.json", sekaiMasterDB + "musics.json"}
	sekaiMusicDifficulties = cachedSource{"./sekai_master_db_diff_music_difficulties.json", sekaiMasterDB + "musicDifficulties.json"}
	sekaiMusicArtists      = cachedSource{"./sekai_master_db_diff_music_artists.json", sekaiMasterDB + "musicArtists.json"}
)

var songDBClient = &http.Client{Timeout: 30 * time.Second}

// handleSongDB returns the song list for the search box (?mode=bang|pjsk).
func (s *Server) handleSongDB(w http.ResponseWriter, r *http.Request) {
	// Hook: "ournotes" is a fully local database this fork adds; it never
	// touches the network. See orunotesdb.go.
	if handleOurNotesSongDB(w, r) {
		return
	}

	var (
		db  any
		err error
	)
	if r.URL.Query().Get("mode") == "pjsk" {
		db, err = loadSekaiSongDB()
	} else {
		db, err = loadBangSongDB()
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, db)
}

func loadBangSongDB() (any, error) {
	songs, err := bestdoriSongs.load()
	if err != nil {
		return nil, fmt.Errorf("songs: %w", err)
	}
	bands, err := bestdoriBands.load()
	if err != nil {
		return nil, fmt.Errorf("bands: %w", err)
	}
	return map[string]json.RawMessage{
		"songs": songs,
		"bands": bands,
	}, nil
}

func loadSekaiSongDB() (any, error) {
	musics, err := sekaiMusics.load()
	if err != nil {
		return nil, fmt.Errorf("songs: %w", err)
	}
	difficulties, err := sekaiMusicDifficulties.load()
	if err != nil {
		return nil, fmt.Errorf("difficulties: %w", err)
	}
	artists, err := sekaiMusicArtists.load()
	if err != nil {
		return nil, fmt.Errorf("artists: %w", err)
	}
	return map[string]json.RawMessage{
		"songs":             musics,
		"songsJp":           musics,
		"bands":             json.RawMessage("{}"),
		"artists":           artists,
		"musicDifficulties": difficulties,
	}, nil
}

// load fetches the latest version, refreshing the cache, and falls back to
// the cache when the fetch fails.
func (c cachedSource) load() (json.RawMessage, error) {
	data, fetchErr := c.fetch()
	if fetchErr == nil {
		if cached, err := os.ReadFile(c.cacheFile); err != nil || !bytes.Equal(cached, data) {
			writeFileAtomic(c.cacheFile, data)
		}
		return data, nil
	}
	if cached, err := os.ReadFile(c.cacheFile); err == nil && json.Valid(cached) {
		return cached, nil
	}
	return nil, fetchErr
}

func (c cachedSource) fetch() ([]byte, error) {
	resp, err := songDBClient.Get(c.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", c.url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("GET %s: response is not JSON", c.url)
	}
	return data, nil
}

// writeFileAtomic writes data to a temp file and renames it into place, so a
// concurrent reader never sees a half-written cache file. Best effort: the
// cache is non-essential.
func writeFileAtomic(path string, data []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		os.Rename(tmp.Name(), path)
	}
}
