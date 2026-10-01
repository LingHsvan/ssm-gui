// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// Thread-safe accessors used by the GUI. The GUI mutates the device map from
// HTTP handler goroutines while playback may read it, so every GUI access goes
// through these methods. Kept out of config.go so that file stays identical to
// upstream.

package config

import "sync"

// guiMu guards Config.Devices, the song-detection ROI maps and the on-disk
// file for the GUI accessors.
var guiMu sync.Mutex

// Lookup returns the stored config for serial, or nil if the device has not
// been configured. Unlike Get it never prompts on stdin.
func (c *Config) Lookup(serial string) *DeviceConfig {
	guiMu.Lock()
	defer guiMu.Unlock()

	dc, ok := c.Devices[serial]
	if !ok || dc == nil {
		return nil
	}
	dc.Serial = serial
	return dc
}

// SetDevice stores (or overwrites) a device entry and persists the config.
func (c *Config) SetDevice(serial string, width, height int) error {
	guiMu.Lock()
	defer guiMu.Unlock()

	if c.Devices == nil {
		c.Devices = map[string]*DeviceConfig{}
	}
	c.Devices[serial] = &DeviceConfig{Serial: serial, Width: width, Height: height}
	return c.Save()
}

// DeleteDevice removes a device entry along with its ROI calibrations and
// persists the config. The ROI is dropped too so removing a phone cannot leave
// an orphan bucket behind for a serial that will never be picked again.
func (c *Config) DeleteDevice(serial string) error {
	guiMu.Lock()
	defer guiMu.Unlock()

	if c.Devices != nil {
		delete(c.Devices, serial)
	}
	if c.SongDetectROIDevice != nil {
		delete(c.SongDetectROIDevice, serial)
	}
	return c.Save()
}

// Snapshot returns a copy of the device map that is safe to read without the lock.
func (c *Config) Snapshot() map[string]DeviceConfig {
	guiMu.Lock()
	defer guiMu.Unlock()

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
	guiMu.Lock()
	defer guiMu.Unlock()

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
// The shared mode-level map stays as the fallback, so single-device setups and
// configs written before per-device storage keep working unchanged.
// ─────────────────────────────────────────────────────────────

// SongDetectROIFor returns the calibration saved for this exact device and
// mode. An empty serial never matches: it means "no device was resolved".
func (c *Config) SongDetectROIFor(serial, mode string) ([4]float64, bool) {
	guiMu.Lock()
	defer guiMu.Unlock()

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
	guiMu.Lock()
	defer guiMu.Unlock()

	roi, ok := c.SongDetectROI[mode]
	return roi, ok
}

// SetSongDetectROI stores a calibration for (serial, mode) and persists the
// config. An empty serial — a local test image, or no device could be picked —
// falls back to the shared mode-level bucket instead of inventing a device.
func (c *Config) SetSongDetectROI(serial, mode string, roi [4]float64) error {
	guiMu.Lock()
	defer guiMu.Unlock()

	if serial == "" {
		if c.SongDetectROI == nil {
			c.SongDetectROI = map[string][4]float64{}
		}
		c.SongDetectROI[mode] = roi
		return c.Save()
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
	return c.Save()
}

// ClearDeviceROI drops the calibrations of one device; an empty mode clears
// every mode of that device. Backs the settings page's "reset ROI" action.
func (c *Config) ClearDeviceROI(serial, mode string) error {
	guiMu.Lock()
	defer guiMu.Unlock()

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
	return c.Save()
}

// DeviceROIs returns a deep copy of the per-device calibrations, safe to read
// without the lock.
func (c *Config) DeviceROIs() map[string]map[string][4]float64 {
	guiMu.Lock()
	defer guiMu.Unlock()

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
	guiMu.Lock()
	defer guiMu.Unlock()

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
	return c.Save()
}

// Tuning returns copies of the persisted panels, safe to use without the lock.
// The advanced map is keyed by game mode; jitter is nil when never saved.
func (c *Config) Tuning() (*JitterConfig, map[string]AdvancedConfig) {
	guiMu.Lock()
	defer guiMu.Unlock()

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
	guiMu.Lock()
	defer guiMu.Unlock()

	return c.AutoOpenBrowser
}

// SetAutoOpenBrowser stores the startup preference and persists the config.
func (c *Config) SetAutoOpenBrowser(open bool) error {
	guiMu.Lock()
	defer guiMu.Unlock()

	c.AutoOpenBrowser = open
	return c.Save()
}
