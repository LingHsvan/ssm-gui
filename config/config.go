// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type DeviceConfig struct {
	Serial string `json:"-"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// JitterConfig mirrors the GUI's "Humanization (Jitter)" panel. Values are
// stored in the same units the sliders hold, so restoring them is a plain
// assignment and no scaling can drift.
type JitterConfig struct {
	Timing   int64 `json:"timing"`   // ±ms
	Position int64 `json:"position"` // index into the GUI's position map (0..10)
	TapDur   int64 `json:"tapDur"`   // ±ms
	GrOffset int64 `json:"grOffset"` // ms
	GrCount  int64 `json:"grCount"`  // exact number of Greats, 0 = disabled
}

// AdvancedConfig mirrors the GUI's "Advanced Parameters" panel for one game
// mode. FlickFactor and FlickPow are slider units too (the GUI divides them by
// 100 and 10 respectively), so what is stored is exactly what the sliders show.
type AdvancedConfig struct {
	TapDuration         int64 `json:"tapDuration"`
	FlickDuration       int64 `json:"flickDuration"`
	FlickReportInterval int64 `json:"flickReportInterval"`
	SlideReportInterval int64 `json:"slideReportInterval"`
	FlickLead           int64 `json:"flickLead"`
	FlickFactor         int64 `json:"flickFactor"`
	FlickPow            int64 `json:"flickPow"`
}

type Config struct {
	Path    string                   `json:"-"`
	Devices map[string]*DeviceConfig `json:"devices"`

	// Everything below is this fork's own state. It has to live in the struct
	// because Go cannot extend a struct from another file; the accessors that
	// touch it are in config_gui.go, so this file stays close to upstream.

	// SongDetectROI persists calibrated normalized [x,y,w,h] crops for the
	// /api/detect-song endpoint, keyed by game mode. It is the shared,
	// device-agnostic fallback kept for configs written before the per-device
	// map existed, and the landing bucket when no device could be resolved
	// (e.g. detecting against a local test image).
	SongDetectROI map[string][4]float64 `json:"songDetectROI,omitempty"`

	// SongDetectROIDevice persists the same crops keyed by device serial and
	// then game mode: SongDetectROIDevice[serial][mode]. Phones frame the title
	// bar at different normalized positions, so a calibration only means
	// anything for the device it was measured on.
	SongDetectROIDevice map[string]map[string][4]float64 `json:"songDetectROIDevice,omitempty"`

	// Jitter and Advanced persist the two GUI fine-tuning panels so tuning
	// survives a restart. Advanced is keyed by game mode because each mode has
	// its own defaults (and therefore its own tuning).
	Jitter   *JitterConfig              `json:"jitter,omitempty"`
	Advanced map[string]*AdvancedConfig `json:"advanced,omitempty"`

	// AutoOpenBrowser controls whether starting the GUI also opens the default
	// browser. Off by default — a missing key means off too — so a start stays
	// quiet unless asked otherwise; the console always prints the URL.
	AutoOpenBrowser bool `json:"autoOpenBrowser,omitempty"`
}

func (c *Config) askFor(serial string) *DeviceConfig {
	dc := &DeviceConfig{}
	fmt.Printf("Please provide info for device [%s]\n", serial)
	for dc.Width <= 0 {
		fmt.Print("Device Width (an integer > 0): ")
		fmt.Scanln(&dc.Width)
	}

	for dc.Height <= 0 {
		fmt.Print("Device Height (an integer > 0): ")
		fmt.Scanln(&dc.Height)
	}

	dc.Serial = serial
	return dc
}

func (c *Config) Get(serial string) *DeviceConfig {
	if c.Devices == nil {
		c.Devices = map[string]*DeviceConfig{}
	}

	if dc, ok := c.Devices[serial]; ok {
		dc.Serial = serial
		return dc
	} else {
		dc = c.askFor(serial)
		c.Devices[serial] = dc
		c.Save()
		return dc
	}
}

func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, err := os.Create(path); err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	c := &Config{}
	// A freshly created config is empty; only a non-empty file can be parsed.
	if len(data) > 0 {
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("parse config %q: %w", path, err)
		}
	}
	c.Path = path
	return c, nil
}

func (c *Config) Save() error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(c.Path, data, 0o600)
}
