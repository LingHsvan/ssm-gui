// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package adb

import (
	"fmt"
	"slices"
	"strings"
)

// PickDevice selects the device to use for playback (the GUI and CLI share
// this code path). serial is the caller-requested serial ("" = no
// preference) and recorded is the set of serials saved in "Device
// Management".
//
// Rules:
//  1. serial != "" and a connected, authorized device has that serial: use
//     it — whether the device is registered is checked by the caller
//     afterwards (conf.Get), so the "not configured" error keeps its exact
//     wording.
//  2. otherwise (disconnected / unauthorized / no preference): auto-select
//     among connected, authorized, registered devices only, so a phone
//     temporarily plugged in for adb debugging is never picked by accident.
//     0 candidates -> error; 1 -> use it; >= 2 -> deterministically pick the
//     lexicographically first serial.
//
// The returned reason is an English, log-ready description of why the device
// was chosen (which device was requested, which one was selected).
func PickDevice(devices []*Device, serial string, recorded map[string]struct{}) (*Device, string, error) {
	serial = strings.TrimSpace(serial)

	// 1. explicit serial.
	requestedState := ""
	if serial != "" {
		for _, d := range devices {
			if d.Serial() != serial {
				continue
			}
			if d.Authorized() {
				return d, fmt.Sprintf("using requested device %q", serial), nil
			}
			requestedState = "not authorized (accept the USB debugging prompt on the phone)"
			break
		}
		if requestedState == "" {
			requestedState = "not connected"
		}
	}

	// 2. auto selection among registered + authorized devices.
	registered := make([]*Device, 0, len(devices))
	ignored := make([]string, 0)
	for _, d := range devices {
		if !d.Authorized() {
			continue
		}
		if _, ok := recorded[d.Serial()]; ok {
			registered = append(registered, d)
		} else {
			ignored = append(ignored, d.Serial())
		}
	}
	slices.SortFunc(registered, func(a, b *Device) int {
		return strings.Compare(a.Serial(), b.Serial())
	})

	if len(registered) == 0 {
		msg := "no registered ADB device found"
		if requestedState != "" {
			msg = fmt.Sprintf("requested device %q is %s; ", serial, requestedState) + msg
		}
		if len(ignored) > 0 {
			msg += fmt.Sprintf(" (connected but not registered, ignored: %s)", strings.Join(ignored, ", "))
		}
		return nil, "", fmt.Errorf("%s; add one via Auto Detect or in Settings first", msg)
	}

	chosen := registered[0]
	reason := fmt.Sprintf("auto-selected device %q", chosen.Serial())
	if requestedState != "" {
		reason = fmt.Sprintf("requested device %q is %s; auto-selected %q", serial, requestedState, chosen.Serial())
	}
	if len(registered) > 1 {
		serials := make([]string, len(registered))
		for i, d := range registered {
			serials[i] = d.Serial()
		}
		reason += fmt.Sprintf(" (first of %d registered devices: %s)", len(registered), strings.Join(serials, ", "))
	}
	return chosen, reason, nil
}

// PickRecordedOrFirstAuthorized prefers a connected, authorized device that
// is already registered in Settings / via Auto Detect; without one it falls
// back to the first authorized device, because discovering brand-new devices
// is exactly what Auto Detect is for. Returns nil when nothing is authorized.
func PickRecordedOrFirstAuthorized(devices []*Device, recorded map[string]struct{}) *Device {
	for _, d := range devices {
		if !d.Authorized() {
			continue
		}
		if _, ok := recorded[d.Serial()]; ok {
			return d
		}
	}
	return FirstAuthorizedDevice(devices)
}
