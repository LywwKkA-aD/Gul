package opus

/*
#include <opus.h>

static int gul_enc_set_bitrate(OpusEncoder *st, opus_int32 v) {
	return opus_encoder_ctl(st, OPUS_SET_BITRATE(v));
}
static int gul_enc_set_vbr(OpusEncoder *st, opus_int32 v) {
	return opus_encoder_ctl(st, OPUS_SET_VBR(v));
}
static int gul_enc_set_complexity(OpusEncoder *st, opus_int32 v) {
	return opus_encoder_ctl(st, OPUS_SET_COMPLEXITY(v));
}
static int gul_enc_reset(OpusEncoder *st) {
	return opus_encoder_ctl(st, OPUS_RESET_STATE);
}
*/
import "C"

import "fmt"

// applicationFor maps a target bitrate to the codec profile the way the
// official Mumble client does (src/mumble/AudioInput.cpp).
func applicationFor(bitrate int) C.int {
	switch {
	case bitrate >= 64000:
		return C.OPUS_APPLICATION_RESTRICTED_LOWDELAY
	case bitrate >= 32000:
		return C.OPUS_APPLICATION_AUDIO
	default:
		return C.OPUS_APPLICATION_VOIP
	}
}

// Encoder compresses our fixed 10 ms mono frames. Not safe for concurrent
// use; release with Close.
type Encoder struct {
	st      *C.OpusEncoder
	bitrate int
}

// NewEncoder creates a 48 kHz mono encoder tuned like the official Mumble
// client for the given bitrate: application profile by bitrate, CBR (an even
// stream for the TCP tunnel), complexity 10.
func NewEncoder(bitrate int) (*Encoder, error) {
	var rc C.int
	st := C.opus_encoder_create(SampleRate, Channels, applicationFor(bitrate), &rc)
	if err := codeErr(rc); err != nil {
		return nil, err
	}
	e := &Encoder{st: st}
	if err := e.SetBitrate(bitrate); err != nil {
		e.Close()
		return nil, err
	}
	if err := codeErr(C.gul_enc_set_vbr(e.st, 0)); err != nil {
		e.Close()
		return nil, err
	}
	if err := codeErr(C.gul_enc_set_complexity(e.st, 10)); err != nil {
		e.Close()
		return nil, err
	}
	return e, nil
}

// SetBitrate applies a new target bitrate, e.g. when the server announces
// its MaxBitrate. The application profile is fixed at creation: recreate the
// encoder when the new rate crosses a profile boundary.
func (e *Encoder) SetBitrate(bitrate int) error {
	if e.st == nil {
		return ErrClosed
	}
	if err := codeErr(C.gul_enc_set_bitrate(e.st, C.opus_int32(bitrate))); err != nil {
		return err
	}
	e.bitrate = bitrate
	return nil
}

// Bitrate reports the last applied target bitrate.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

// Encode compresses exactly one 10 ms frame (len(pcm) == FrameSize) into
// dst and returns the filled prefix. dst is reused when its capacity is at
// least MaxEncodedBytes, otherwise a fresh buffer is allocated.
func (e *Encoder) Encode(pcm []int16, dst []byte) ([]byte, error) {
	if e.st == nil {
		return nil, ErrClosed
	}
	if len(pcm) != FrameSize {
		return nil, fmt.Errorf("opus: encode frame of %d samples, want %d", len(pcm), FrameSize)
	}
	if cap(dst) < MaxEncodedBytes {
		dst = make([]byte, MaxEncodedBytes)
	}
	dst = dst[:cap(dst)]
	n := C.opus_encode(e.st,
		(*C.opus_int16)(&pcm[0]), FrameSize,
		(*C.uchar)(&dst[0]), C.opus_int32(len(dst)))
	if err := codeErr(n); err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// Reset drops the codec state, e.g. after a long transmission pause.
func (e *Encoder) Reset() error {
	if e.st == nil {
		return ErrClosed
	}
	return codeErr(C.gul_enc_reset(e.st))
}

// Close releases the C state. The encoder is unusable afterwards.
func (e *Encoder) Close() {
	if e.st != nil {
		C.opus_encoder_destroy(e.st)
		e.st = nil
	}
}
