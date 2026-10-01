// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed frontend/dist
var frontendFiles embed.FS

// staticHandler serves the built frontend embedded in the binary.
func staticHandler() (http.Handler, error) {
	dist, err := fs.Sub(frontendFiles, "frontend/dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pages must never be cached, so a rebuilt binary always serves the
		// matching (hash-named) assets.
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		files.ServeHTTP(w, r)
	}), nil
}
