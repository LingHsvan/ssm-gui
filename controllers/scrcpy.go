// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package controllers

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/decoders/av"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/stage"
)

type ScrcpyController struct {
	device    *adb.Device
	sessionID string

	listener      net.Listener
	videoSocket   net.Conn
	controlSocket net.Conn

	width    int
	height   int
	codecID  string
	decoder  *av.AVDecoder
	cRunning atomic.Bool
	vRunning atomic.Bool

	frameMu     sync.RWMutex
	latestFrame *ScrcpyFrame
	frameFn     func(ScrcpyFrame)
}

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

func NewScrcpyController(device *adb.Device) *ScrcpyController {
	return &ScrcpyController{
		device:    device,
		sessionID: fmt.Sprintf("%08x", rand.Int31()),
	}
}

func tryListen(host string, port int) (net.Listener, int) {
	for {
		addr := fmt.Sprintf("%s:%d", host, port)
		listen, err := net.Listen("tcp", addr)
		if err == nil {
			return listen, port
		}

		port++
	}
}

func readFull(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	return err
}

const testFromPort = 27188

func (c *ScrcpyController) Open(filepath string, version string) error {
	listener, port := tryListen("localhost", testFromPort)
	c.listener = listener
	log.Debugf("Listening at localhost:%d", port)

	localName := fmt.Sprintf("localabstract:scrcpy_%s", c.sessionID)
	err := c.device.Forward(localName, fmt.Sprintf("tcp:%d", port), true, false)
	if err != nil {
		return err
	}
	log.Debugf("ADB reverse socket `%s` created.", localName)

	f, err := os.Open(filepath)
	if err != nil {
		return err
	}

	log.Debugln("`scrcpy-server` loaded.")

	if err := c.device.Push(f, "/data/local/tmp/scrcpy-server.jar"); err != nil {
		return err
	}

	log.Debugln("`scrcpy-server` pushed to gaming device.")

	go func() {
		result, err := c.device.Sh(
			"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
			"app_process",
			"/",
			"com.genymobile.scrcpy.Server",
			version,
			fmt.Sprintf("scid=%s", c.sessionID), // session id
			"log_level=warn",                    // log level
			"audio=false",                       // disable audio sync
			"clipboard_autosync=false",          // disable clipboard
			"video_bit_rate=100000",             // Use a very low bitrate to reduce video bandwidth usage
		)
		if err != nil {
			log.Warnf("failed to start `scrcpy-server`: %v", err)
			return
		}

		log.Debugln(result)
	}()

	videoSocket, err := listener.Accept()
	if err != nil {
		return err
	}
	c.videoSocket = videoSocket

	log.Debugln("Video socket accepted.")

	controlSocket, err := listener.Accept()
	if err != nil {
		return err
	}
	c.controlSocket = controlSocket

	log.Debugln("Control socket accepted.")

	// Wait for scrcpy-server to finish initialization on the device
	time.Sleep(500 * time.Millisecond)

	err = c.device.KillReverseForward(localName)
	if err != nil {
		return err
	}

	log.Debugf("ADB reverse socket `%s` removed.", localName)

	deviceName := make([]byte, 64)
	if err := readFull(videoSocket, deviceName); err != nil {
		return err
	}

	buf := make([]byte, 4)
	if err := readFull(videoSocket, buf); err != nil {
		return err
	}
	c.codecID = string(buf)
	if os.Getenv("SSM_ENABLE_VIDEO_DECODE") == "1" {
		c.decoder, err = av.NewAVDecoder(c.codecID)
		if err != nil {
			return err
		}
		c.decoder.SetFrameHandler(func(f av.DecodedFrame) {
			frame := ScrcpyFrame{
				PTS:         f.PTS,
				Width:       f.Width,
				Height:      f.Height,
				PixelFormat: f.PixelFormat,
				Plane0:      append([]byte(nil), f.Plane0...),
				CapturedAt:  time.Now(),
			}

			c.frameMu.Lock()
			c.latestFrame = &frame
			fn := c.frameFn
			c.frameMu.Unlock()

			if fn != nil {
				fn(frame)
			}
		})
	} else {
		log.Debugln("Video decode is disabled (set SSM_ENABLE_VIDEO_DECODE=1 to enable).")
	}

	if err := readFull(videoSocket, buf); err != nil {
		return err
	}
	c.width = int(binary.BigEndian.Uint32(buf))

	if err := readFull(videoSocket, buf); err != nil {
		return err
	}
	c.height = int(binary.BigEndian.Uint32(buf))

	c.cRunning.Store(true)
	c.vRunning.Store(true)

	go func() {
		msgTypeBuf := make([]byte, 1)
		sizeBuf := make([]byte, 4)
		for c.cRunning.Load() {
			if err := readFull(controlSocket, msgTypeBuf); err != nil {
				break
			}

			if err := readFull(controlSocket, sizeBuf); err != nil {
				break
			}

			size := binary.BigEndian.Uint32(sizeBuf)
			bodyBuf := make([]byte, size)
			if err := readFull(controlSocket, bodyBuf); err != nil {
				break
			}
		}

		c.cRunning.Store(false)
	}()

	go func() {
		ptsBuf := make([]byte, 8)
		sizeBuf := make([]byte, 4)
		for c.vRunning.Load() {
			if err := readFull(videoSocket, ptsBuf); err != nil {
				break
			}
			pts := binary.BigEndian.Uint64(ptsBuf)

			if err := readFull(videoSocket, sizeBuf); err != nil {
				break
			}
			size := binary.BigEndian.Uint32(sizeBuf)

			if c.decoder == nil {
				// No decoding needed, discard directly
				io.CopyN(io.Discard, videoSocket, int64(size))
				continue
			}

			data := make([]byte, size)
			if err := readFull(videoSocket, data); err != nil {
				break
			}
			c.decoder.Decode(pts, data)
		}
		c.vRunning.Store(false)
	}()

	return nil
}

func (c *ScrcpyController) Encode(action common.TouchAction, x, y int32, pointerID uint64) []byte {
	data := make([]byte, 32)
	data[0] = 2 // type: SC_CONTROL_MSG_TYPE_INJECT_TOUCH_EVENT
	data[1] = byte(action)
	binary.BigEndian.PutUint64(data[2:], pointerID)
	binary.BigEndian.PutUint32(data[10:], uint32(x))
	binary.BigEndian.PutUint32(data[14:], uint32(y))
	binary.BigEndian.PutUint16(data[18:], uint16(c.width))
	binary.BigEndian.PutUint16(data[20:], uint16(c.height))
	binary.BigEndian.PutUint16(data[22:], 0xffff)
	binary.BigEndian.PutUint32(data[24:], 1) // AMOTION_EVENT_BUTTON_PRIMARY
	binary.BigEndian.PutUint32(data[28:], 1) // AMOTION_EVENT_BUTTON_PRIMARY
	return data
}

func (c *ScrcpyController) touch(action common.TouchAction, x, y int32, pointerID uint64) {
	c.Send(c.Encode(action, x, y, pointerID))
}

func (c *ScrcpyController) Down(pointerID uint64, x, y int) {
	c.touch(common.TouchDown, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Move(pointerID uint64, x, y int) {
	c.touch(common.TouchMove, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Up(pointerID uint64, x, y int) {
	c.touch(common.TouchUp, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Close() error {
	c.cRunning.Store(false)
	c.vRunning.Store(false)

	// Close every resource even if an earlier one errors, so a failed
	// videoSocket.Close() can no longer leak the control socket and listener.
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	keep(c.videoSocket.Close())
	keep(c.controlSocket.Close())

	if c.decoder != nil {
		c.decoder.Drop()
		c.decoder = nil
	}

	keep(c.listener.Close())
	return firstErr
}

func (c *ScrcpyController) SetFrameHandler(fn func(ScrcpyFrame)) {
	c.frameMu.Lock()
	c.frameFn = fn
	c.frameMu.Unlock()
}

func (c *ScrcpyController) LatestFrame() (ScrcpyFrame, bool) {
	c.frameMu.RLock()
	defer c.frameMu.RUnlock()
	if c.latestFrame == nil {
		return ScrcpyFrame{}, false
	}
	f := *c.latestFrame
	f.Plane0 = append([]byte(nil), c.latestFrame.Plane0...)
	return f, true
}

func (c *ScrcpyController) Preprocess(rawEvents common.RawVirtualEvents, turnRight bool, dc *config.DeviceConfig, calc stage.JudgeLinePositionCalculator) []common.ViscousEventItem {
	width, height := float64(dc.Height), float64(dc.Width)
	x1, x2, yy := calc(width, height)
	mapper := func(x, y float64) (int, int) {
		return int(math.Round(x1 + (x2-x1)*x)), int(math.Round(yy - (yy-height/2)*y))
	}

	result := []common.ViscousEventItem{}
	currentFingers := map[int]bool{}
	for _, events := range rawEvents {
		var data []byte
		for _, event := range events.Events {
			if event.PointerID < 0 {
				log.Fatalf("invalid pointer id: %d", event.PointerID)
			}
			x, y := mapper(event.X, event.Y)
			action, ok := common.NormalizeTouchAction(event.Action)
			if !ok {
				log.Fatalf("unknown touch action: %d\n", event.Action)
			}
			switch action {
			case common.TouchDown:
				if currentFingers[event.PointerID] {
					// Be tolerant to occasional duplicated down events to avoid hard crash.
					log.Warnf("pointer `%d` duplicated down; convert to move", event.PointerID)
					action = common.TouchMove
				} else {
					currentFingers[event.PointerID] = true
				}
			case common.TouchMove:
				if !currentFingers[event.PointerID] {
					// Recover by treating stray move as a down.
					log.Warnf("pointer `%d` move without down; convert to down", event.PointerID)
					action = common.TouchDown
					currentFingers[event.PointerID] = true
				}
			case common.TouchUp:
				if !currentFingers[event.PointerID] {
					// Ignore duplicated up events.
					log.Warnf("pointer `%d` duplicated up; ignore", event.PointerID)
					continue
				}
				delete(currentFingers, event.PointerID)
			}

			data = append(data, c.Encode(action, int32(x), int32(y), uint64(event.PointerID))...)
		}

		result = append(result, common.ViscousEventItem{
			Timestamp: events.Timestamp,
			Data:      data,
		})
	}

	return result
}

func (c *ScrcpyController) Send(data []byte) {
	// for i := 0; i < len(data); i += 32 {
	// 	chunk := data[i : i+32]
	// 	action := chunk[1]
	// 	x := int(binary.BigEndian.Uint32(chunk[10:]))
	// 	y := int(binary.BigEndian.Uint32(chunk[14:]))
	// 	pid := binary.BigEndian.Uint64(chunk[2:])
	// 	log.Debugf("[TOUCH] action=%d ptr=%d x=%d y=%d", action, pid, x, y)
	// }
	n, err := c.controlSocket.Write(data)
	if err != nil {
		log.Warnf("failed to send control data through control socket: %v", err)
		return
	}

	if n != len(data) {
		log.Warnf("partial control data sent: expect %d bytes, sent %d bytes", len(data), n)
		return
	}
}

func (c *ScrcpyController) ResetTouch() {
	if c.controlSocket == nil {
		return
	}
	for i := 0; i < 10; i++ {
		data := c.Encode(common.TouchUp, 0, 0, uint64(i))
		c.controlSocket.Write(data)
	}
}
