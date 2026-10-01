// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package adb

import "fmt"

// KillReverseForward removes a reverse forward on this specific device.
//
// Client.KillForward(local, true) goes through the `host:tport:any` transport,
// which fails with "more than one device/emulator" as soon as several devices
// are attached. Opening the device's own transport avoids that.
func (d *Device) KillReverseForward(local string) error {
	conn, err := d.Open()
	if err != nil {
		return err
	}

	defer conn.Close()
	return conn.Run(fmt.Sprintf("reverse:killforward:%s", local))
}
