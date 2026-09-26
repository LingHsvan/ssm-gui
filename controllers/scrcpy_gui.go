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

func (g GUIScrcpy) Send(data []byte) {
	n, err := g.controlSocket.Write(data)
	if err != nil {
		log.Warnf("failed to send control data through control socket: %v", err)
		return
	}

	if n != len(data) {
		log.Warnf("partial control data sent: expect %d bytes, sent %d bytes", len(data), n)
	}
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

	if c.decoder != nil {
		c.decoder.Drop()
		c.decoder = nil
	}

	if c.listener != nil {
		keep(c.listener.Close())
	}
	return firstErr
}

// ResetTouch lifts the first ten pointers so a stopped song never leaves a
// finger held down on the device.
func (c *ScrcpyController) ResetTouch() {
	if c.controlSocket == nil {
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
