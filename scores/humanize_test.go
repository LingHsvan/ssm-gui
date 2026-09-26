// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package scores

import (
	"math"
	"math/rand"
	"os"
	"testing"

	"github.com/kvarenzn/ssm/common"
)

func TestMain(m *testing.M) {
	code := m.Run()
	// GenerateTouchEvent writes these debug dumps to the working directory.
	os.Remove("res.json")
	os.Remove("out.json")
	os.Exit(code)
}

func testConfig() *VTEGenerateConfig {
	return &VTEGenerateConfig{
		TapDuration:         10,
		FlickDuration:       60,
		FlickReportInterval: 5,
		FlickFactor:         1.0 / 5,
		FlickPow:            1,
		SlideReportInterval: 10,
	}
}

// mixedChart builds a chart with taps, chords, flicks (narrow and wide),
// throws, drags and slides (including same-tick steps and end flicks).
func mixedChart(seed int64) Chart {
	r := rand.New(rand.NewSource(seed))
	lane := func() float64 { return float64(r.Intn(7)) / 6 }
	var chart Chart
	t := 1.0
	for range 150 + r.Intn(150) {
		t += float64(r.Intn(400)) / 1000
		for range 1 + r.Intn(2) {
			switch k := r.Intn(12); {
			case k < 5:
				chart = append(chart, newStar(t, lane(), 1.0/6).markAsTap())
			case k < 7:
				w := float64(1+r.Intn(4)) / 6
				chart = append(chart, newStar(t, lane(), w).markAsTap().flickToIfOk(true, []int{0, 45, 90, 180}[r.Intn(4)]))
			case k < 8:
				chart = append(chart, newStar(t, lane(), 1.0/6).flickToIfOk(true, 90))
			case k < 9:
				chart = append(chart, newStar(t, lane(), 1.0/6))
			default:
				prev := newStar(t, lane(), 1.0/6).markAsTap().markAsHead()
				st := t
				for range r.Intn(4) {
					if r.Intn(4) != 0 {
						st += float64(1+r.Intn(300)) / 1000
					}
					prev = newStar(st, lane(), 1.0/6).chainsAfter(prev)
				}
				end := newStar(st+float64(20+r.Intn(400))/1000, lane(), 1.0/6).chainsAfter(prev)
				if r.Intn(3) == 0 {
					end.flickToIfOk(true, 90)
				} else {
					end.markAsEnd()
				}
				chart = append(chart, end)
			}
		}
	}
	return chart
}

// checkStrokes fails unless every pointer goes down, optionally moves, and
// comes up again, with no two events of one pointer on the same tick.
func checkStrokes(t *testing.T, events common.RawVirtualEvents) {
	t.Helper()
	down := map[int]bool{}
	lastTick := map[int]int64{}
	for i, item := range events {
		if i > 0 && item.Timestamp <= events[i-1].Timestamp {
			t.Fatalf("ticks not increasing at %d", item.Timestamp)
		}
		for _, e := range item.Events {
			if last, ok := lastTick[e.PointerID]; ok && last == item.Timestamp {
				t.Fatalf("pointer %d has two events at tick %d", e.PointerID, item.Timestamp)
			}
			lastTick[e.PointerID] = item.Timestamp
			switch e.Action {
			case common.TouchDown:
				if down[e.PointerID] {
					t.Fatalf("pointer %d down twice at tick %d", e.PointerID, item.Timestamp)
				}
				down[e.PointerID] = true
			case common.TouchMove, common.TouchUp:
				if !down[e.PointerID] {
					t.Fatalf("pointer %d %v while up at tick %d", e.PointerID, e.Action, item.Timestamp)
				}
				if e.Action == common.TouchUp {
					down[e.PointerID] = false
				}
			}
		}
	}
	for p, d := range down {
		if d {
			t.Fatalf("pointer %d never lifted", p)
		}
	}
}

func TestHumanizedStrokesAreWellFormed(t *testing.T) {
	h := HumanizeConfig{TimingJitter: 60, PositionJitter: 0.1, TapDurJitter: 8, GreatOffsetMs: 30, GreatTargetCount: 50}
	for seed := range int64(100) {
		events, _ := generateHumanized(testConfig(), h, mixedChart(seed), rand.New(rand.NewSource(seed)))
		checkStrokes(t, events)
	}
}

func TestDisabledHumanizeHasNoJitter(t *testing.T) {
	chart := Chart{
		newStar(1.0, 0.5, 1.0/6).markAsTap(),
		newStar(2.0, 0.5, 1.0/6).markAsTap(),
	}
	events, great := GenerateHumanizedTouchEvent(testConfig(), HumanizeConfig{}, chart)
	if great != 0 {
		t.Fatalf("great applied = %d, want 0", great)
	}
	want := []int64{1000, 1010, 2000, 2010}
	if len(events) != len(want) {
		t.Fatalf("got %d ticks, want %d", len(events), len(want))
	}
	for i, item := range events {
		if item.Timestamp != want[i] || item.Events[0].X != 0.5 {
			t.Fatalf("tick %d: got %d x=%v", i, item.Timestamp, item.Events[0].X)
		}
	}
}

// tapsChart has n taps on one lane, far enough apart that jitter never makes
// two notes on the pointer touch.
func tapsChart(n int) Chart {
	var chart Chart
	for i := range n {
		chart = append(chart, newStar(1+float64(i), 0.5, 1.0/6).markAsTap())
	}
	return chart
}

func TestGreatCountIsExactAndSparesFirstTap(t *testing.T) {
	const offset = 20
	for _, target := range []int64{1, 5, 19, 50} {
		events, great := generateHumanized(testConfig(), HumanizeConfig{GreatOffsetMs: offset, GreatTargetCount: target}, tapsChart(20), rand.New(rand.NewSource(target)))
		if want := min(int(target), 19); great != want {
			t.Fatalf("target %d: great applied = %d, want %d", target, great, want)
		}
		early := 0
		for _, item := range events {
			if item.Events[0].Action != common.TouchDown {
				continue
			}
			switch item.Timestamp % 1000 {
			case 0:
			case 1000 - offset:
				early++
			default:
				t.Fatalf("unexpected touch-down tick %d", item.Timestamp)
			}
		}
		if events[0].Timestamp != 1000 {
			t.Fatalf("first tap moved to %d", events[0].Timestamp)
		}
		if early != great {
			t.Fatalf("%d taps early, great applied %d", early, great)
		}
	}
}

func TestJitterStaysWithinBounds(t *testing.T) {
	h := HumanizeConfig{TimingJitter: 30, PositionJitter: 0.05, TapDurJitter: 4}
	events, _ := generateHumanized(testConfig(), h, tapsChart(200), rand.New(rand.NewSource(1)))
	var downAt int64
	for _, item := range events {
		e := item.Events[0]
		switch e.Action {
		case common.TouchDown:
			downAt = item.Timestamp
			nominal := (item.Timestamp + 500) / 1000 * 1000
			if d := item.Timestamp - nominal; d < -30 || d > 30 {
				t.Fatalf("tap at %d is %d ms off", item.Timestamp, d)
			}
			if math.Abs(e.X-0.5) > 0.05 {
				t.Fatalf("tap x %v out of range", e.X)
			}
		case common.TouchUp:
			if d := item.Timestamp - downAt; d < 6 || d > 14 {
				t.Fatalf("hold time %d ms out of range", d)
			}
		}
	}
}

func TestWideFlickStartsFromOppositeEdge(t *testing.T) {
	for _, tc := range []struct {
		deg   int
		wantX float64
	}{
		{0, 0.5 - 0.25},   // rightward: start at the left edge
		{180, 0.5 + 0.25}, // leftward: start at the right edge
		{90, 0.5},         // upward: start at the center
	} {
		chart := Chart{newStar(1, 0.5, 0.5).markAsTap().flickToIfOk(true, tc.deg)}
		events, _ := GenerateHumanizedTouchEvent(testConfig(), HumanizeConfig{}, chart)
		if x := events[0].Events[0].X; math.Abs(x-tc.wantX) > 1e-9 {
			t.Fatalf("flick %d°: starts at %v, want %v", tc.deg, x, tc.wantX)
		}
	}
}

func TestSlideStepsOnSameTickStayOrdered(t *testing.T) {
	head := newStar(1, 0.2, 1.0/6).markAsTap().markAsHead()
	mid := newStar(1, 0.4, 1.0/6).chainsAfter(head)
	mid2 := newStar(1, 0.6, 1.0/6).chainsAfter(mid)
	end := newStar(1.2, 0.8, 1.0/6).chainsAfter(mid2).markAsEnd()
	events, _ := GenerateHumanizedTouchEvent(testConfig(), HumanizeConfig{}, Chart{end})
	checkStrokes(t, events)
}
