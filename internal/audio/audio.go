// Package audio implements the voice engine: devices and rings (see the
// miniaudio subpackage), the DSP goroutine, per-user jitter buffers, the
// mixer and level meters. The specification is PLAN.md section 4; the
// invariants live in .claude/skills/gul-audio-core.
package audio

const (
	// The fixed project audio grid (PLAN.md 4.1).
	SampleRate   = 48000
	Channels     = 1
	FrameMs      = 10
	FrameSamples = SampleRate / 1000 * FrameMs // 480

	// Adaptive jitter buffer bounds (PLAN.md 4.1): the start depth is the
	// initial latency budget, the max absorbs TCP head-of-line bursts.
	JitterStartMs = 80
	JitterMaxMs   = 500
)
