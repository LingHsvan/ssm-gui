// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// GUI-side behavior for the scrcpy (adb) backend, kept out of scrcpy.go so that
// file stays close to upstream. Upstream's methods log.Fatal on I/O errors,
// which would take the whole GUI down; the GUI uses these variants instead.

package controllers

import (
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/stage"
)

// GUIScrcpy is a ScrcpyController whose Send never terminates the program and
// whose Close releases every resource even if one of them fails to close.
type GUIScrcpy struct {
	*ScrcpyController
}

// Send forwards to ScrcpyController.Send. This fork already made that method
// non-fatal (it warns, latches sendBroken and drops later events), so the GUI
// keeps its "one warning per dead session" behaviour and Broken() stays
// meaningful for the playback loop.
func (g GUIScrcpy) Send(data []byte) {
	g.ScrcpyController.Send(data)
}

func (g GUIScrcpy) Close() error {
	c := g.ScrcpyController
	c.mu.Lock()
	c.cRunning = false
	c.vRunning = false
	c.mu.Unlock()

	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if c.videoSocket != nil {
		keep(c.videoSocket.Close())
	}
	if c.controlSocket != nil {
		keep(c.controlSocket.Close())
	}

	// Drop is idempotent and makes later Decode calls no-ops. The pointer is
	// kept: the video reader goroutine may still be about to use it.
	if c.decoder != nil {
		c.decoder.Drop()
	}

	if c.listener != nil {
		keep(c.listener.Close())
	}
	return firstErr
}

// normalizeTouchAction folds the pointer-related aliases of the three basic
// touch actions into them. Upstream dropped this helper when it inlined the
// switch into Preprocess; this fork keeps Preprocess tolerant (a duplicated
// down must not kill the GUI), so the helper stays too -- it just lives here
// instead of in common/events.go.
func normalizeTouchAction(action common.TouchAction) (common.TouchAction, bool) {
	switch action {
	case common.TouchDown, common.TouchPointerDown:
		return common.TouchDown, true
	case common.TouchUp, common.TouchCancel, common.TouchOutside, common.TouchPointerUp:
		return common.TouchUp, true
	case common.TouchMove, common.TouchHoverMove:
		return common.TouchMove, true
	default:
		return 0, false
	}
}

// ResetTouch lifts the first ten pointers so a stopped song never leaves a
// finger held down on the device. A latched sendBroken means the control
// socket is already gone, so there is nothing to lift.
func (c *ScrcpyController) ResetTouch() {
	if c.controlSocket == nil || c.sendBroken.Load() {
		return
	}
	for i := range 10 {
		c.controlSocket.Write(c.Encode(common.TouchUp, 0, 0, uint64(i)))
	}
}

// PreprocessGUI is Preprocess made tolerant of inconsistent pointer sequences:
// a repeated down becomes a move, a move without a down becomes a down, and a
// repeated up is dropped, instead of aborting playback.
func (c *ScrcpyController) PreprocessGUI(rawEvents common.RawVirtualEvents, turnRight bool, dc *config.DeviceConfig, calc stage.JudgeLinePositionCalculator) []common.ViscousEventItem {
	return c.Preprocess(sanitizeTouches(rawEvents), turnRight, dc, calc)
}

func sanitizeTouches(rawEvents common.RawVirtualEvents) common.RawVirtualEvents {
	onScreen := map[int]bool{}
	result := make(common.RawVirtualEvents, 0, len(rawEvents))
	for _, item := range rawEvents {
		events := make([]*common.VirtualTouchEvent, 0, len(item.Events))
		for _, event := range item.Events {
			e := *event
			switch e.Action {
			case common.TouchDown:
				if onScreen[e.PointerID] {
					log.Warnf("pointer `%d` duplicated down; convert to move", e.PointerID)
					e.Action = common.TouchMove
				} else {
					onScreen[e.PointerID] = true
				}
			case common.TouchMove:
				if !onScreen[e.PointerID] {
					log.Warnf("pointer `%d` move without down; convert to down", e.PointerID)
					e.Action = common.TouchDown
					onScreen[e.PointerID] = true
				}
			case common.TouchUp:
				if !onScreen[e.PointerID] {
					log.Warnf("pointer `%d` duplicated up; ignore", e.PointerID)
					continue
				}
				delete(onScreen, e.PointerID)
			}
			events = append(events, &e)
		}
		result = append(result, &common.VirtualEventsItem{
			Timestamp: item.Timestamp,
			Events:    events,
		})
	}
	return result
}
