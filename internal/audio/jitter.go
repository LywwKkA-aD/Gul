package audio

// Adaptive per-user jitter buffer for the RX path (PLAN.md 4.4).
//
// Voice rides on Mumble's TCP control channel, so frames are not lost in
// transit, but head-of-line blocking turns one stalled segment into a silence
// of hundreds of milliseconds followed by a burst of frames delivered at once.
// The buffer is therefore tuned for bursts rather than for random loss:
//
//   - Playout starts only once the target depth is buffered, so a stream never
//     starts with an empty reserve.
//   - Running dry does not advance the playout position: the slot is concealed,
//     the target depth grows by one frame per starved tick (capped at
//     JitterMaxMs) and the buffer re-primes to the new depth. Nothing is thrown
//     away, the stream just slips later in time by the length of the stall.
//   - A clean run of playout decays the target by one frame every couple of
//     seconds, and audio queued above the target is trimmed by dropping a single
//     frame at a time, far enough apart not to be heard as a jump.
//
// Frames live in a fixed ring addressed by absolute sequence number, which
// gives ordering and duplicate rejection for free and keeps Push and Pop
// allocation-free. One buffer belongs to one sender and is touched only by the
// DSP goroutine, so there are no locks.

// JitterResult tells the caller what to do with the current 10 ms slot.
type JitterResult int

const (
	JitterFrame   JitterResult = iota // dst filled with the next frame
	JitterConceal                     // gap: caller must run PLC for this slot
	JitterIdle                        // no active stream, caller outputs nothing
)

const (
	// Depth bounds derived from the grid constants: 8 frames of start latency,
	// 50 frames of ceiling for TCP bursts.
	jitterStartFrames = JitterStartMs / FrameMs
	jitterMaxFrames   = JitterMaxMs / FrameMs

	// The ring holds twice the depth ceiling so that a burst overshooting the
	// target still lands intact and is trimmed afterwards instead of at intake.
	jitterRingFrames = jitterMaxFrames * 2

	// A sequence this far outside the current window is a new transmission
	// (long silence, sender restart, sequence counted from zero again).
	jitterRestartGap = jitterRingFrames * 2

	// One frame of target decay per this many ticks of clean playout (2 s).
	jitterShrinkTicks = 200

	// Trim one queued frame per this many ticks while the queue sits above the
	// target (500 ms), and faster while it sits above the depth ceiling.
	jitterSoftDropTicks = 50
	jitterHardDropTicks = 10

	// Ticks without any arrival that end the transmission when the buffer is
	// empty, or release frames held back during priming (1 s).
	jitterSilenceTicks = 100
)

type jitterState uint8

const (
	jitterIdleState  jitterState = iota // nothing to play
	jitterPrimeState                    // first frames arriving, output nothing yet
	jitterPlayState                     // handing out frames
	jitterStallState                    // ran dry mid-transmission, rebuilding depth
)

// Jitter reorders, de-duplicates and paces decoded 10 ms frames of one sender.
type Jitter struct {
	ring    [][]int16 // slot i holds the frame whose sequence is i mod len(ring)
	present []bool

	state jitterState

	next    int64 // sequence due on the next tick
	highest int64 // one past the highest sequence seen

	count       int // frames currently held
	target      int // adaptive target depth, in frames
	stallTarget int // target as it was when the current stall began

	final    bool
	finalSeq int64

	sincePush     int // ticks since the last accepted arrival
	sinceUnderrun int // ticks of clean playout
	sinceDrop     int // ticks since the last trim
}

// NewJitter creates a per-user adaptive jitter buffer for 480-sample
// frames. Single-goroutine use (the DSP goroutine), no locks needed.
func NewJitter() *Jitter {
	j := &Jitter{
		ring:    make([][]int16, jitterRingFrames),
		present: make([]bool, jitterRingFrames),
		target:  jitterStartFrames,
	}
	// One backing array, sliced into fixed frames: the ring never grows, so a
	// deeper target costs latency but no allocation.
	backing := make([]int16, jitterRingFrames*FrameSamples)
	for i := range j.ring {
		lo := i * FrameSamples
		j.ring[i] = backing[lo : lo+FrameSamples : lo+FrameSamples]
	}
	return j
}

// Push inserts one decoded 10 ms frame with its absolute sequence number
// (Mumble sequence is counted in 10 ms frame units; the RX pipeline derives
// per-frame numbers when repacking 20/40/60 ms packets).
func (j *Jitter) Push(seq int64, frame []int16) {
	if seq < 0 {
		return
	}
	if j.state == jitterIdleState {
		j.startStream(seq)
	} else if j.isRestart(seq) {
		j.clear()
		j.startStream(seq)
	}
	if seq < j.next {
		if j.state != jitterPrimeState || j.highest-seq > jitterRingFrames {
			// Late: this slot has already been played or concealed.
			return
		}
		// Still priming, so nothing has been played yet: the transmission
		// simply did not start with its lowest sequence number.
		j.next = seq
	}
	if seq >= j.next+jitterRingFrames {
		// The ring cannot hold this frame together with the pending ones. Give
		// up the oldest audio rather than the newest.
		j.dropUntil(seq - jitterRingFrames + 1)
	}
	idx := j.slot(seq)
	if j.present[idx] {
		// Duplicate: the first copy wins.
		return
	}
	copyIntoSlot(j.ring[idx], frame)
	j.present[idx] = true
	j.count++
	if seq >= j.highest {
		j.highest = seq + 1
	}
	j.sincePush = 0
}

// PushFinal marks the end of the sender's transmission at seq (terminator
// packet): the buffer may drain fully and go idle without waiting.
func (j *Jitter) PushFinal(seq int64) {
	if j.state == jitterIdleState || j.isRestart(seq) {
		return
	}
	j.final = true
	j.finalSeq = seq
	if seq >= j.highest {
		// Slots up to the terminator were sent; anything missing gets concealed
		// rather than silently skipped.
		j.highest = seq + 1
	}
	j.sincePush = 0
}

// Pop is called exactly once per 10 ms tick. dst has FrameSamples length.
func (j *Jitter) Pop(dst []int16) JitterResult {
	j.sincePush++

	switch j.state {
	case jitterIdleState:
		return JitterIdle

	case jitterPrimeState:
		if !j.ready() {
			if j.count == 0 && j.sincePush >= jitterSilenceTicks {
				// The sender went away before the buffer filled.
				j.endStream()
			}
			return JitterIdle
		}
		j.state = jitterPlayState

	case jitterStallState:
		if !j.ready() {
			if j.count == 0 {
				if j.sincePush >= jitterSilenceTicks {
					// Nothing came back: the sender stopped talking rather than
					// the link stalling, so the deeper target was never earned.
					j.target = j.stallTarget
					j.endStream()
					return JitterIdle
				}
				j.grow()
			}
			return JitterConceal
		}
		j.state = jitterPlayState
	}

	return j.play(dst)
}

// Reset drops all state (stream restart, user reconnect).
func (j *Jitter) Reset() {
	j.endStream()
	j.target = jitterStartFrames
	j.stallTarget = jitterStartFrames
	j.sincePush = 0
	j.sinceUnderrun = 0
	j.sinceDrop = 0
}

// Depth reports the current target depth in frames (diagnostics).
func (j *Jitter) Depth() int { return j.target }

// play hands out the slot at j.next, adapting the depth on the way.
func (j *Jitter) play(dst []int16) JitterResult {
	j.sinceUnderrun++
	if j.sinceUnderrun >= jitterShrinkTicks && j.target > jitterStartFrames {
		j.target--
		j.sinceUnderrun = 0
	}
	j.trimExcess()

	idx := j.slot(j.next)
	if j.present[idx] {
		copy(dst, j.ring[idx])
		j.present[idx] = false
		j.count--
		j.advance()
		return JitterFrame
	}
	if j.next < j.highest {
		// A hole inside the range the sender announced: the frame is not coming.
		j.advance()
		return JitterConceal
	}
	if j.final && j.next > j.finalSeq {
		// Nothing left and nothing coming: the terminator named a frame this
		// buffer has already played past. Ending here rather than stalling is
		// not an optimisation - without it the buffer never leaves this state.
		//
		// ready() answers true unconditionally once final is set, so the stall
		// state hands control straight back to play, which runs dry again and
		// stalls again. advance() is what calls endStream, and the dry path
		// never reaches it, so next never passes finalSeq and the stream never
		// closes. Measured before the fix: three thousand ticks - thirty
		// seconds - of unbroken conceal, the target pinned at the 500 ms
		// ceiling, the peer lit up as speaking the whole time, their decoder
		// never released, and the ceiling still in place when they next spoke.
		//
		// It is reachable from any peer: a terminator whose payload is empty
		// or undecodable arrives with no frames of its own (pipeline_rx.go),
		// which is exactly a final for a sequence already behind us.
		j.endStream()
		return JitterIdle
	}
	// Ran dry. Hold the playout position so no audio is skipped, deepen the
	// target by the length of the stall and rebuild before speaking again.
	// The pre-stall depth is remembered: growth is kept only if audio really
	// comes back, which is what tells a stalled link from a finished phrase.
	j.stallTarget = j.target
	j.grow()
	j.state = jitterStallState
	j.sinceDrop = 0
	return JitterConceal
}

// ready reports whether enough audio is queued to (re)start playout.
func (j *Jitter) ready() bool {
	if j.final {
		// The transmission is over: drain whatever is left, do not wait.
		return true
	}
	if j.count == 0 {
		return false
	}
	// The silence valve keeps a short burst from being held back forever when
	// the sender stops without a terminator.
	return j.count >= j.target || j.sincePush >= jitterSilenceTicks
}

// trimExcess gives back latency accumulated above the target, one frame at a
// time and rarely enough that the skip stays inaudible.
func (j *Jitter) trimExcess() {
	if j.final {
		// Draining anyway; dropping tail audio would only truncate the phrase.
		j.sinceDrop = 0
		return
	}
	limit := jitterSoftDropTicks
	switch {
	case j.count > jitterMaxFrames:
		limit = jitterHardDropTicks
	case j.count > j.target:
	default:
		j.sinceDrop = 0
		return
	}
	j.sinceDrop++
	if j.sinceDrop < limit {
		return
	}
	j.sinceDrop = 0
	idx := j.slot(j.next)
	if j.present[idx] {
		j.present[idx] = false
		j.count--
		j.advance()
	}
}

// grow deepens the target after a starved tick.
func (j *Jitter) grow() {
	if j.target < jitterMaxFrames {
		j.target++
	}
	j.sinceUnderrun = 0
}

// advance moves the playout position and closes the stream at the terminator.
func (j *Jitter) advance() {
	j.next++
	if j.final && j.next > j.finalSeq {
		j.endStream()
	}
}

// isRestart reports whether seq belongs to a new transmission rather than to
// the one currently buffered.
func (j *Jitter) isRestart(seq int64) bool {
	if j.final && seq > j.finalSeq {
		return true
	}
	return seq >= j.next+jitterRestartGap || seq <= j.next-jitterRestartGap
}

// startStream arms the buffer for a transmission beginning at seq. The learned
// target depth survives: it describes the link, not the phrase.
func (j *Jitter) startStream(seq int64) {
	j.state = jitterPrimeState
	j.next = seq
	j.highest = seq
	j.final = false
	j.finalSeq = 0
	j.sincePush = 0
	j.sinceUnderrun = 0
	j.sinceDrop = 0
}

// endStream returns to idle, ready for the next transmission (any sequence
// range, including one starting at zero again).
func (j *Jitter) endStream() {
	j.clear()
	j.state = jitterIdleState
	j.next = 0
	j.highest = 0
	j.final = false
	j.finalSeq = 0
}

// dropUntil discards queued frames below seq.
func (j *Jitter) dropUntil(seq int64) {
	if seq-j.next >= jitterRingFrames {
		j.clear()
		j.next = seq
		return
	}
	for ; j.next < seq; j.next++ {
		idx := j.slot(j.next)
		if j.present[idx] {
			j.present[idx] = false
			j.count--
		}
	}
}

func (j *Jitter) clear() {
	for i := range j.present {
		j.present[i] = false
	}
	j.count = 0
}

func (j *Jitter) slot(seq int64) int { return int(seq % jitterRingFrames) }

// copyIntoSlot fills a ring slot, tolerating a short or long source frame so that
// a malformed packet cannot panic the DSP goroutine.
func copyIntoSlot(dst, src []int16) {
	n := copy(dst, src)
	clear(dst[n:])
}
