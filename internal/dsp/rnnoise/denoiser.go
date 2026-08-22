package rnnoise

/*
#include <rnnoise.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Denoiser is one RNNoise state: the noise suppressor of the TX path and the
// source of the VAD probability the transmission gate runs on. Not safe for
// concurrent use; release with Close.
type Denoiser struct {
	st    *C.DenoiseState
	model *C.RNNModel
}

// NewDenoiser creates a denoiser over the embedded weights blob.
func NewDenoiser() (*Denoiser, error) {
	blob, err := weights()
	if err != nil {
		return nil, err
	}
	model := C.rnnoise_model_from_buffer(blob, C.int(len(weightsBlob)))
	if model == nil {
		return nil, errors.New("rnnoise: cannot read the embedded weights blob")
	}
	st := C.rnnoise_create(model)
	if st == nil {
		C.rnnoise_model_free(model)
		return nil, errors.New("rnnoise: cannot initialise a state from the embedded weights blob")
	}
	return &Denoiser{st: st, model: model}, nil
}

// Process denoises exactly one frame in place and returns the speech
// probability of that frame. len(frame) must be FrameSamples, and the samples
// must be in S16 scale (+/-32768) - see the package documentation for what
// normalized input silently does.
//
// The returned probability is the VAD output of the network, so it is exactly
// 0 on frames the internal silence detector rejected: use it with hysteresis,
// never as a hard threshold. The denoised output is not amplitude limited;
// clip when converting back to int16.
func (d *Denoiser) Process(frame []float32) (vadProb float32, err error) {
	if d.st == nil {
		return 0, ErrClosed
	}
	if len(frame) != FrameSamples {
		return 0, fmt.Errorf("rnnoise: frame of %d samples, want %d", len(frame), FrameSamples)
	}
	buf := (*C.float)(unsafe.Pointer(&frame[0]))
	return float32(C.rnnoise_process_frame(d.st, buf, buf)), nil
}

// Close releases the C state. It is idempotent, and the denoiser is unusable
// afterwards. The state is freed before the model it borrows weights from.
func (d *Denoiser) Close() error {
	if d.st != nil {
		C.rnnoise_destroy(d.st)
		d.st = nil
	}
	if d.model != nil {
		C.rnnoise_model_free(d.model)
		d.model = nil
	}
	return nil
}
