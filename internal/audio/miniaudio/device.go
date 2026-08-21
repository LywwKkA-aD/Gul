package miniaudio

/*
#include <stdlib.h>
#include "gul_ma.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Stats is a monotonic snapshot of one device's counters. CallbackFrames
// against wall time is the drift input (PLAN.md 4.2); drops and underruns
// are visible desync instead of the silent sabotage of duplex mode.
type Stats struct {
	CallbackFrames uint64 // frames seen by the realtime callback
	RingDropped    uint64 // capture: frames lost to a full ring
	Underruns      uint64 // playback: periods padded with silence
	Reroutes       uint64 // device changes routed by the backend
	Stopped        bool   // backend stopped the device (unplug, error)
}

// device is the shared half of Capture and Playback.
type device struct {
	d *C.gul_device
}

func (dev *device) open(ctx *Context, typ C.int, id *DeviceID, ringFrames int) error {
	if ctx.ctx == nil {
		return ErrClosed
	}
	if ringFrames <= 0 {
		return fmt.Errorf("miniaudio: ring of %d frames", ringFrames)
	}
	var cid *C.ma_device_id
	if id != nil {
		// ma_device_init copies the ID into the device; the pointer is not
		// retained past the call.
		cid = (*C.ma_device_id)(C.CBytes(id[:]))
		defer C.free(unsafe.Pointer(cid))
	}
	return maErr(C.gul_device_open(&dev.d, ctx.ctx, typ, cid,
		SampleRate, FrameSamples, C.int(ringFrames)))
}

// Start begins the realtime callbacks.
func (dev *device) Start() error {
	if dev.d == nil {
		return ErrClosed
	}
	return maErr(C.gul_device_start(dev.d))
}

// Stop halts the callbacks; the device can be started again.
func (dev *device) Stop() error {
	if dev.d == nil {
		return ErrClosed
	}
	return maErr(C.gul_device_stop(dev.d))
}

// Stats reads the counters kept by the C side.
func (dev *device) Stats() Stats {
	if dev.d == nil {
		return Stats{Stopped: true}
	}
	return Stats{
		CallbackFrames: uint64(C.gul_device_callback_frames(dev.d)),
		RingDropped:    uint64(C.gul_ring_dropped(C.gul_device_ring(dev.d))),
		Underruns:      uint64(C.gul_device_underruns(dev.d)),
		Reroutes:       uint64(C.gul_device_reroutes(dev.d)),
		Stopped:        C.gul_device_stopped(dev.d) != 0,
	}
}

// InternalSampleRate reports the device-native rate: anything other than
// SampleRate means the miniaudio resampler is engaged and must be logged.
func (dev *device) InternalSampleRate() int {
	if dev.d == nil {
		return 0
	}
	return int(C.gul_device_internal_rate(dev.d))
}

// InternalPeriod reports the device-native period in frames.
func (dev *device) InternalPeriod() int {
	if dev.d == nil {
		return 0
	}
	return int(C.gul_device_internal_period(dev.d))
}

// Close stops the device and frees the C state.
func (dev *device) Close() {
	if dev.d != nil {
		C.gul_device_close(dev.d)
		dev.d = nil
	}
}

// Capture is the microphone side: the C callback pushes 480-sample frames
// into the ring, the DSP goroutine drains them with ReadFrame.
type Capture struct {
	device
}

// OpenCapture opens a capture device (nil id = system default) with a ring
// of ringFrames frames.
func (c *Context) OpenCapture(id *DeviceID, ringFrames int) (*Capture, error) {
	dev := &Capture{}
	if err := dev.open(c, C.GUL_DEVICE_CAPTURE, id, ringFrames); err != nil {
		return nil, err
	}
	return dev, nil
}

// ReadFrame moves one frame into dst without blocking. len(dst) must be
// FrameSamples. Returns false when no frame is ready.
func (c *Capture) ReadFrame(dst []int16) bool {
	if c.d == nil || len(dst) != FrameSamples {
		return false
	}
	return C.gul_ring_pop(C.gul_device_ring(c.d), (*C.int16_t)(&dst[0])) == 1
}

// Playback is the speaker side: the DSP goroutine feeds frames with
// WriteFrame, the C callback drains the ring (silence on underrun).
type Playback struct {
	device
}

// OpenPlayback opens a playback device (nil id = system default) with a
// ring of ringFrames frames.
func (c *Context) OpenPlayback(id *DeviceID, ringFrames int) (*Playback, error) {
	pb := &Playback{}
	if err := pb.open(c, C.GUL_DEVICE_PLAYBACK, id, ringFrames); err != nil {
		return nil, err
	}
	return pb, nil
}

// WriteFrame queues one frame without blocking. len(src) must be
// FrameSamples. Returns false when the ring is full (frame not queued).
func (p *Playback) WriteFrame(src []int16) bool {
	if p.d == nil || len(src) != FrameSamples {
		return false
	}
	return C.gul_ring_push(C.gul_device_ring(p.d), (*C.int16_t)(&src[0])) == 1
}
