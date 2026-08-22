package audio

// Transmit gate for the TX path (PLAN.md 4.3). It takes one decision per
// 10 ms frame, between RNNoise and the Opus encoder: transmit this frame or
// stay silent. Closing the gate ends the transmission, but emitting the
// terminator frame belongs to the pipeline, not here.
//
// The VAD mode is hysteretic on purpose. A single threshold on a per frame
// speech probability chatters around its decision point, and every flip
// costs a restarted Opus transmission, so opening takes a high probability
// while staying open only takes a lower one. The hangover tail then carries
// the transmission across the quiet gaps inside a phrase (stops, unvoiced
// consonants, a breath between words). The tail is the cheaper of the two
// possible errors: a few hundred milliseconds of room tone after a phrase
// is inaudible next to a clipped word ending, which is what M3 must not do
// ("VAD does not cut off the starts of words", PLAN.md 7).
//
// The opening frame is transmitted rather than swallowed: the probability
// describes the frame that is about to be encoded, so dropping it would
// remove the first frame of the word that triggered the opening.
//
// Single-goroutine use (the DSP goroutine): plain fields, no locks and no
// atomics. The engine feeds Update and changes the settings from that same
// goroutine.

// GateMode selects what decides transmission.
type GateMode int

const (
	// GateVAD follows the denoiser's speech probability, with hysteresis
	// and a hangover tail.
	GateVAD GateMode = iota
	// GatePTT follows the push-to-talk key and nothing else.
	GatePTT
)

const (
	// Defaults from PLAN.md 4.3. The thresholds are speech probabilities as
	// reported by RNNoise; the span between them is the hysteresis band.
	gateOpenDefault       = 0.6
	gateCloseDefault      = 0.4
	gateHangoverDefaultMs = 300

	// gateHangoverMaxMs bounds what the UI can dial in. A tail of several
	// seconds stops being a tail: it holds the transmission open across
	// whole silences, which is what the gate exists to avoid.
	gateHangoverMaxMs = 5000
)

// Gate decides, frame by frame, whether the microphone goes on the wire.
type Gate struct {
	mode GateMode

	openLevel  float32 // closed -> open, strictly above
	closeLevel float32 // open stays open at or above this

	hangover int // tail length in frames
	left     int // tail frames still available in the current run of silence

	transmitting bool
}

// NewGate returns a gate in VAD mode with the PLAN.md 4.3 defaults:
// open above 0.6, hold at or above 0.4, 300 ms of hangover.
func NewGate() *Gate {
	return &Gate{
		openLevel:  gateOpenDefault,
		closeLevel: gateCloseDefault,
		hangover:   gateHangoverDefaultMs / FrameMs,
	}
}

// SetMode switches between VAD and push-to-talk. Changing the mode resets
// the gate: a hangover earned under VAD must not keep the microphone open
// after the user moved to a key they are not holding. Setting the mode the
// gate is already in is a no-op, so the engine may call this every tick.
func (g *Gate) SetMode(m GateMode) {
	if m != GateVAD && m != GatePTT {
		return
	}
	if m == g.mode {
		return
	}
	g.mode = m
	g.Reset()
}

// SetThresholds sets the VAD hysteresis band. Both values are clamped to
// [0, 1], and close is pulled down to open if the caller inverts the pair:
// open is the threshold that decides whether speech is picked up at all, so
// it is the one worth preserving. A NaN in either value discards the call -
// keeping the previous band beats clamping NaN to an always-open microphone.
func (g *Gate) SetThresholds(open, close float32) {
	if open != open || close != close {
		return
	}
	open = clampProb(open)
	close = clampProb(close)
	if close > open {
		close = open
	}
	g.openLevel = open
	g.closeLevel = close
}

// SetHangoverMs sets the tail length, clamped to [0, 5000] ms and rounded
// down to whole 10 ms frames. Shortening it while the tail is running
// applies immediately instead of letting the old, longer tail finish.
func (g *Gate) SetHangoverMs(ms int) {
	if ms < 0 {
		ms = 0
	}
	if ms > gateHangoverMaxMs {
		ms = gateHangoverMaxMs
	}
	g.hangover = ms / FrameMs
	if g.left > g.hangover {
		g.left = g.hangover
	}
}

// Update takes the decision for one 10 ms frame and reports whether that
// frame is transmitted. Call it exactly once per frame: the hangover is
// counted in calls, not in wall clock time.
func (g *Gate) Update(vadProb float32, pttHeld bool) bool {
	if g.mode == GatePTT {
		// The key is the whole decision: no hysteresis, no tail. A release
		// stops the transmission on that same frame.
		return pttHeld
	}

	if !g.transmitting {
		if vadProb > g.openLevel {
			g.transmitting = true
			g.left = g.hangover
			return true
		}
		return false
	}

	if vadProb >= g.closeLevel {
		// Still speech: the tail starts over from full length whenever it
		// comes back, so a gap inside a phrase never accumulates towards
		// the closing of that phrase.
		g.left = g.hangover
		return true
	}
	if g.left > 0 {
		g.left--
		return true
	}
	g.transmitting = false
	return false
}

// Reset closes the gate and clears the hangover (mute, device change,
// transmission aborted elsewhere). The thresholds, the tail length and the
// mode are settings and survive.
func (g *Gate) Reset() {
	g.transmitting = false
	g.left = 0
}

// clampProb folds a probability into [0, 1].
func clampProb(v float32) float32 {
	// Also catches NaN: any comparison with NaN is false.
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
