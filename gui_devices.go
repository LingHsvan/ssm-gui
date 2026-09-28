// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

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
func openController(conf *config.Config, req gui.RunRequest, pjsk bool, raw common.RawVirtualEvents) (playController, []common.ViscousEventItem, error) {
	turnRight := req.Orient == "right"
	switch req.Backend {
	case "adb":
		return openADB(conf, req.DeviceSerial, raw, turnRight, judgeLine(pjsk))
	case "hid":
		return openHID(conf, req.DeviceSerial, raw, turnRight, judgeLine(pjsk))
	default:
		return nil, nil, fmt.Errorf("unknown backend: %q", req.Backend)
	}
}

func openADB(conf *config.Config, serial string, raw common.RawVirtualEvents, turnRight bool, judge stage.JudgeLinePositionCalculator) (playController, []common.ViscousEventItem, error) {
	checkOrDownload()
	device, err := findADBDevice(serial)
	if err != nil {
		return nil, nil, err
	}

	scrcpy := controllers.GUIScrcpy{ScrcpyController: controllers.NewScrcpyController(device)}
	if err := scrcpy.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
		scrcpy.Close()
		return nil, nil, fmt.Errorf("failed to connect to device: %w", err)
	}
	dc := conf.Lookup(device.Serial())
	if dc == nil {
		scrcpy.Close()
		return nil, nil, errNotConfigured(device.Serial())
	}
	return scrcpy, scrcpy.PreprocessGUI(raw, turnRight, dc, judge), nil
}

// findADBDevice returns the device with serial, or the first authorized
// device when serial is empty.
func findADBDevice(serial string) (*adb.Device, error) {
	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		return nil, fmt.Errorf("failed to start ADB server: %w", err)
	}
	devices, err := adb.NewDefaultClient().Devices()
	if err != nil {
		return nil, fmt.Errorf("failed to list ADB devices: %w", err)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("%s", errNoDevice)
	}
	log.Debugln("ADB devices:", devices)

	if serial == "" {
		device := adb.FirstAuthorizedDevice(devices)
		if device == nil {
			return nil, fmt.Errorf("no authorized ADB device found")
		}
		return device, nil
	}
	for _, d := range devices {
		if d.Serial() == serial {
			if !d.Authorized() {
				return nil, fmt.Errorf("device %q is not authorized", serial)
			}
			return d, nil
		}
	}
	return nil, fmt.Errorf("no device has serial %q", serial)
}

func openHID(conf *config.Config, serial string, raw common.RawVirtualEvents, turnRight bool, judge stage.JudgeLinePositionCalculator) (playController, []common.ViscousEventItem, error) {
	if serial == "" {
		serials := controllers.FindHIDDevices()
		log.Debugln("Recognized devices:", serials)
		if len(serials) == 0 {
			return nil, nil, fmt.Errorf("%s", errNoDevice)
		}
		serial = serials[0]
	}
	dc := conf.Lookup(serial)
	if dc == nil {
		return nil, nil, errNotConfigured(serial)
	}
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
// getJudgeLineCalculator, but without reading the CLI's global flags.
func judgeLine(pjsk bool) stage.JudgeLinePositionCalculator {
	if pjsk {
		return stage.PJSKJudgeLinePos
	}
	return stage.BanGJudgeLinePos
}
