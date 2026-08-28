package audio

import (
	"log/slog"
	"testing"
	"time"
)

// talkingRig watches what the UI would be told about one sender.
type talkingRig struct {
	rx   *rxPipeline
	s    *rxStream
	sink FrameSink
	on   bool
}

func newTalkingRig(t *testing.T) *talkingRig {
	t.Helper()
	chain, err := newDSPChain(DSPOptions{}, slog.Default())
	if err != nil {
		t.Fatalf("newDSPChain: %v", err)
	}
	t.Cleanup(chain.close)

	rig := &talkingRig{sink: &collectSink{}}
	rig.rx = newRxPipeline(Config{
		Log:       slog.Default(),
		Callbacks: Callbacks{OnTalking: func(_ uint32, _ string, on bool) { rig.on = on }},
	}, chain)
	t.Cleanup(rig.rx.close)

	rig.s = newVitalsStream(t, vitalsRigSession, "sender")
	rig.rx.streams[vitalsRigSession] = rig.s
	return rig
}

// A sender who stops without a terminator has to stop being shown as talking,
// and within a bound.
//
// The bound is the point. Nothing here guarded it at the pipeline level, and
// the failure it guards against has already happened once: a terminator naming
// a frame the buffer had played past put the stream in a state it could never
// leave, and the peer was lit up as speaking for thirty unbroken seconds while
// the decoder invented audio for every one of them. That was found by reading,
// not by a test, because the only coverage lived a layer down in the buffer.
//
// The measured cost of an ordinary stop-without-a-terminator is 91 ticks, 910
// ms: the silence valve waits a second before it will accept that a sender who
// announced nothing more has finished. Shortening it is not the repair it
// looks like - a TCP burst of several hundred milliseconds is the case this
// buffer exists for, and a shorter valve would blink the indicator off and on
// through exactly those. So the number is pinned rather than reduced, and what
// this test refuses is the class of regression that turns it into thirty
// seconds.
func TestASenderWhoStopsWithoutATerminatorStopsShowingAsTalking(t *testing.T) {
	t.Parallel()
	rig := newTalkingRig(t)

	jitterPrime(rig.s.jit, 0, jitterStartFrames)
	rig.s.lastPacket = time.Now()

	// The sender goes silent after the primed frames: no terminator, nothing.
	silent := -1
	const patience = 4 * jitterSilenceTicks
	for tick := range patience {
		rig.rx.tick(rig.sink, false, &userAudioState{}, 0)
		if !rig.on && tick >= jitterStartFrames {
			silent = tick
			break
		}
	}
	if silent < 0 {
		t.Fatalf("the peer was still shown as talking after %d ticks (%d ms) of silence",
			patience, patience*FrameMs)
	}

	// Measured at 91 ticks. The ceiling leaves room for the valve and no room
	// for a stream that cannot end.
	const ceiling = 2 * jitterSilenceTicks
	if false_ := silent - jitterStartFrames; false_ > ceiling {
		t.Fatalf("the peer was shown as talking for %d ticks (%d ms) after the last frame, want at most %d",
			false_, false_*FrameMs, ceiling)
	}
}

// The other half of the same guarantee: a phrase that ends properly ends at
// once, and does not pay the valve at all. Measured at zero extra ticks - the
// indicator goes out on the tick after the last frame.
//
// Two paths close a terminated phrase, advance() and the dry branch of play(),
// and mutation testing says either one alone is enough here: removing just one
// changes nothing, removing both is caught. That is the right shape for this
// test to have. It guards that the phrase ends, not which line ends it, and
// the redundancy is not an accident - the dry branch was added because a
// terminator naming a frame already played past reaches advance() never.
func TestATerminatedPhraseStopsShowingAsTalkingImmediately(t *testing.T) {
	t.Parallel()
	rig := newTalkingRig(t)

	jitterPrime(rig.s.jit, 0, jitterStartFrames)
	rig.s.jit.PushFinal(jitterStartFrames - 1)
	rig.s.lastPacket = time.Now()

	silent := -1
	for tick := range jitterSilenceTicks {
		rig.rx.tick(rig.sink, false, &userAudioState{}, 0)
		if !rig.on && tick >= jitterStartFrames {
			silent = tick
			break
		}
	}
	if silent < 0 {
		t.Fatal("a phrase that named its own end never stopped showing as talking")
	}
	// One tick to notice the buffer is done, not a second of valve.
	if got := silent - jitterStartFrames; got > 2 {
		t.Errorf("a terminated phrase took %d ticks to stop showing as talking, want at most 2 - "+
			"a sender who said where they finished must not wait on the silence valve", got)
	}
}
