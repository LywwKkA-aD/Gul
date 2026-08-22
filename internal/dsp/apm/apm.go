// Package apm wraps the vendored webrtc-audio-processing 2.1
// (third_party/webrtc-apm) for the fixed Mumble audio grid: 48 kHz mono
// int16, 10 ms / 480-sample frames, which is also the native grid of the
// library, so no resampling or reframing happens on either side.
//
// One APM instance serves the whole client: the capture stream goes through
// ProcessStream, and the single mix of every remote participant goes through
// ProcessReverseStream as the AEC3 reference. The capture chain inside the
// library is fixed at HPF -> AEC3 -> NS -> AGC2 and only echo cancellation and
// the noise suppression level are configurable here; the high-pass filter and
// the adaptive digital gain controller are always on (PLAN.md section 4.3).
//
// Trade-offs of this build. The AVX2 translation units are left out because
// they require per-file -mavx2, which cgo cannot express (see
// apm_avx2_stub_amd64.cc); SSE2 on amd64 and NEON on arm64 are compiled in.
// Debug dumps are compiled out. The alternatives that were weighed, and the
// reason AEC3 replaced speexdsp, are recorded in
// docs/research/alternatives/aec.md.
//
// An APM owns C state: it is not safe for concurrent use, must be released
// with an explicit Close and has no finalizer (project DSP rule: DSP states
// live on a single locked goroutine).
package apm

/*
#cgo CPPFLAGS: -I${SRCDIR}/../../../third_party/webrtc-apm/webrtc -I${SRCDIR}/../../../third_party/webrtc-apm/abseil
// Upstream common_cflags also carries -DWEBRTC_ENABLE_SYMBOL_EXPORT; it is
// omitted here on purpose. With it, WEBRTC_WIN + WEBRTC_LIBRARY_IMPL expand
// RTC_EXPORT to __declspec(dllexport) (rtc_base/system/rtc_export.h), which
// is wrong for a statically linked cgo package and breaks the Windows build.
// Do not re-add it when diffing these flags against meson.build on a bump.
#cgo CPPFLAGS: -DWEBRTC_LIBRARY_IMPL -DWEBRTC_APM_DEBUG_DUMP=0 -DNDEBUG -D_GNU_SOURCE
#cgo CFLAGS: -O2
// One C++ standard for the whole package: abseil is built against a fixed
// -std and mixing standards across translation units breaks its ABI (upstream
// meson.build says as much). The C++ runtime itself needs no -l flag: the go
// tool links a package holding .cc files with the C++ compiler driver.
#cgo CXXFLAGS: -O2 -std=c++17
#cgo arm64 CPPFLAGS: -DWEBRTC_ARCH_ARM64 -DWEBRTC_HAS_NEON
#cgo darwin CPPFLAGS: -DWEBRTC_POSIX -DWEBRTC_MAC
#cgo darwin CFLAGS: -mmacosx-version-min=11.0
#cgo darwin CXXFLAGS: -mmacosx-version-min=11.0
// rtc_base/logging.cc reads the bundle identifier through CoreFoundation on
// macOS release builds; upstream meson.build links the same framework.
#cgo darwin LDFLAGS: -framework CoreFoundation
#cgo linux CPPFLAGS: -DWEBRTC_POSIX -DWEBRTC_LINUX
#cgo linux LDFLAGS: -lm -lpthread
#cgo windows CPPFLAGS: -DWEBRTC_WIN -D_WIN32 -D__STDC_FORMAT_MACROS=1 -DNOMINMAX -D_USE_MATH_DEFINES -D_WINSOCKAPI_
#cgo windows LDFLAGS: -lwinmm

#include "apm_shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
)

// FrameSamples is the frame both directions are processed in: 10 ms of mono
// audio at 48 kHz. The library accepts nothing else on this grid.
const FrameSamples = 480

// The shim and the Go side must agree on the frame size; a mismatch is a
// compile error rather than a runtime surprise.
var _ [FrameSamples]struct{} = [C.GUL_APM_FRAME_SAMPLES]struct{}{}

// maxStreamDelayMs is the widest playout delay we report. Beyond half a second
// the value says more about a stalled device than about the echo path, and
// AEC3 refines the delay from the signal anyway.
const maxStreamDelayMs = 500

// NSLevel selects how aggressively the noise suppressor works. The plan runs
// it soft by default because RNNoise sits behind it in the capture chain and
// two suppressors in series over-suppress speech.
type NSLevel int

const (
	NSOff NSLevel = iota
	NSLow
	NSModerate
	NSHigh
)

// Config is the part of the APM configuration the client controls.
type Config struct {
	// EchoCancel enables AEC3 in desktop mode.
	EchoCancel bool
	// NS selects the noise suppressor level, NSOff disables it.
	NS NSLevel
}

// ErrClosed is returned on any use of an APM after Close.
var ErrClosed = errors.New("apm: use after Close")

// Error is a status code from the library or from the C shim.
type Error int

func (e Error) Error() string {
	switch e {
	case C.GUL_APM_ERR_NULL:
		return "apm: null argument"
	case C.GUL_APM_ERR_CREATE:
		return "apm: webrtc refused to create the audio processing module"
	case C.GUL_APM_ERR_FRAME_SIZE:
		return "apm: webrtc reports a frame size other than 480 at 48 kHz"
	case C.GUL_APM_ERR_NS_LEVEL:
		return "apm: unsupported noise suppression level"
	}
	return fmt.Sprintf("apm: webrtc error %d", int(e))
}

// APM is one audio processing module: the capture chain plus the reverse
// (render) stream that feeds the echo canceller its reference.
type APM struct {
	st *C.gul_apm
}

// nsLevelC maps our level onto the shim constant. The mapping is spelled out
// rather than assumed numeric so the two enumerations can drift apart safely.
func nsLevelC(l NSLevel) (C.int, bool) {
	switch l {
	case NSOff:
		return C.GUL_APM_NS_OFF, true
	case NSLow:
		return C.GUL_APM_NS_LOW, true
	case NSModerate:
		return C.GUL_APM_NS_MODERATE, true
	case NSHigh:
		return C.GUL_APM_NS_HIGH, true
	}
	return 0, false
}

// New creates the audio processing module. There should be exactly one per
// client: the module has a single reverse stream, so every remote participant
// has to be mixed into it before it is played out.
func New(cfg Config) (*APM, error) {
	level, ok := nsLevelC(cfg.NS)
	if !ok {
		return nil, fmt.Errorf("apm: unknown noise suppression level %d", int(cfg.NS))
	}

	var ccfg C.gul_apm_config
	if cfg.EchoCancel {
		ccfg.echo_cancel = 1
	}
	ccfg.ns_level = level

	var rc C.int
	st := C.gul_apm_create(&ccfg, &rc)
	if st == nil {
		return nil, Error(rc)
	}
	return &APM{st: st}, nil
}

// ProcessStream runs the capture chain over one frame in place. Feed it the
// microphone signal before RNNoise and the gate.
func (p *APM) ProcessStream(frame []int16) error {
	if p.st == nil {
		return ErrClosed
	}
	if len(frame) != FrameSamples {
		return fmt.Errorf("apm: frame of %d samples, want %d", len(frame), FrameSamples)
	}
	if rc := C.gul_apm_process_stream(p.st, (*C.int16_t)(&frame[0])); rc != 0 {
		return Error(rc)
	}
	return nil
}

// ProcessReverseStream analyses one frame of the render stream in place. It
// must be fed exactly what goes to the speaker, immediately before it is
// written to the playback ring, or the echo canceller has the wrong reference.
func (p *APM) ProcessReverseStream(frame []int16) error {
	if p.st == nil {
		return ErrClosed
	}
	if len(frame) != FrameSamples {
		return fmt.Errorf("apm: frame of %d samples, want %d", len(frame), FrameSamples)
	}
	if rc := C.gul_apm_process_reverse_stream(p.st, (*C.int16_t)(&frame[0])); rc != 0 {
		return Error(rc)
	}
	return nil
}

// SetStreamDelayMs reports the measured delay between a frame leaving
// ProcessReverseStream and the same frame reaching the microphone: playback
// buffer plus device latency. It is a starting hint, clamped to [0, 500];
// AEC3 keeps refining the delay from the signal itself.
func (p *APM) SetStreamDelayMs(ms int) {
	if p.st == nil {
		return
	}
	if ms < 0 {
		ms = 0
	}
	if ms > maxStreamDelayMs {
		ms = maxStreamDelayMs
	}
	C.gul_apm_set_stream_delay_ms(p.st, C.int(ms))
}

// Close releases the C state. It is idempotent; every other method returns
// ErrClosed afterwards.
func (p *APM) Close() error {
	if p.st != nil {
		C.gul_apm_destroy(p.st)
		p.st = nil
	}
	return nil
}
