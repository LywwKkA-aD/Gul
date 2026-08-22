// Package rnnoise wraps the vendored RNNoise (third_party/rnnoise, branch
// main) for the fixed project audio grid: 48 kHz mono, 10 ms / 480-sample
// frames, which is exactly the frame RNNoise itself processes.
//
// Frames are float32 in S16 scale (+/-32768), not normalized to +/-1.0.
// This is the single most expensive mistake available here: normalized input
// falls under the internal silence threshold, the network never runs, the
// output is merely the high-passed input delayed by one frame and vad_prob
// stays 0 forever - with no error anywhere. TestNormalizedInputIsSilence
// pins that behaviour down.
//
// Two more properties the caller has to plan for. The output is not
// amplitude limited, so the conversion back to int16 must clip. And the
// denoiser adds about 20 ms of algorithmic delay: 10 ms from the 50% window
// overlap plus one deliberately delayed frame (upstream applies the gains of
// frame N to the spectrum of frame N-1). Dropping the first output frame,
// as the upstream demo does, is the pipeline's decision, not this package's.
//
// The model is not linked in: the library is built with -DUSE_WEIGHTS_FILE
// and the weights come from the embedded blob instead (see
// scripts/vendor-rnnoise.sh). A Denoiser owns C state: it is not safe for
// concurrent use, is released by an explicit Close and has no finalizer
// (project DSP rule - states live on a single locked goroutine).
package rnnoise

/*
#cgo CFLAGS: -O2 -DUSE_WEIGHTS_FILE -I${SRCDIR}/../../../third_party/rnnoise/include -I${SRCDIR}/../../../third_party/rnnoise/src
#cgo darwin CFLAGS: -mmacosx-version-min=11.0
#cgo linux LDFLAGS: -lm
#include <rnnoise.h>
*/
import "C"

import (
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// FrameSamples is the only frame length RNNoise accepts: 10 ms at 48 kHz.
const FrameSamples = 480

// ErrClosed is returned on any use of a Denoiser after Close.
var ErrClosed = errors.New("rnnoise: use after Close")

// weightsBlob is the model in the upstream binary weight format, as produced
// by dump_weights_blob; scripts/vendor-rnnoise.sh regenerates it from the
// pinned model tarball and records its checksum in third_party/rnnoise.
//
//go:embed weights_blob.bin
var weightsBlob []byte

var (
	weightsOnce sync.Once
	weightsMem  unsafe.Pointer
	weightsErr  error
)

// weights returns the process-wide C copy of the model, creating it on first
// use. The copy is required rather than convenient: rnnoise_model_from_buffer
// stores the pointer without copying, and rnnoise_init then keeps pointers
// into the blob inside every DenoiseState built from it, so the buffer has to
// outlive the states and must not be Go memory that the collector may move.
// It is immutable, shared by every Denoiser and lives until the process exits.
func weights() (unsafe.Pointer, error) {
	weightsOnce.Do(func() {
		if n := int(C.rnnoise_get_frame_size()); n != FrameSamples {
			weightsErr = fmt.Errorf("rnnoise: vendored library uses %d-sample frames, want %d", n, FrameSamples)
			return
		}
		weightsMem = C.CBytes(weightsBlob)
	})
	return weightsMem, weightsErr
}
