package audio

import "math"

const (
	// softClipKnee is the level, in full scale units, below which the
	// mixer is bit-transparent. The remaining headroom above it is the
	// saturation zone. 0.9 leaves 10 percent of full scale for the knee,
	// which is enough to fold several simultaneous speakers in without
	// touching the level of the common single-speaker case.
	softClipKnee = 0.9

	// fullScale converts between the int16 sample scale used by the whole
	// pipeline (PLAN.md 4.1) and the full scale units the soft-clip works
	// in. Both factors are powers of two, so the round trip is exact.
	fullScale    = 32768.0
	invFullScale = 1.0 / fullScale
)

// Mixer sums active voice streams into one 10 ms frame: float32
// accumulation, per-source volume, soft-clip. Single-goroutine use.
//
// It belongs to the DSP goroutine (PLAN.md 4.4): no locks, no allocations
// after construction.
type Mixer struct {
	acc   []float32
	dirty bool
}

// NewMixer returns a mixer with an empty accumulator sized for one frame.
func NewMixer() *Mixer {
	return &Mixer{acc: make([]float32, FrameSamples)}
}

// Add accumulates one source frame (len FrameSamples) scaled by volume
// (1.0 = unity; caller clamps user settings to a sane range).
func (m *Mixer) Add(pcm []int16, volume float32) {
	if len(pcm) == 0 || volume == 0 {
		return
	}
	if len(pcm) > FrameSamples {
		pcm = pcm[:FrameSamples]
	}
	acc := m.acc[:len(pcm)]
	for i, s := range pcm {
		acc[i] += float32(s) * volume
	}
	m.dirty = true
}

// Mix writes the accumulated sum into dst (len FrameSamples) using a
// tanh soft-clip so simultaneous speakers cannot wrap around, then
// resets the accumulator for the next tick. With no sources dst is
// silence.
func (m *Mixer) Mix(dst []int16) {
	if !m.dirty {
		clear(dst)
		return
	}
	n := len(dst)
	if n > FrameSamples {
		clear(dst[FrameSamples:])
		n = FrameSamples
	}
	acc := m.acc[:n]
	out := dst[:n]
	for i, sum := range acc {
		out[i] = toInt16(softClip(sum*invFullScale) * fullScale)
	}
	clear(m.acc)
	m.dirty = false
}

// softClip maps a mix in full scale units (1.0 = int16 full scale) into
// the open range (-1, 1).
//
// Below the knee it is the identity, so a single speaker at unity gain
// comes out bit-identical: no compression, no dulling of the common case.
// Above the knee the leftover headroom becomes a tanh saturation zone:
//
//	|y| = k + (1-k) * tanh((|x| - k) / (1-k))
//
// The two pieces meet with the same value and the same slope at the knee
// (tanh'(0) = 1), so the transition is continuous and inaudible, and |y|
// approaches but never reaches 1 for any input, so a loud sum saturates
// smoothly instead of wrapping around. A plain tanh over the whole range
// was rejected: it compresses every level, including a single quiet
// speaker, which is audible as constant downward compression.
func softClip(x float32) float32 {
	if x <= softClipKnee && x >= -softClipKnee {
		return x
	}
	const head = 1.0 - softClipKnee
	if x > 0 {
		return softClipKnee + head*float32(math.Tanh(float64((x-softClipKnee)/head)))
	}
	return -(softClipKnee + head*float32(math.Tanh(float64((-x-softClipKnee)/head))))
}

// toInt16 rounds half away from zero and clamps, so an out of range
// float never becomes an implementation defined int16. Values that are
// already exact integers pass through unchanged.
func toInt16(v float32) int16 {
	if v > 0 {
		v += 0.5
	} else {
		v -= 0.5
	}
	if v >= math.MaxInt16 {
		return math.MaxInt16
	}
	if v <= math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
