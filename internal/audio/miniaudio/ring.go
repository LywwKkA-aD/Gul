package miniaudio

/*
#include "gul_ma.h"
*/
import "C"

// Ring exposes the C SPSC frame ring directly: the pipeline uses it via
// the devices, tests exercise it without any hardware.
type Ring struct {
	r            *C.gul_ring
	frameSamples int
}

// NewRing creates a ring of slots frames, frameSamples samples each.
func NewRing(frameSamples, slots int) (*Ring, error) {
	r := C.gul_ring_new(C.int(frameSamples), C.int(slots))
	if r == nil {
		return nil, ErrClosed
	}
	return &Ring{r: r, frameSamples: frameSamples}, nil
}

// Push copies one frame in; false when the ring is full (frame dropped).
func (r *Ring) Push(frame []int16) bool {
	if r.r == nil || len(frame) != r.frameSamples {
		return false
	}
	return C.gul_ring_push(r.r, (*C.int16_t)(&frame[0])) == 1
}

// Pop copies one frame out; false when the ring is empty.
func (r *Ring) Pop(dst []int16) bool {
	if r.r == nil || len(dst) != r.frameSamples {
		return false
	}
	return C.gul_ring_pop(r.r, (*C.int16_t)(&dst[0])) == 1
}

// Available reports frames ready to pop.
func (r *Ring) Available() int {
	if r.r == nil {
		return 0
	}
	return int(C.gul_ring_available(r.r))
}

// Dropped reports frames rejected by Push on overflow.
func (r *Ring) Dropped() uint64 {
	if r.r == nil {
		return 0
	}
	return uint64(C.gul_ring_dropped(r.r))
}

// Close frees the C ring.
func (r *Ring) Close() {
	if r.r != nil {
		C.gul_ring_free(r.r)
		r.r = nil
	}
}
