// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

var DisablePrompt bool = false

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

	// SongDetectROI persists calibrated normalized [x,y,w,h] crops for the
	// /api/detect-song endpoint, keyed by game mode ("bang"/"pjsk"). It is the
	// shared, device-agnostic fallback kept for configs written before the
	// per-device map existed, and the landing bucket when no device could be
	// resolved (e.g. detecting against a local test image).
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

	// mu guards Devices, the song-detection ROI maps and the on-disk file. The
	// GUI mutates these from HTTP handler goroutines while playback may read
	// them, so all access goes through the locked methods below.
	mu sync.Mutex `json:"-"`
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Devices == nil {
		c.Devices = map[string]*DeviceConfig{}
	}

	if dc, ok := c.Devices[serial]; ok {
		dc.Serial = serial
		return dc
	}

	if DisablePrompt {
		return nil
	}
	dc := c.askFor(serial)
	c.Devices[serial] = dc
	c.saveLocked()
	return dc
}

// SetDevice stores (or overwrites) a device entry and persists the config.
func (c *Config) SetDevice(serial string, width, height int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Devices == nil {
		c.Devices = map[string]*DeviceConfig{}
	}
	c.Devices[serial] = &DeviceConfig{Serial: serial, Width: width, Height: height}
	return c.saveLocked()
}

// DeleteDevice removes a device entry along with its ROI calibrations and
// persists the config. The ROI is dropped too so removing a phone cannot leave
// an orphan bucket behind for a serial that will never be picked again.
func (c *Config) DeleteDevice(serial string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Devices != nil {
		delete(c.Devices, serial)
	}
	if c.SongDetectROIDevice != nil {
		delete(c.SongDetectROIDevice, serial)
	}
	return c.saveLocked()
}

// Snapshot returns a copy of the device map safe to read without the lock.
func (c *Config) Snapshot() map[string]DeviceConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]DeviceConfig, len(c.Devices))
	for k, v := range c.Devices {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// RecordedSerials returns the set of serials present in the device map, safe
// to read without the lock. Callers use it to tell "registered in Device
// Management" apart from "merely connected".
func (c *Config) RecordedSerials() map[string]struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]struct{}, len(c.Devices))
	for s := range c.Devices {
		out[s] = struct{}{}
	}
	return out
}

// ─────────────────────────────────────────────────────────────
// Song-detection ROI calibration
//
// The ROI is the normalized [x,y,w,h] crop handed to OCR. It is genuinely
// device-specific — the same game on two phones puts the title bar at
// different places — so it is stored per device serial and then per game mode.
// The shared mode-level map above stays as the fallback, so single-device
// setups and configs written before per-device storage keep working unchanged.
// ─────────────────────────────────────────────────────────────

// SongDetectROIFor returns the calibration saved for this exact device and
// mode. An empty serial never matches: it means "no device was resolved".
func (c *Config) SongDetectROIFor(serial, mode string) ([4]float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if serial == "" {
		return [4]float64{}, false
	}
	bucket, ok := c.SongDetectROIDevice[serial]
	if !ok {
		return [4]float64{}, false
	}
	roi, ok := bucket[mode]
	return roi, ok
}

// SongDetectROIMode returns the shared, device-agnostic calibration for a mode.
func (c *Config) SongDetectROIMode(mode string) ([4]float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	roi, ok := c.SongDetectROI[mode]
	return roi, ok
}

// SetSongDetectROI stores a calibration for (serial, mode) and persists the
// config. An empty serial — a local test image, or no device could be picked —
// falls back to the shared mode-level bucket instead of inventing a device.
func (c *Config) SetSongDetectROI(serial, mode string, roi [4]float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if serial == "" {
		if c.SongDetectROI == nil {
			c.SongDetectROI = map[string][4]float64{}
		}
		c.SongDetectROI[mode] = roi
		return c.saveLocked()
	}

	if c.SongDetectROIDevice == nil {
		c.SongDetectROIDevice = map[string]map[string][4]float64{}
	}
	bucket := c.SongDetectROIDevice[serial]
	if bucket == nil {
		bucket = map[string][4]float64{}
		c.SongDetectROIDevice[serial] = bucket
	}
	bucket[mode] = roi
	return c.saveLocked()
}

// ClearDeviceROI drops the calibrations of one device; an empty mode clears
// every mode of that device. Backs the settings page's "reset ROI" action.
func (c *Config) ClearDeviceROI(serial, mode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if serial == "" {
		return nil
	}
	if mode == "" {
		delete(c.SongDetectROIDevice, serial)
	} else if bucket := c.SongDetectROIDevice[serial]; bucket != nil {
		delete(bucket, mode)
		if len(bucket) == 0 {
			delete(c.SongDetectROIDevice, serial)
		}
	}
	return c.saveLocked()
}

// DeviceROIs returns a deep copy of the per-device calibrations, safe to read
// without the lock.
func (c *Config) DeviceROIs() map[string]map[string][4]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]map[string][4]float64, len(c.SongDetectROIDevice))
	for serial, bucket := range c.SongDetectROIDevice {
		if len(bucket) == 0 {
			continue
		}
		cp := make(map[string][4]float64, len(bucket))
		for mode, roi := range bucket {
			cp[mode] = roi
		}
		out[serial] = cp
	}
	return out
}

// SetTuning stores the jitter panel and, when mode is non-empty, that mode's
// advanced parameters, then persists the config.
func (c *Config) SetTuning(jitter *JitterConfig, mode string, advanced *AdvancedConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if jitter != nil {
		j := *jitter
		c.Jitter = &j
	}
	if mode != "" && advanced != nil {
		if c.Advanced == nil {
			c.Advanced = map[string]*AdvancedConfig{}
		}
		a := *advanced
		c.Advanced[mode] = &a
	}
	return c.saveLocked()
}

// Tuning returns copies of the persisted panels, safe to use without the lock.
// The advanced map is keyed by game mode; jitter is nil when never saved.
func (c *Config) Tuning() (*JitterConfig, map[string]AdvancedConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var jitter *JitterConfig
	if c.Jitter != nil {
		j := *c.Jitter
		jitter = &j
	}

	advanced := make(map[string]AdvancedConfig, len(c.Advanced))
	for mode, a := range c.Advanced {
		if a != nil {
			advanced[mode] = *a
		}
	}
	return jitter, advanced
}

// ShouldAutoOpenBrowser reports whether launching the GUI should also open the
// default browser. A config written before this setting existed simply leaves
// the field false, which is the intended default.
func (c *Config) ShouldAutoOpenBrowser() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AutoOpenBrowser
}

// SetAutoOpenBrowser stores the startup preference and persists the config.
func (c *Config) SetAutoOpenBrowser(open bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AutoOpenBrowser = open
	return c.saveLocked()
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
	if len(data) > 0 {
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("parse config %q: %w", path, err)
		}
	}
	c.Path = path
	return c, nil
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

// saveLocked marshals and writes the config. Callers must hold c.mu.
func (c *Config) saveLocked() error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(c.Path, data, 0o600)
}
