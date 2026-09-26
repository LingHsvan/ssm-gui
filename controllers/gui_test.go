// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package controllers

import (
	"testing"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/stage"
)

func touch(ts int64, events ...*common.VirtualTouchEvent) *common.VirtualEventsItem {
	return &common.VirtualEventsItem{Timestamp: ts, Events: events}
}

func ev(p int, a common.TouchAction) *common.VirtualTouchEvent {
	return &common.VirtualTouchEvent{PointerID: p, Action: a, X: 0.5}
}

func TestPackPointerSlotsReusesFreedSlotsAndDropsOverflow(t *testing.T) {
	var raw common.RawVirtualEvents
	// Pointers 100..111 go down together: only ten fit.
	for p := 100; p < 112; p++ {
		raw = append(raw, touch(int64(p), ev(p, common.TouchDown)))
	}
	raw = append(raw,
		touch(200, ev(111, common.TouchMove)), // dropped stroke: ignored
		touch(201, ev(103, common.TouchUp)),   // frees slot 3
		touch(202, ev(111, common.TouchUp)),   // end of the dropped stroke
		touch(203, ev(500, common.TouchDown)), // takes the freed slot 3
	)

	packed := packPointerSlots(raw, 10)
	var got []int
	for _, item := range packed {
		for _, e := range item.Events {
			got = append(got, e.PointerID)
		}
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 3, 3}
	if len(got) != len(want) {
		t.Fatalf("got pointers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got pointers %v, want %v", got, want)
		}
	}
	if len(packed) != len(raw) {
		t.Fatalf("ticks must be kept even when emptied: %d vs %d", len(packed), len(raw))
	}
}

func TestHIDReportsAlwaysCarryAllContacts(t *testing.T) {
	raw := common.RawVirtualEvents{
		touch(0, ev(0, common.TouchDown)),
		touch(10, ev(0, common.TouchUp)),
	}
	h := GUIHID{&HIDController{dc: &config.DeviceConfig{Width: 2400, Height: 1080}}}
	for _, item := range h.PreprocessGUI(raw, false, stage.BanGJudgeLinePos) {
		if len(item.Data) != hidMaxContacts*hidFingerEventSize {
			t.Fatalf("report at %d has %d bytes", item.Timestamp, len(item.Data))
		}
	}
}

func TestSanitizeTouches(t *testing.T) {
	raw := common.RawVirtualEvents{
		touch(0, ev(1, common.TouchMove)), // move without down -> down
		touch(1, ev(1, common.TouchDown)), // repeated down -> move
		touch(2, ev(1, common.TouchUp)),
		touch(3, ev(1, common.TouchUp)), // repeated up -> dropped
	}
	got := sanitizeTouches(raw)
	want := []common.TouchAction{common.TouchDown, common.TouchMove, common.TouchUp}
	var actions []common.TouchAction
	for _, item := range got {
		for _, e := range item.Events {
			actions = append(actions, e.Action)
		}
	}
	if len(actions) != len(want) {
		t.Fatalf("got %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("got %v, want %v", actions, want)
		}
	}
	if raw[0].Events[0].Action != common.TouchMove {
		t.Fatal("input events must not be modified")
	}
}
