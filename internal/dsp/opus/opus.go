// Package opus wraps the vendored libopus 1.6.1 (third_party/opus) for the
// fixed Mumble audio grid: 48 kHz mono int16, 10 ms / 480-sample frames.
//
// The build is float without ML features (no DRED/OSCE/BWE), without
// intrinsics and RTCD; the trade-offs are measured and recorded in
// docs/research/alternatives/opus.md.
//
// Encoder and Decoder own C state: they are not safe for concurrent use,
// must be released with an explicit Close and have no finalizers (project
// DSP rule: codec states live on a single locked goroutine).
package opus

/*
#cgo CFLAGS: -DHAVE_CONFIG_H -I${SRCDIR} -I${SRCDIR}/../../../third_party/opus/include -I${SRCDIR}/../../../third_party/opus/celt -I${SRCDIR}/../../../third_party/opus/silk -I${SRCDIR}/../../../third_party/opus/silk/float -I${SRCDIR}/../../../third_party/opus/src
#include <opus.h>
*/
import "C"

import "errors"

// ErrClosed is returned on any use of an Encoder or Decoder after Close.
var ErrClosed = errors.New("opus: use after Close")

const (
	// SampleRate and Channels are fixed by the Mumble protocol.
	SampleRate = 48000
	Channels   = 1
	// FrameSize is our own TX frame: 10 ms at 48 kHz.
	FrameSize = 480
	// MaxFrameSize is the largest frame a remote client may legally send
	// inside one packet (60 ms); RX buffers must accommodate it.
	MaxFrameSize = 2880
	// MaxEncodedBytes is the recommended upper bound for one encoded
	// packet (RFC 6716 section 3.2.1).
	MaxEncodedBytes = 1275
)

// Version reports the vendored library version string.
func Version() string {
	return C.GoString(C.opus_get_version_string())
}

// Error is a libopus status code (negative values of OPUS_*).
type Error int

func (e Error) Error() string {
	return "opus: " + C.GoString(C.opus_strerror(C.int(e)))
}

// codeErr maps a libopus return code to an error (nil for OPUS_OK and any
// non-negative result).
func codeErr(rc C.int) error {
	if rc >= 0 {
		return nil
	}
	return Error(rc)
}
