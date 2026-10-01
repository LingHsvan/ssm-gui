// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/stage"
)

// playController is a playback backend that can be shut down.
type playController interface {
	controllers.Controller
	Close() error
}

// openController opens the device req asks for (adb or hid) and converts raw
// into touch events for it. The caller must Close the controller.
func openController(conf *config.Config, req gui.RunRequest, mode string, raw common.RawVirtualEvents) (playController, []common.ViscousEventItem, error) {
	turnRight := req.Orient == "right"
	switch req.Backend {
	case "adb":
		return openADB(conf, req.DeviceSerial, raw, turnRight, judgeLine(mode))
	case "hid":
		return openHID(conf, req.DeviceSerial, raw, turnRight, judgeLine(mode))
	default:
		return nil, nil, fmt.Errorf("unknown backend: %q", req.Backend)
	}
}

func openADB(conf *config.Config, serial string, raw common.RawVirtualEvents, turnRight bool, judge stage.JudgeLinePositionCalculator) (playController, []common.ViscousEventItem, error) {
	checkOrDownload()
	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		return nil, nil, fmt.Errorf("failed to start ADB server: %w", err)
	}
	devices, err := adb.NewDefaultClient().Devices()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list ADB devices: %w", err)
	}
	if len(devices) == 0 {
		return nil, nil, fmt.Errorf("%s", errNoDevice)
	}
	log.Debugln("ADB devices:", devices)

	// The requested serial wins while it is connected (whether it is
	// registered is checked by conf.Lookup below); when it is gone -- or none
	// was requested -- a registered + connected device is auto-selected, so
	// swapping devices temporarily needs no config edit. Devices not
	// registered in Device Management are ignored.
	device, pickInfo, err := adb.PickDevice(devices, serial, conf.RecordedSerials())
	if err != nil {
		return nil, nil, err
	}
	log.Infof("[ADB] %s", pickInfo)
	log.Debugln("Selected device:", device)

	// A plain *ScrcpyController: its Send is already non-fatal and latches a
	// broken control socket, and the screen-detection code type-asserts the
	// concrete type to reuse a live session.
	scrcpy := controllers.NewScrcpyController(device)
	if err := scrcpy.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
		scrcpy.Close()
		return nil, nil, fmt.Errorf("failed to connect to device: %w", err)
	}
	dc := conf.Lookup(device.Serial())
	if dc == nil {
		scrcpy.Close()
		return nil, nil, errNotConfigured(device.Serial())
	}
	return scrcpy, scrcpy.Preprocess(raw, turnRight, dc, judge), nil
}

func openHID(conf *config.Config, serial string, raw common.RawVirtualEvents, turnRight bool, judge stage.JudgeLinePositionCalculator) (playController, []common.ViscousEventItem, error) {
	serials := controllers.FindHIDDevices()
	log.Debugln("Recognized devices:", serials)
	if len(serials) == 0 {
		return nil, nil, fmt.Errorf("%s", errNoDevice)
	}

	// Same selection semantics as the adb branch: the requested serial is
	// kept when present (an unregistered one still fails in conf.Lookup
	// below); when it is gone -- or none was requested -- fall back to a
	// registered + present device, deterministically.
	if serial == "" || !slices.Contains(serials, serial) {
		recorded := conf.RecordedSerials()
		candidates := make([]string, 0, len(serials))
		for _, s := range serials {
			if _, ok := recorded[s]; ok {
				candidates = append(candidates, s)
			}
		}
		if len(candidates) == 0 {
			return nil, nil, fmt.Errorf("no registered HID device found (connected: %s); add one via Auto Detect or in Settings first", strings.Join(serials, ", "))
		}
		slices.Sort(candidates)
		if serial == "" {
			log.Infof("[HID] auto-selected device %q among registered devices %v", candidates[0], candidates)
		} else {
			log.Infof("[HID] requested device %q is not connected; auto-selected %q among registered devices %v", serial, candidates[0], candidates)
		}
		serial = candidates[0]
	}

	dc := conf.Lookup(serial)
	if dc == nil {
		return nil, nil, errNotConfigured(serial)
	}
	// GUIHID, not HIDController: the latter's Send is log.Fatal on a USB
	// error, which would take the whole GUI down.
	hid, err := controllers.OpenGUIHID(dc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize HID: %w", err)
	}
	return hid, hid.PreprocessGUI(raw, turnRight, judge), nil
}

func errNotConfigured(serial string) error {
	return fmt.Errorf("device [%s] not configured. Please add it in Settings first", serial)
}

// judgeLine returns the judge line position for the game mode. Like upstream's
// getJudgeLineCalculator, but driven by the requested mode instead of the
// CLI's global flag.
func judgeLine(mode string) stage.JudgeLinePositionCalculator {
	switch mode {
	case common.ModePjsk:
		return stage.PJSKJudgeLinePos
	case common.ModeOurNotes:
		return stage.OurNotesJudgeLinePos
	default:
		return stage.BanGJudgeLinePos
	}
}
