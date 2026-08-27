package opus

/*
#include <opus.h>

static int gul_enc_set_bitrate(OpusEncoder *st, opus_int32 v) {
	return opus_encoder_ctl(st, OPUS_SET_BITRATE(v));
}
static int gul_enc_set_vbr_constraint(OpusEncoder *st, opus_int32 v) {
	return opus_encoder_ctl(st, OPUS_SET_VBR_CONSTRAINT(v));
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

// NewEncoder creates a 48 kHz mono encoder for the given bitrate: application
// profile by bitrate, constrained variable bitrate, complexity 10.
//
// DECISION 2026-08-27: variable bitrate, against the official Mumble client's
// constant one, because constant bitrate is a fingerprint of the plainest
// kind. Measured on a speech-shaped signal at 40 kbit/s: constant produced
// exactly ONE frame size, 50 bytes, for every frame without exception;
// variable produced forty distinct sizes between 34 and 99 with the mean held
// at the target. A hundred identical packets a second is something a
// classifier recognises without reading a byte of them.
//
// The cost, stated plainly: frame sizes now follow the energy of the speech,
// which is a known way to learn a little about what is being said through an
// encrypted stream. That is a research-grade attack against an observer who
// wants to transcribe; the observer this exists for wants to identify and
// block, and against them the constant size was the gift. On the QUIC road
// the padding removes the leak entirely (relayproto.Salamander); on the
// WebSocket road it stands until that road gets padding of its own.
//
// The constraint is on so the average stays at the target: unconstrained
// peaks could run past what the server allows per user, and murmur enforces
// that.
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
	if err := codeErr(C.gul_enc_set_vbr(e.st, 1)); err != nil {
		e.Close()
		return nil, err
	}
	if err := codeErr(C.gul_enc_set_vbr_constraint(e.st, 1)); err != nil {
		e.Close()
		return nil, err
	}
	if err := codeErr(C.gul_enc_set_complexity(e.st, 10)); err != nil {
		e.Close()
		return nil, err
	}
	return e, nil
}

// SetVBR switches the encoder between constant and variable bitrate. Exposed
// for measurement; production picks one at construction.
func (e *Encoder) SetVBR(on int) error {
	if e.st == nil {
		return ErrClosed
	}
	return codeErr(C.gul_enc_set_vbr(e.st, C.opus_int32(on)))
}

// SetVBRConstraint bounds variable bitrate to the target, so peaks cannot run
// past what the server allows.
func (e *Encoder) SetVBRConstraint(on int) error {
	if e.st == nil {
		return ErrClosed
	}
	return codeErr(C.gul_enc_set_vbr_constraint(e.st, C.opus_int32(on)))
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
