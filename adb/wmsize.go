// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package adb

import (
	"regexp"
	"strconv"
	"strings"
)

var wmSizeRE = regexp.MustCompile(`(\d+)\s*x\s*(\d+)`)

// ParseWMSize extracts the display resolution from `wm size` shell output,
// preferring the active "Override size" line over the "Physical size" one.
// Returns 0, 0 when no size line is present or it cannot be parsed.
func ParseWMSize(out string) (width, height int) {
	for _, key := range []string{"Override size:", "Physical size:"} {
		for _, line := range strings.Split(out, "\n") {
			if !strings.Contains(line, key) {
				continue
			}
			if m := wmSizeRE.FindStringSubmatch(line); m != nil {
				w, wErr := strconv.Atoi(m[1])
				h, hErr := strconv.Atoi(m[2])
				if wErr == nil && hErr == nil && w > 0 && h > 0 {
					return w, h
				}
			}
		}
	}
	return 0, 0
}
