// Copyright (C) 2024, 2025, 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package scores

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// BanG Dream! Our Notes (sirius) chart format.
//
// Charts are gzip-compressed JSON hosted at
// Assets/AddressableResources/Live/MusicScore/<NNNN>_<DD>.bytes:
//
//	{"meta":{"version":100},
//	 "score":{"events":{"bpm":[{"t":0,"bpm":190}], "sig":[...]},
//	          "notes":[{"t":26880,"pos":12,"size":8},
//	                   {"type":"flick","t":14400,"pos":0,"size":6,"dir":"left"},
//	                   {"type":"long","node":[{"t":70560,"pos":12,"size":6},
//	                                          {"t":71040,"pos":18,"size":6}]},
//	                   {"type":"guide","node":[{"visible":false,...},{"pos":"auto"},...]}]}}
//
// Semantics (verified against the game's own manifests / haneoka-cassiopeia):
//   - t      : tick, PPQ = 480 (NOT milliseconds)
//   - pos    : LEFT edge of the note on a 0..24 lane grid (12 physical lanes,
//     addressed in half-lanes); may be negative or exceed 24 (overhang)
//   - size   : width in the same grid, defaults to 6, may be 0
//   - type   : "" (tap) | flick | trace | long | guide
//   - node   : node chain of a long/guide; may contain pos:"auto" and visible:false
//   - dir    : flick direction, only "left"/"right" occur in practice
//   - crit   : wider (easy) judgement area; does not change the touch itself
//   - ease   : node easing (in/out); approximated as linear here
const (
	ourNotesPPQ       = 480.0
	ourNotesLaneCount = 24.0
	ourNotesDefSize   = 6.0
	ourNotesDefBPM    = 120.0
)

type onNote struct {
	Type    string          `json:"type"`
	T       float64         `json:"t"`
	Pos     json.RawMessage `json:"pos"` // number | "auto"
	Size    *float64        `json:"size"`
	Node    []onNote        `json:"node"`
	Dir     string          `json:"dir"`
	Crit    bool            `json:"crit"`
	Visible *bool           `json:"visible"`
}

type onBPMEvent struct {
	T   float64 `json:"t"`
	BPM float32 `json:"bpm"`
}

type onChart struct {
	Score struct {
		Events struct {
			BPM []onBPMEvent `json:"bpm"`
		} `json:"events"`
		Notes []onNote `json:"notes"`
	} `json:"score"`
}

// ParseOurNotes parses a BanG Dream! Our Notes (sirius) chart into the shared
// Chart representation. data may be the raw gzip-compressed .bytes payload or
// already-decoded JSON; a UTF-8 BOM is tolerated either way.
func ParseOurNotes(data []byte) (Chart, error) {
	raw, err := decodeOurNotes(data)
	if err != nil {
		return nil, err
	}

	var c onChart
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("ournotes: parse chart json: %w", err)
	}

	return buildOurNotes(&c), nil
}

var ourNotesBOM = []byte{0xEF, 0xBB, 0xBF}

func decodeOurNotes(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, ourNotesBOM)
	if len(data) < 2 || data[0] != 0x1F || data[1] != 0x8B {
		return data, nil
	}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ournotes: open gzip stream: %w", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("ournotes: gunzip chart: %w", err)
	}

	return bytes.TrimPrefix(out, ourNotesBOM), nil
}

// ── time ────────────────────────────────────────────────────────────────────

type onSegment struct {
	startTick float64
	bpm       float32
	startMs   float64
}

// ourNotesDeltaMs mirrors the game: (dTick * 60000) / float32(bpm * 480).
func ourNotesDeltaMs(dTick float64, bpm float32) float64 {
	return (dTick * 60000.0) / float64(bpm*ourNotesPPQ)
}

// newOurNotesClock builds a tick -> millisecond converter. BPM changes are
// accumulated per segment, each segment start rounded half-to-even and the
// final value floored, matching the game's TickConverter.
func newOurNotesClock(events []onBPMEvent) func(tick float64) float64 {
	if len(events) == 0 {
		events = []onBPMEvent{{T: 0, BPM: ourNotesDefBPM}}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].T < events[j].T })

	segs := make([]onSegment, 0, len(events)+1)
	if events[0].T > 0 {
		segs = append(segs, onSegment{startTick: 0, bpm: ourNotesDefBPM, startMs: 0})
	}
	for _, e := range events {
		bpm := e.BPM
		if !(bpm > 0) {
			bpm = ourNotesDefBPM
		}

		startMs := 0.0
		if n := len(segs); n > 0 {
			prev := segs[n-1]
			startMs = math.RoundToEven(prev.startMs + ourNotesDeltaMs(e.T-prev.startTick, prev.bpm))
		}
		segs = append(segs, onSegment{startTick: e.T, bpm: bpm, startMs: startMs})
	}

	return func(tick float64) float64 {
		idx := sort.Search(len(segs), func(i int) bool { return segs[i].startTick > tick }) - 1
		if idx < 0 {
			idx = 0
		}
		s := segs[idx]
		return math.Floor(s.startMs + ourNotesDeltaMs(tick-s.startTick, s.bpm))
	}
}

// ── geometry ────────────────────────────────────────────────────────────────

func (n onNote) autoPos() bool {
	return len(n.Pos) > 0 && strings.TrimSpace(string(n.Pos)) == `"auto"`
}

func (n onNote) width() float64 {
	if n.Size != nil {
		return *n.Size
	}
	return ourNotesDefSize
}

// leftEdge returns the note's left edge; ok is false for pos:"auto" (or an
// unparseable value), which callers resolve via interpolation or skip.
func (n onNote) leftEdge() (float64, bool) {
	if n.autoPos() {
		return 0, false
	}
	var p float64
	if err := json.Unmarshal(n.Pos, &p); err != nil {
		return 0, false
	}
	return p, true
}

// ourNotesTrackWidth maps the 0..24 chart grid onto the shared 0..1 track /
// width used by the rest of the package, keeping the note centre inside the
// lane field.
func ourNotesTrackWidth(pos, size float64) (track, width float64) {
	width = size / ourNotesLaneCount
	track = (pos + size/2) / ourNotesLaneCount

	half := width / 2
	if track < half {
		track = half
	}
	if track > 1-half {
		track = 1 - half
	}
	return
}

// ourNotesResolveLeft resolves the left edge of every node, filling pos:"auto"
// nodes by linear interpolation between the nearest concrete neighbours.
func ourNotesResolveLeft(nodes []onNote) []float64 {
	left := make([]float64, len(nodes))
	concrete := make([]int, 0, len(nodes))
	for i := range nodes {
		if p, ok := nodes[i].leftEdge(); ok {
			left[i] = p
			concrete = append(concrete, i)
		}
	}

	for i := range nodes {
		if !nodes[i].autoPos() {
			continue
		}

		lo, hi := -1, -1
		for _, c := range concrete {
			if c < i {
				lo = c
			} else if c > i && hi < 0 {
				hi = c
			}
		}

		switch {
		case lo >= 0 && hi >= 0:
			r := float64(i-lo) / float64(hi-lo)
			left[i] = left[lo] + (left[hi]-left[lo])*r
		case lo >= 0:
			left[i] = left[lo]
		case hi >= 0:
			left[i] = left[hi]
		}
	}

	return left
}

// ourNotesFlickDeg converts the chart's flick direction to the degrees used by
// star.flickToIfOk (up = 90, left = 180, right = 0).
func ourNotesFlickDeg(dir string) int {
	switch dir {
	case "left":
		return 180
	case "right":
		return 0
	default: // "up", "down" or absent
		return 90
	}
}

// ── chart building ──────────────────────────────────────────────────────────

func buildOurNotes(c *onChart) Chart {
	clock := newOurNotesClock(c.Score.Events.BPM)
	events := Chart{}

	for _, n := range c.Score.Notes {
		switch n.Type {
		case "long":
			// A long is a slide: head taps down and the finger follows the
			// node chain to the end. visible:false nodes are still path
			// points the finger must travel through, so they are kept.
			if len(n.Node) < 2 {
				continue
			}

			left := ourNotesResolveLeft(n.Node)
			var prev, last *star
			for i := range n.Node {
				track, width := ourNotesTrackWidth(left[i], n.Node[i].width())
				s := newStar(clock(n.Node[i].T)/1000.0, track, width)
				if i == 0 {
					s.markAsTap().markAsHead()
				} else {
					s.chainsAfter(prev)
				}
				prev, last = s, s
			}

			end := n.Node[len(n.Node)-1]
			if end.Type == "flick" {
				// flickToIfOk also marks the node as the slide end.
				last.flickToIfOk(true, ourNotesFlickDeg(end.Dir))
			} else {
				last.markAsEnd()
			}
			events = append(events, last)

		case "guide", "trace":
			// guide: a guidance path with no independent judgement (its nodes
			// are mostly visible:false), so it needs no touch. A top-level
			// trace is likewise not a press target. Both are skipped.

		default: // "", "tap", "flick"
			pos, ok := n.leftEdge()
			if !ok {
				continue // an "auto" position outside a line cannot be pressed
			}

			track, width := ourNotesTrackWidth(pos, n.width())
			s := newStar(clock(n.T)/1000.0, track, width).markAsTap()
			if n.Type == "flick" {
				s.flickToIfOk(true, ourNotesFlickDeg(n.Dir))
			}
			events = append(events, s)
		}
	}

	return events
}
