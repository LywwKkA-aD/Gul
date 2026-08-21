package opus

/*
#include <opus.h>

static int gul_dec_reset(OpusDecoder *st) {
	return opus_decoder_ctl(st, OPUS_RESET_STATE);
}
*/
import "C"

import "fmt"

// Decoder decompresses one remote voice stream. Not safe for concurrent
// use; release with Close.
type Decoder struct {
	st *C.OpusDecoder
}

// NewDecoder creates a 48 kHz mono decoder. Decoder complexity stays at the
// libopus default 0: the ML concealment paths are not vendored at all.
func NewDecoder() (*Decoder, error) {
	var rc C.int
	st := C.opus_decoder_create(SampleRate, Channels, &rc)
	if err := codeErr(rc); err != nil {
		return nil, err
	}
	return &Decoder{st: st}, nil
}

// Decode decompresses one packet into pcm and returns the sample count.
// len(pcm) bounds the accepted frame and must fit the sender's frame size
// (MaxFrameSize covers any legal packet). Empty data runs packet loss
// concealment for exactly len(pcm) samples.
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if d.st == nil {
		return 0, ErrClosed
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: decode into an empty pcm buffer")
	}
	var p *C.uchar
	if len(data) > 0 {
		p = (*C.uchar)(&data[0])
	}
	n := C.opus_decode(d.st,
		p, C.opus_int32(len(data)),
		(*C.opus_int16)(&pcm[0]), C.int(len(pcm)), 0)
	if err := codeErr(n); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Reset drops the codec state, e.g. when a user's stream restarts.
func (d *Decoder) Reset() error {
	if d.st == nil {
		return ErrClosed
	}
	return codeErr(C.gul_dec_reset(d.st))
}

// Close releases the C state. The decoder is unusable afterwards.
func (d *Decoder) Close() {
	if d.st != nil {
		C.opus_decoder_destroy(d.st)
		d.st = nil
	}
}
