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

type Config struct {
	Path    string                   `json:"-"`
	Devices map[string]*DeviceConfig `json:"devices"`

	// SongDetectROI persists calibrated normalized [x,y,w,h] crops for the
	// /api/detect-song endpoint, keyed by game mode ("bang"/"pjsk").
	SongDetectROI map[string][4]float64 `json:"songDetectROI,omitempty"`

	// mu guards Devices and the on-disk file. The GUI mutates the device
	// map from HTTP handler goroutines while playback may read it, so all
	// access goes through the locked methods below.
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

// DeleteDevice removes a device entry and persists the config.
func (c *Config) DeleteDevice(serial string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Devices == nil {
		return nil
	}
	delete(c.Devices, serial)
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
