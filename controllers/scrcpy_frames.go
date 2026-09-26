// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// Opt-in video decoding and frame capture for the scrcpy backend. Playback
// does not need the video stream, so decoding is skipped unless
// SSM_ENABLE_VIDEO_DECODE=1; the latest decoded frame is then exposed for
// screen analyzers.

package controllers

import (
	"os"
	"sync"
	"time"

	"github.com/kvarenzn/ssm/decoders/av"
	"github.com/kvarenzn/ssm/log"
)

// ScrcpyFrame is a compact grayscale-friendly frame snapshot for analyzers.
// Plane0 typically represents Y/luma for the common YUV formats from scrcpy.
type ScrcpyFrame struct {
	PTS         int64
	Width       int
	Height      int
	PixelFormat int
	Plane0      []byte
	CapturedAt  time.Time
}

type frameCapture struct {
	mu     sync.RWMutex
	latest *ScrcpyFrame
	fn     func(ScrcpyFrame)
}

// setupDecoder creates the video decoder when decoding is enabled; otherwise
// c.decoder stays nil and the video reader discards frame payloads.
func (c *ScrcpyController) setupDecoder() error {
	if os.Getenv("SSM_ENABLE_VIDEO_DECODE") != "1" {
		log.Debugln("Video decode is disabled (set SSM_ENABLE_VIDEO_DECODE=1 to enable).")
		return nil
	}

	decoder, err := av.NewAVDecoder(c.codecID)
	if err != nil {
		return err
	}
	decoder.SetFrameHandler(func(f av.DecodedFrame) {
		frame := ScrcpyFrame{
			PTS:         f.PTS,
			Width:       f.Width,
			Height:      f.Height,
			PixelFormat: f.PixelFormat,
			Plane0:      append([]byte(nil), f.Plane0...),
			CapturedAt:  time.Now(),
		}

		c.frames.mu.Lock()
		c.frames.latest = &frame
		fn := c.frames.fn
		c.frames.mu.Unlock()

		if fn != nil {
			fn(frame)
		}
	})
	c.decoder = decoder
	return nil
}

func (c *ScrcpyController) SetFrameHandler(fn func(ScrcpyFrame)) {
	c.frames.mu.Lock()
	c.frames.fn = fn
	c.frames.mu.Unlock()
}

func (c *ScrcpyController) LatestFrame() (ScrcpyFrame, bool) {
	c.frames.mu.RLock()
	defer c.frames.mu.RUnlock()
	if c.frames.latest == nil {
		return ScrcpyFrame{}, false
	}
	f := *c.frames.latest
	f.Plane0 = append([]byte(nil), c.frames.latest.Plane0...)
	return f, true
}
