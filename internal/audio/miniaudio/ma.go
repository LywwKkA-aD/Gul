// Package miniaudio is our thin cgo wrapper over the vendored miniaudio
// (third_party/miniaudio, patched): two independent devices instead of the
// forbidden duplex mode, realtime callbacks in pure C, frame exchange with
// Go through lock-free SPSC rings. See PLAN.md 4.2 and
// docs/research/alternatives/audio-io.md for the reasoning.
package miniaudio

/*
#cgo CFLAGS: -O2 -I${SRCDIR}/../../../third_party/miniaudio
#cgo darwin CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -lpthread -lm
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "gul_ma.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

const (
	// SampleRate and FrameSamples mirror the project audio grid
	// (48 kHz mono, 10 ms frames) - PLAN.md 4.1.
	SampleRate   = 48000
	FrameSamples = 480
)

// ErrClosed is returned on any use of a closed Context or device.
var ErrClosed = errors.New("miniaudio: use after Close")

// maErr maps an ma_result to an error (nil for MA_SUCCESS).
func maErr(rc C.int) error {
	if rc == C.MA_SUCCESS {
		return nil
	}
	return errors.New("miniaudio: " + C.GoString(C.ma_result_description(C.ma_result(rc))))
}

// DeviceID is an opaque backend device identifier, copyable by value.
type DeviceID [C.sizeof_ma_device_id]byte

// DeviceInfo describes one device of the enumeration snapshot.
type DeviceInfo struct {
	ID        DeviceID
	Name      string
	IsDefault bool
}

// Context owns the backend connection and device enumeration. Safe to use
// from one goroutine; release with Close after closing all devices.
type Context struct {
	ctx *C.ma_context
}

// NewContext initializes the platform default backend.
func NewContext() (*Context, error) {
	ctx := (*C.ma_context)(C.calloc(1, C.sizeof_ma_context))
	if ctx == nil {
		return nil, errors.New("miniaudio: context allocation failed")
	}
	if err := maErr(C.int(C.ma_context_init(nil, 0, nil, ctx))); err != nil {
		C.free(unsafe.Pointer(ctx))
		return nil, err
	}
	return &Context{ctx: ctx}, nil
}

// Devices returns a snapshot of playback and capture devices.
func (c *Context) Devices() (playback, capture []DeviceInfo, err error) {
	if c.ctx == nil {
		return nil, nil, ErrClosed
	}
	var (
		pInfos, cInfos *C.ma_device_info
		pCount, cCount C.ma_uint32
	)
	rc := C.ma_context_get_devices(c.ctx, &pInfos, &pCount, &cInfos, &cCount)
	if err := maErr(C.int(rc)); err != nil {
		return nil, nil, err
	}
	return convertInfos(pInfos, pCount), convertInfos(cInfos, cCount), nil
}

// convertInfos copies the context-owned enumeration (invalidated by the
// next call) into Go values.
func convertInfos(infos *C.ma_device_info, count C.ma_uint32) []DeviceInfo {
	if infos == nil || count == 0 {
		return nil
	}
	src := unsafe.Slice(infos, int(count))
	out := make([]DeviceInfo, 0, int(count))
	for i := range src {
		var id DeviceID
		copy(id[:], C.GoBytes(unsafe.Pointer(&src[i].id), C.sizeof_ma_device_id))
		out = append(out, DeviceInfo{
			ID:        id,
			Name:      C.GoString(&src[i].name[0]),
			IsDefault: src[i].isDefault != 0,
		})
	}
	return out
}

// Close releases the backend. All devices must be closed first.
func (c *Context) Close() error {
	if c.ctx == nil {
		return nil
	}
	err := maErr(C.int(C.ma_context_uninit(c.ctx)))
	C.free(unsafe.Pointer(c.ctx))
	c.ctx = nil
	return err
}
