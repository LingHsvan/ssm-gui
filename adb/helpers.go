// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

var (
	ErrADBServerRunning    = errors.New("adb server already running")
	ErrADBServerNotRunning = errors.New("adb server not running")
)

// adbServerStartTimeout bounds each `adb start-server` invocation so a
// wedged adb executable cannot block the caller forever.
const adbServerStartTimeout = 30 * time.Second

func IsDefaultHostAndPort(host string, port int) bool {
	return host == ADBDefaultServerHost && port == ADBDefaultServerPort
}

func IsADBServerRunning(host string, port int) bool {
	client := NewClient(host, port)
	return client.Ping() == nil
}

// is path exists and is a file?
func FileExists(path string) bool {
	if stat, err := os.Stat(path); err != nil {
		return false
	} else {
		return !stat.IsDir()
	}
}

func StartADBServer(host string, port int) error {
	if IsADBServerRunning(host, port) {
		return ErrADBServerRunning
	}

	args := []string{}
	if !IsDefaultHostAndPort(host, port) {
		args = append(args, "-H", host, "-L", fmt.Sprintf("%d", port))
	}

	args = append(args, "start-server")

	adbExecutable := "adb"
	if runtime.GOOS == "windows" {
		adbExecutable = "adb.exe"
	}

	// try local adb executable first
	localExecutable := "./" + adbExecutable
	if FileExists(localExecutable) {
		ctx, cancel := context.WithTimeout(context.Background(), adbServerStartTimeout)
		cmd := exec.CommandContext(ctx, localExecutable, args...)
		err := cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), adbServerStartTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbExecutable, args...)
	return cmd.Run()
}

func StopADBServer(host string, port int) error {
	if !IsADBServerRunning(host, port) {
		return ErrADBServerNotRunning
	}

	client := NewClient(host, port)
	return client.KillServer()
}

func RestartADBServer(host string, port int) error {
	if err := StopADBServer(host, port); err != nil && err != ErrADBServerNotRunning {
		return err
	}

	return StartADBServer(host, port)
}
