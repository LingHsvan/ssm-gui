// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// GUI entry point. main.go stays identical to upstream (kvarenzn/ssm) apart
// from one `if guiRequested() { runGUI(); return }` hook; everything the GUI
// needs in package main lives in the gui_*.go files.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/log"
)

var (
	guiMode bool
	guiPort int
)

func init() {
	flag.BoolVar(&guiMode, "gui", false, "Start the graphical interface (browser GUI)")
	flag.IntVar(&guiPort, "port", 8765, "Port used by the GUI")
}

// guiRequested reports whether to run the GUI: either -gui was given, or the
// program was started without any arguments (e.g. double-clicked).
func guiRequested() bool {
	return guiMode || len(os.Args) == 1
}

const guiConfigPath = "./config.json"

func runGUI() {
	conf, err := loadGUIConfig(guiConfigPath)
	if err != nil {
		log.Die(err)
	}

	p := &player{conf: conf}
	srv := gui.NewServer(guiPort, conf, p)
	p.srv = srv

	addr, err := srv.Start()
	if err != nil {
		log.Die("Failed to start GUI server:", err)
	}
	fmt.Printf("\n  SSM GUI started\n")
	fmt.Printf("   Open this URL in your browser: %s\n\n", addr)

	// Off by default: the URL above is the entry point, and the setting (in
	// Settings, persisted to config.json) is a convenience for users who want
	// the browser to come up on its own.
	if conf.ShouldAutoOpenBrowser() {
		openBrowser(addr)
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, syscall.SIGINT, syscall.SIGTERM)
	<-interrupted
	fmt.Println("\nSSM GUI closed")
}

// loadGUIConfig loads the device config, creating it on first run. Upstream
// config.Load creates an empty file and then fails to parse it, so seed an
// empty JSON object instead.
func loadGUIConfig(path string) (*config.Config, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && info.Size() == 0) {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return config.Load(path)
}
