package audio

import (
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"
)

// countSink keeps a tally instead of a slice, so it can stand inside a
// measured tick without allocating itself.
type countSink struct{ frames, audible int }

func (s *countSink) WriteFrame(src []int16) bool {
	s.frames++
	if RMS(src) > 0 {
		s.audible++
	}
	return true
}

// cueRig drives the receive path without devices and without the engine's
// ticker: one step is exactly one DSP tick, so every cue assertion below is
// deterministic instead of timing dependent. The DSP shape is the bare one
// (no APM, no denoiser), so the mix reaches the sink untouched and what the
// test measures is the cue itself.
type cueRig struct {
	e    *Engine
	rx   *rxPipeline
	rec  *collectSink
	sink FrameSink
	vols sync.Map
}

func newCueRig(t *testing.T) *cueRig {
	t.Helper()
	chain, err := newDSPChain(DSPOptions{}, slog.Default())
	if err != nil {
		t.Fatalf("newDSPChain: %v", err)
	}
	t.Cleanup(chain.close)
	cfg := Config{Log: slog.Default()}
	rec := &collectSink{}
	r := &cueRig{e: NewEngine(cfg), rx: newRxPipeline(cfg, chain), rec: rec, sink: rec}
	t.Cleanup(r.rx.close)
	return r
}

// step runs n ticks the way Engine.run does: apply the pending cue, then
// produce one playback frame.
func (r *cueRig) step(n int, deafened bool) {
	for range n {
		r.e.applyCue(&r.rx.cues)
		r.rx.tick(r.sink, deafened, &r.vols, 0)
	}
}

// audible returns the RMS of every non-silent frame, in order. With no
// remote streams the only source is the cue, so silence is exactly zero.
func (r *cueRig) audible() []float64 {
	r.rec.mu.Lock()
	defer r.rec.mu.Unlock()
	out := make([]float64, 0, len(r.rec.rms))
	for _, v := range r.rec.rms {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func cueFrames(c Cue) int { return len(cueClips[c]) / FrameSamples }

// TestCuePlaysExpectedDuration checks that a queued cue reaches the sink as
// a contiguous run of audible frames as long as its clip, starting on the
// first tick after PlayCue and stopping on its own.
func TestCuePlaysExpectedDuration(t *testing.T) {
	for _, c := range []Cue{CueJoin, CueLeave, CueMuted, CueUnmuted} {
		rig := newCueRig(t)
		rig.e.PlayCue(c)
		want := cueFrames(c)
		rig.step(want+10, false)

		if got := len(rig.audible()); got != want {
			t.Errorf("cue %d: %d audible frames, want %d", c, got, want)
		}
		rig.rec.mu.Lock()
		frames := append([]float64(nil), rig.rec.rms...)
		rig.rec.mu.Unlock()
		for i, v := range frames {
			quiet := v == 0
			if wantQuiet := i >= want; quiet != wantQuiet {
				t.Fatalf("cue %d: frame %d rms %.0f, want quiet=%v", c, i, v, wantQuiet)
			}
		}
	}
}

// TestPlayCueKeepsAtMostOnePending pins the drop rule at the queue itself:
// the slot holds one cue, a second call while it is still full is lost.
func TestPlayCueKeepsAtMostOnePending(t *testing.T) {
	e := NewEngine(Config{})
	e.PlayCue(CueJoin)
	e.PlayCue(CueLeave)

	got, ok := e.takeCue()
	if !ok || got != CueJoin {
		t.Fatalf("takeCue = %d, %v; want CueJoin, true", got, ok)
	}
	if _, ok := e.takeCue(); ok {
		t.Fatal("a second cue was queued, want the slot emptied and the extra call dropped")
	}
}

// TestTwoPlayCueCallsPlayOnce is the same rule end to end. Nothing drains
// the slot between the two calls (the rig has no DSP goroutine), so the
// result does not depend on scheduling.
func TestTwoPlayCueCallsPlayOnce(t *testing.T) {
	rig := newCueRig(t)
	rig.e.PlayCue(CueJoin)
	rig.e.PlayCue(CueLeave)
	rig.step(cueFrames(CueJoin)+cueFrames(CueLeave)+10, false)

	if got, want := len(rig.audible()), cueFrames(CueJoin); got != want {
		t.Fatalf("%d audible frames, want %d - the second cue was not dropped", got, want)
	}
}

// TestCueDuringPlaybackWaitsForTheClip pins the no-click rule: a cue that
// arrives mid-clip does not restart the player (a restart puts a step of up
// to the clip's peak into the output), it plays back to back afterwards.
func TestCueDuringPlaybackWaitsForTheClip(t *testing.T) {
	rig := newCueRig(t)
	rig.e.PlayCue(CueJoin)
	rig.step(3, false) // three frames into the first clip
	rig.e.PlayCue(CueLeave)
	rig.step(cueFrames(CueJoin)+cueFrames(CueLeave)+10, false)

	if got, want := len(rig.audible()), cueFrames(CueJoin)+cueFrames(CueLeave); got != want {
		t.Fatalf("%d audible frames, want %d - the second cue must follow, not replace", got, want)
	}
}

// TestStopDropsAPendingCue: a cue queued while the engine winds down must
// not open the next session with a stale beep.
func TestStopDropsAPendingCue(t *testing.T) {
	e := NewEngine(Config{})
	e.PlayCue(CueJoin)
	e.dropPendingCue() // what Stop does after the DSP goroutine exits
	if _, ok := e.takeCue(); ok {
		t.Fatal("a cue survived the drain")
	}
}

// TestCuePlaysWhileDeafened pins the decision documented in rxPipeline.tick:
// deafen silences other people, not this client's own feedback.
func TestCuePlaysWhileDeafened(t *testing.T) {
	rig := newCueRig(t)
	rig.e.PlayCue(CueMuted)
	want := cueFrames(CueMuted)
	rig.step(want+5, true)

	if got := len(rig.audible()); got != want {
		t.Fatalf("%d audible frames while deafened, want %d", got, want)
	}
}

// cuePeak plays one cue at the given volume and returns the loudest frame.
func cuePeak(t *testing.T, volume float32) float64 {
	t.Helper()
	rig := newCueRig(t)
	rig.e.SetCueVolume(volume)
	rig.e.PlayCue(CueJoin)
	rig.step(cueFrames(CueJoin)+2, false)
	peak := 0.0
	for _, v := range rig.audible() {
		peak = math.Max(peak, v)
	}
	return peak
}

// TestCueVolumeApplies checks that the gain scales the mixed cue linearly
// and that the clamp holds at both ends.
func TestCueVolumeApplies(t *testing.T) {
	full := cuePeak(t, 1)
	if full <= 0 {
		t.Fatal("no audible cue at volume 1")
	}
	quarter := cuePeak(t, 0.25)
	if ratio := full / quarter; math.Abs(ratio-4) > 0.05 {
		t.Errorf("peak ratio between volume 1 and 0.25 is %.3f, want 4", ratio)
	}
	// Above the range the gain clamps to 1 rather than amplifying.
	if over := cuePeak(t, 4); math.Abs(over-full) > 1 {
		t.Errorf("volume 4 peaks at %.0f, want the volume 1 peak %.0f", over, full)
	}
	if off := cuePeak(t, 0); off != 0 {
		t.Errorf("volume 0 still plays at %.0f, want silence", off)
	}
	// A negative setting is a bug upstream, not a licence to invert phase.
	if neg := cuePeak(t, -1); neg != 0 {
		t.Errorf("volume -1 plays at %.0f, want silence", neg)
	}
}

// TestCueTickDoesNotAllocate pins the realtime rule for the receive tick
// with a cue in flight (PLAN.md 4.6): the clip is pre-rendered, so mixing
// it must be a copy and nothing else.
func TestCueTickDoesNotAllocate(t *testing.T) {
	rig := newCueRig(t)
	sink := &countSink{}
	rig.sink = sink
	rig.e.PlayCue(CueJoin)
	rig.e.applyCue(&rig.rx.cues)

	got := testing.AllocsPerRun(500, func() {
		// Keep a cue active for every measured run; the clip is 140 ms and
		// the loop is longer than that.
		if rig.rx.cues.clip == nil {
			rig.e.PlayCue(CueJoin)
		}
		rig.e.applyCue(&rig.rx.cues)
		rig.rx.tick(rig.sink, false, &rig.vols, 0)
	})
	if got != 0 {
		t.Errorf("the RX tick with an active cue allocates %.1f times per frame, want 0", got)
	}
	// Guard against measuring an idle pipeline: the runs above must really
	// have carried cue audio to the sink.
	if sink.audible == 0 {
		t.Fatalf("no cue frames reached the sink across %d ticks", sink.frames)
	}
}

// TestCueClipsStayQuiet is the golden-ish check on the synthesized clips:
// whole frames, silent at both ends (no click), and a peak inside the
// -12 dBFS ceiling at gain 1 while still being loud enough to hear.
func TestCueClipsStayQuiet(t *testing.T) {
	for c, clip := range cueClips {
		if len(clip) == 0 || len(clip)%FrameSamples != 0 {
			t.Fatalf("cue %d: clip is %d samples, want a non-zero multiple of %d",
				c, len(clip), FrameSamples)
		}
		if clip[0] != 0 || clip[len(clip)-1] != 0 {
			t.Errorf("cue %d: clip starts at %d and ends at %d, want silence at both ends",
				c, clip[0], clip[len(clip)-1])
		}
		peak := 0
		for _, s := range clip {
			v := int(s)
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		db := 20 * math.Log10(float64(peak)/fullScale)
		if db > -12 {
			t.Errorf("cue %d: peak %.1f dBFS, want at most -12 dBFS", c, db)
		}
		if db < -20 {
			t.Errorf("cue %d: peak %.1f dBFS, too quiet to notice", c, db)
		}
	}
}

// noteCrossings counts sign changes inside one note of a clip, which tracks
// the note's frequency without a spectrum: a sine crosses zero twice per
// cycle. Zero samples (the fades) are skipped so they cannot fake a change.
func noteCrossings(clip []int16, note, frames int) int {
	n := frames * FrameSamples
	seg := clip[note*n : (note+1)*n]
	crossings, last := 0, 0
	for _, s := range seg {
		sign := 0
		switch {
		case s > 0:
			sign = 1
		case s < 0:
			sign = -1
		default:
			continue
		}
		if last != 0 && sign != last {
			crossings++
		}
		last = sign
	}
	return crossings
}

// TestCueClipsHaveTheRightShape checks the melodic contract the cues carry:
// join rises, leave falls, and the mute pair is low-then-high. A cue that
// played the wrong figure would still pass every level check above.
func TestCueClipsHaveTheRightShape(t *testing.T) {
	joinFirst := noteCrossings(cueClips[CueJoin], 0, cuePairNoteFrames)
	joinSecond := noteCrossings(cueClips[CueJoin], 1, cuePairNoteFrames)
	if joinFirst >= joinSecond {
		t.Errorf("join notes do not rise: %d then %d zero crossings", joinFirst, joinSecond)
	}
	leaveFirst := noteCrossings(cueClips[CueLeave], 0, cuePairNoteFrames)
	leaveSecond := noteCrossings(cueClips[CueLeave], 1, cuePairNoteFrames)
	if leaveFirst <= leaveSecond {
		t.Errorf("leave notes do not fall: %d then %d zero crossings", leaveFirst, leaveSecond)
	}
	muted := noteCrossings(cueClips[CueMuted], 0, cueSingleNoteFrames)
	unmuted := noteCrossings(cueClips[CueUnmuted], 0, cueSingleNoteFrames)
	if muted >= unmuted {
		t.Errorf("the muted cue is not the lower note: %d vs %d zero crossings", muted, unmuted)
	}
}

// waitCueDone polls until the sink has played the whole clip and gone quiet
// again, so the count below does not race the 10 ms ticker.
func waitCueDone(t *testing.T, sink *collectSink, want int) {
	t.Helper()
	const quietTail = 3
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		audible, trailing := 0, 0
		for _, v := range sink.rms {
			if v > 0 {
				audible, trailing = audible+1, 0
			} else if audible > 0 {
				trailing++
			}
		}
		sink.mu.Unlock()
		if audible >= want && trailing >= quietTail {
			return
		}
		time.Sleep(FrameMs * time.Millisecond)
	}
	t.Fatal("the cue never finished playing")
}

// TestEnginePlayCueReachesTheSink is the wiring check through the real DSP
// goroutine: PlayCue from outside must surface as audible playback frames
// with no remote streams and no packets at all.
func TestEnginePlayCueReachesTheSink(t *testing.T) {
	discard := func([]byte, bool) error { return nil }
	e := NewEngine(Config{Send: discard, DSP: &DSPOptions{}, Log: slog.Default()})
	src := &burstSource{toneFrames: 0}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)

	e.PlayCue(CueJoin)
	waitCueDone(t, sink, cueFrames(CueJoin))
	close(stop)
	<-done

	sink.mu.Lock()
	defer sink.mu.Unlock()
	audible := 0
	for _, v := range sink.rms {
		if v > 0 {
			audible++
		}
	}
	if want := cueFrames(CueJoin); audible != want {
		t.Fatalf("%d audible frames, want exactly %d", audible, want)
	}
}
