// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// Thread-safe accessors used by the GUI. The GUI mutates the device map from
// HTTP handler goroutines while playback may read it, so every GUI access goes
// through these methods. Kept out of config.go so that file stays identical to
// upstream.

package config

import "sync"

// guiMu guards Config.Devices and the on-disk file for the GUI accessors.
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

// DeleteDevice removes a device entry and persists the config.
func (c *Config) DeleteDevice(serial string) error {
	guiMu.Lock()
	defer guiMu.Unlock()

	if c.Devices == nil {
		return nil
	}
	delete(c.Devices, serial)
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
