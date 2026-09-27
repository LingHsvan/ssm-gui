// Copyright (C) 2024, 2025, 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package common

import "strings"

// Supported game modes. The values double as the `mode` string exchanged
// with the front-end (RunRequest.Mode / /api/songdb?mode= / /api/detect-song).
const (
	ModeBang     = "bang"
	ModePjsk     = "pjsk"
	ModeOurNotes = "ournotes"
)

// NormalizeMode maps an arbitrary UI/CLI mode string onto one of the three
// known modes; anything unrecognized falls back to BanG (the historical
// default) so old callers keep working unchanged.
func NormalizeMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case ModePjsk:
		return ModePjsk
	case ModeOurNotes:
		return ModeOurNotes
	default:
		return ModeBang
	}
}
