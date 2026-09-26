// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// Human-like variation for the GUI: timing/position/hold-time jitter and an
// exact number of tap notes hit early enough to judge as Great.
//
// It is layered on top of the upstream generator instead of being written into
// GenerateTouchEvent: the generator calls VTEGenerateConfig.beforeEmit once
// pointers are allocated, the notes are adjusted there, and the few changes
// that cannot be expressed on notes (per-note hold time, flick touch-down
// offset) are applied to the generated events afterwards. generate.go
// therefore stays identical to upstream apart from that one hook.

package scores

import (
	"maps"
	"math"
	"math/rand"
	"slices"
	"time"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/log"
)

type HumanizeConfig struct {
	TimingJitter   int64   // ± ms applied to each note's start
	PositionJitter float64 // ± track units applied to note positions
	TapDurJitter   int64   // ± ms applied to the hold time of tap and drag notes

	GreatOffsetMs    int64 // notes chosen for Great are hit this many ms early
	GreatTargetCount int64 // exact number of tap notes to hit as Great; 0 disables
}

// GenerateHumanizedTouchEvent generates touch events like GenerateTouchEvent and
// applies h to them. It also returns how many tap notes were shifted for Great.
func GenerateHumanizedTouchEvent(config *VTEGenerateConfig, h HumanizeConfig, chart Chart) (common.RawVirtualEvents, int) {
	return generateHumanized(config, h, chart, rand.New(rand.NewSource(time.Now().UnixNano())))
}

func generateHumanized(config *VTEGenerateConfig, h HumanizeConfig, chart Chart, rng *rand.Rand) (common.RawVirtualEvents, int) {
	p := &humanizer{config: config, h: h, rng: rng, flickDownX: map[touchKey]float64{}}
	c := *config
	c.beforeEmit = p.adjust
	events := GenerateTouchEvent(&c, chart)
	return p.finish(events), p.greatApplied
}

// touchKey identifies a touch-down: pointer id and start tick.
type touchKey struct {
	pointerID int
	ms        int64
}

type hold struct {
	touchKey
	dur int64
}

type humanizer struct {
	config *VTEGenerateConfig
	h      HumanizeConfig
	rng    *rand.Rand

	greatApplied int
	holds        []hold               // tap/drag notes whose hold time differs from TapDuration
	flickDownX   map[touchKey]float64 // flick touch-down positions that differ from the note track
}

func (p *humanizer) jitterMs(base int64) int64 {
	if p.h.TimingJitter <= 0 {
		return base
	}
	half := p.h.TimingJitter
	return base + p.rng.Int63n(half*2+1) - half
}

func (p *humanizer) jitterTrack(base float64) float64 {
	if p.h.PositionJitter <= 0 {
		return base
	}
	return base + (p.rng.Float64()*2-1)*p.h.PositionJitter
}

func (p *humanizer) tapDur() int64 {
	if p.h.TapDurJitter <= 0 {
		return p.config.TapDuration
	}
	half := p.h.TapDurJitter
	return max(p.config.TapDuration+p.rng.Int63n(half*2+1)-half, 1)
}

// flickStartX returns where a flick starts: wide, mostly horizontal flicks
// start from the edge opposite to their direction so the swipe covers the note.
func flickStartX(s *star) float64 {
	c := math.Cos(s.direction)
	if s.width <= 1.0/6 || math.Abs(c) <= 0.5 {
		return s.track
	}
	if c > 0 {
		return s.track - s.width/2
	}
	return s.track + s.width/2
}

func setStartMs(s *star, ms int64) {
	s.seconds = float64(ms) / 1000
}

// adjust runs after pointer allocation and before touch events are emitted.
// It moves notes in place so the generator emits the humanized timing and
// positions. Notes on the same pointer never overlap: a note that would start
// before the previous one on its pointer ends is pushed back.
func (p *humanizer) adjust(stars []*star, pointers map[int]int) {
	greatOffsets := p.pickGreatTaps(stars)

	pointerLastEnd := map[int]int64{}
	clampStart := func(pointerID int, start int64) int64 {
		if prevEnd, ok := pointerLastEnd[pointerID]; ok && start <= prevEnd {
			return prevEnd + 1
		}
		return start
	}
	setEnd := func(pointerID int, end int64) {
		if prevEnd, ok := pointerLastEnd[pointerID]; !ok || end > prevEnd {
			pointerLastEnd[pointerID] = end
		}
	}
	flickEnd := p.config.FlickDuration + p.config.FlickReportInterval

	for idx, event := range stars {
		pointerID := pointers[idx]
		switch event.kind() {
		case tapNote, dragNote:
			ms := p.jitterMs(quantify(event.seconds))
			ms += greatOffsets[event]
			ms = clampStart(pointerID, ms)
			event.track = p.jitterTrack(event.track)
			dur := p.tapDur()
			setStartMs(event, ms)
			if dur != p.config.TapDuration {
				p.holds = append(p.holds, hold{touchKey{pointerID, ms}, dur})
			}
			setEnd(pointerID, ms+dur)
		case throwNote, flickNote:
			ms := p.jitterMs(quantify(event.seconds))
			ms = clampStart(pointerID, ms)
			// The swipe itself starts from the unjittered start point; only
			// the touch-down is moved.
			event.track = flickStartX(event)
			if x := p.jitterTrack(event.track); x != event.track {
				p.flickDownX[touchKey{pointerID, ms}] = x
			}
			setStartMs(event, ms)
			setEnd(pointerID, ms+flickEnd)
		case slideNote:
			var ms int64
			var last *star
			for step := range event.iterSlide() {
				if last == nil {
					// Only the slide's start is shifted in time.
					ms = clampStart(pointerID, p.jitterMs(quantify(step.seconds)))
				} else {
					// Keep steps strictly increasing so no two moves of the
					// same pointer share a tick.
					ms = max(quantify(step.seconds), ms+1)
				}
				setStartMs(step, ms)
				step.track = p.jitterTrack(step.track)
				last = step
			}
			// A flick at the end of a slide belongs to its last step.
			if last != event {
				event.direction = last.direction
			}
			if event.isFlick() {
				setEnd(pointerID, ms+flickEnd)
			} else {
				setEnd(pointerID, ms+1)
			}
		}
	}
}

// pickGreatTaps chooses GreatTargetCount tap notes to hit early and returns the
// start offset for each. The first tap is never chosen so the song start stays
// a stable sync anchor.
func (p *humanizer) pickGreatTaps(stars []*star) map[*star]int64 {
	offsets := map[*star]int64{}
	offset := p.h.GreatOffsetMs
	if offset < 0 {
		offset = -offset
	}
	if p.h.GreatTargetCount <= 0 || offset == 0 {
		return offsets
	}

	taps := []*star{}
	for _, s := range stars {
		if s.kind() == tapNote {
			taps = append(taps, s)
		}
	}
	totalTapCount := len(taps)
	if len(taps) > 1 {
		taps = taps[1:]
	} else {
		taps = nil
	}

	target := min(int(p.h.GreatTargetCount), len(taps))
	for _, i := range p.rng.Perm(len(taps))[:target] {
		offsets[taps[i]] = -offset
	}
	p.greatApplied = len(offsets)
	log.Debugf("GreatCount mode: requested=%d selected=%d eligible=%d totalTap=%d firstTapProtected=true offsetMs=%d", p.h.GreatTargetCount, len(offsets), len(taps), totalTapCount, offset)
	return offsets
}

// finish applies the per-note hold times and flick touch-down positions
// recorded by adjust to the generated events.
func (p *humanizer) finish(events common.RawVirtualEvents) common.RawVirtualEvents {
	if len(p.holds) == 0 && len(p.flickDownX) == 0 {
		return events
	}

	byTick := make(map[int64][]*common.VirtualTouchEvent, len(events))
	for _, item := range events {
		byTick[item.Timestamp] = item.Events
	}

	for key, x := range p.flickDownX {
		for _, e := range byTick[key.ms] {
			if e.PointerID == key.pointerID && e.Action == common.TouchDown {
				e.X = x
				break
			}
		}
	}

	// The generator releases every tap/drag after TapDuration; move each
	// release to the note's own hold time.
	for _, h := range p.holds {
		from := h.ms + p.config.TapDuration
		i := slices.IndexFunc(byTick[from], func(e *common.VirtualTouchEvent) bool {
			return e.PointerID == h.pointerID && e.Action == common.TouchUp
		})
		if i < 0 {
			continue
		}
		up := byTick[from][i]
		byTick[from] = slices.Delete(byTick[from], i, i+1)
		to := h.ms + h.dur
		byTick[to] = append(byTick[to], up)
	}

	result := make(common.RawVirtualEvents, 0, len(byTick))
	for _, tick := range slices.Sorted(maps.Keys(byTick)) {
		if len(byTick[tick]) == 0 {
			continue
		}
		result = append(result, &common.VirtualEventsItem{
			Timestamp: tick,
			Events:    byTick[tick],
		})
	}
	return result
}
