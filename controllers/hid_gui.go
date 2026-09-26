// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// GUI-side behavior for the HID (USB AOA) backend, kept out of hid.go so that
// file stays identical to upstream. Upstream's USB helpers log.Fatal on error,
// which would take the whole GUI down; the GUI uses these variants instead.

package controllers

import (
	"fmt"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/stage"
)

// hidMaxContacts is the number of contacts declared by the report descriptor.
const hidMaxContacts = 10

// AOA vendor requests (see NewHIDController for the descriptor layout).
const (
	aoaRegisterHID      = 54
	aoaUnregisterHID    = 55
	aoaSetHIDReportDesc = 56
	aoaSendHIDEvent     = 57
)

// GUIHID is a HIDController whose USB errors are returned or logged instead of
// terminating the program.
type GUIHID struct {
	*HIDController
}

func (c *HIDController) aoaRequest(request uint8, index uint16, data []byte) error {
	_, err := c.device.Control(
		64, // ENDPOINT_OUT | REQUEST_TYPE_VENDOR
		request,
		ACCESSORY_ID,
		index,
		data,
	)
	return err
}

// OpenGUIHID finds the device for dc and registers the virtual touch screen.
func OpenGUIHID(dc *config.DeviceConfig) (GUIHID, error) {
	c, err := NewHIDController(dc)
	if err != nil {
		return GUIHID{}, err
	}
	h := GUIHID{c}

	if err := c.aoaRequest(aoaRegisterHID, uint16(len(c.reportDescription)), nil); err != nil {
		h.release()
		return GUIHID{}, fmt.Errorf("libusb register HID failed: %w", err)
	}
	if err := c.aoaRequest(aoaSetHIDReportDesc, 0, c.reportDescription); err != nil {
		h.Close()
		return GUIHID{}, fmt.Errorf("libusb set HID report descriptor failed: %w", err)
	}
	return h, nil
}

func (h GUIHID) Send(data []byte) {
	if err := h.aoaRequest(aoaSendHIDEvent, 0, data); err != nil {
		log.Warnf("failed to send HID event: %v", err)
	}
}

// Close unregisters the virtual touch screen (best effort: the device may
// already be gone) and releases the USB handles.
func (h GUIHID) Close() error {
	if err := h.aoaRequest(aoaUnregisterHID, 0, nil); err != nil {
		log.Warnf("failed to unregister HID: %v", err)
	}
	return h.release()
}

func (h GUIHID) release() error {
	if err := h.device.Close(); err != nil {
		return err
	}
	return h.usbContext.Close()
}

// PreprocessGUI is Preprocess for any number of pointers: pointer ids are
// packed into the descriptor's ten contact slots (strokes beyond ten
// simultaneous pointers are dropped), and every report carries all ten
// contacts, as the descriptor declares.
func (h GUIHID) PreprocessGUI(rawEvents common.RawVirtualEvents, turnRight bool, calc stage.JudgeLinePositionCalculator) []common.ViscousEventItem {
	result := h.Preprocess(packPointerSlots(rawEvents, hidMaxContacts), turnRight, calc)
	for i := range result {
		for slot := len(result[i].Data) / hidFingerEventSize; slot < hidMaxContacts; slot++ {
			result[i].Data = append(result[i].Data, fingerEvent(slot, false, 0, 0)...)
		}
	}
	return result
}

// hidFingerEventSize is the length of one contact in a report (see fingerEvent).
const hidFingerEventSize = 5

// packPointerSlots renumbers pointers to the lowest free slot below maxSlots.
// A stroke that starts while every slot is busy is dropped as a whole, and
// moves or ups for pointers that are not down are ignored.
func packPointerSlots(rawEvents common.RawVirtualEvents, maxSlots int) common.RawVirtualEvents {
	slotOf := map[int]int{}
	busy := make([]bool, maxSlots)
	dropped := map[int]bool{}
	warned := false

	freeSlot := func() int {
		for i, b := range busy {
			if !b {
				return i
			}
		}
		return -1
	}

	result := make(common.RawVirtualEvents, 0, len(rawEvents))
	for _, item := range rawEvents {
		events := make([]*common.VirtualTouchEvent, 0, len(item.Events))
		for _, event := range item.Events {
			e := *event
			slot, mapped := slotOf[e.PointerID]
			switch e.Action {
			case common.TouchDown:
				if !mapped {
					slot = freeSlot()
					if slot == -1 {
						dropped[e.PointerID] = true
						if !warned {
							warned = true
							log.Warn("HID backend supports at most 10 simultaneous pointers; extra pointers will be dropped")
						}
						continue
					}
					slotOf[e.PointerID] = slot
					busy[slot] = true
				}
			case common.TouchMove:
				if dropped[e.PointerID] || !mapped {
					continue
				}
			case common.TouchUp:
				if dropped[e.PointerID] {
					delete(dropped, e.PointerID)
					continue
				}
				if !mapped {
					continue
				}
				busy[slot] = false
				delete(slotOf, e.PointerID)
			}
			if mapped || e.Action == common.TouchDown {
				e.PointerID = slot
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
