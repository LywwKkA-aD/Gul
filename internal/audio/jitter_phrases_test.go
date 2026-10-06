package audio

import "testing"

func TestJitterPhraseCompletedPrefixStartsBelowTarget(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	jitterPrime(j, 0, 2)
	j.PushFinal(1)
	if base := j.BeginPhrase(); base != 2 {
		t.Fatalf("next phrase base = %d, want 2", base)
	}
	j.Push(2, jitterFrameFor(2))
	// The old phrase must finish promptly even though the open next phrase
	// has not supplied enough audio to fill the normal starting reserve.
	for seq := int64(0); seq < 3; seq++ {
		jitterPopFrame(t, j, dst, "adjacent phrase", seq)
	}
	if got := j.Pop(dst); got != JitterConceal {
		t.Fatalf("open phrase ran dry: got %s, want conceal", jitterResultName(got))
	}
	if j.Depth() != jitterStartFrames+1 || j.Counts().Stalls != 1 {
		t.Fatalf("real stall did not adapt: depth=%d counts=%+v", j.Depth(), j.Counts())
	}
	j.Push(3, jitterFrameFor(3))
	if got := j.Pop(dst); got != JitterConceal {
		t.Fatalf("consumed prefix bypassed stall priming: got %s", jitterResultName(got))
	}
	for seq := int64(4); seq < int64(3+j.Depth()); seq++ {
		j.Push(seq, jitterFrameFor(seq))
	}
	jitterPopFrame(t, j, dst, "recovered open phrase", 3)
}

func TestJitterPhraseCompletedPrefixIsNotTrimmed(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	const completed = 90
	jitterPrime(j, 0, completed)
	j.PushFinal(completed - 1)
	base := j.BeginPhrase()
	if base != completed {
		t.Fatalf("next phrase base = %d, want %d", base, completed)
	}
	j.Push(base, jitterFrameFor(base)) // keep the most recent phrase open
	for seq := int64(0); seq <= completed; seq++ {
		jitterPopFrame(t, j, dst, "completed prefix", seq)
	}
	if got := j.Counts(); got != (JitterCounts{Played: completed + 1}) {
		t.Fatalf("completed speech was discarded or concealed: %+v", got)
	}
}

func TestJitterPhraseManyCompletedPhrasesKeepEveryFrame(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	for phrase := range jitterRingFrames / 2 {
		base := j.BeginPhrase()
		if want := int64(phrase * 2); base != want {
			t.Fatalf("phrase %d base = %d, want %d", phrase, base, want)
		}
		jitterPrime(j, base, 2)
		j.PushFinal(base + 1)
	}
	for seq := int64(0); seq < jitterRingFrames; seq++ {
		jitterPopFrame(t, j, dst, "queued phrases", seq)
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("all phrases ended: got %s, want idle", jitterResultName(got))
	}
	if got := j.Counts(); got != (JitterCounts{Played: jitterRingFrames}) {
		t.Fatalf("lossless phrases changed counters: %+v", got)
	}
}

func TestJitterPhraseOverflowKeepsNewestFramesAndCountsLoss(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	const excess = 20
	for phrase := range jitterRingFrames + excess {
		base := j.BeginPhrase()
		if base != int64(phrase) {
			t.Fatalf("phrase %d base = %d", phrase, base)
		}
		j.Push(base, jitterFrameFor(base))
		j.PushFinal(base)
		if j.count > jitterRingFrames {
			t.Fatalf("buffer grew to %d frames, limit %d", j.count, jitterRingFrames)
		}
	}
	for seq := int64(excess); seq < jitterRingFrames+excess; seq++ {
		jitterPopFrame(t, j, dst, "bounded phrase backlog", seq)
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("backlog drained: got %s, want idle", jitterResultName(got))
	}
	want := JitterCounts{Played: jitterRingFrames, Overflow: excess}
	if got := j.Counts(); got != want {
		t.Fatalf("overflow counts = %+v, want %+v", got, want)
	}
}

func TestJitterPhraseLateFinalKeepsAlreadyAcceptedTail(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	jitterPrime(j, 0, jitterStartFrames)
	j.PushFinal(3)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "accepted tail", seq)
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("accepted tail drained: got %s, want idle", jitterResultName(got))
	}
	if got := j.Counts(); got != (JitterCounts{Played: jitterStartFrames}) {
		t.Fatalf("late final damaged queued audio: %+v", got)
	}
}

func TestJitterPhraseBoundaryPreservesLearnedDepth(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)
	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "before stall", seq)
	}
	for range 3 {
		if got := j.Pop(dst); got != JitterConceal {
			t.Fatalf("stall gave %s, want conceal", jitterResultName(got))
		}
	}
	depth := j.Depth()
	if depth <= jitterStartFrames {
		t.Fatal("test did not build a deeper reserve")
	}
	jitterPrime(j, jitterStartFrames, depth)
	jitterPopFrame(t, j, dst, "after stall", jitterStartFrames)
	last := int64(jitterStartFrames + depth - 1)
	j.PushFinal(last)
	base := j.BeginPhrase()
	if base != last+1 || j.Depth() != depth {
		t.Fatalf("phrase reset learned state: base=%d depth=%d, want %d/%d", base, j.Depth(), last+1, depth)
	}
	j.Push(base, jitterFrameFor(base))
	j.PushFinal(base)
	for seq := int64(jitterStartFrames + 1); seq <= base; seq++ {
		jitterPopFrame(t, j, dst, "after phrase boundary", seq)
	}
	if j.Depth() != depth {
		t.Fatalf("depth after adjacent phrases = %d, want %d", j.Depth(), depth)
	}
}

func TestJitterPhraseResetAndRestartForgetCompletedPrefix(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"reset", "sequence restart"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			j := NewJitter()
			dst := make([]int16, FrameSamples)
			jitterPrime(j, 1000, 2)
			j.PushFinal(1001)
			base := j.BeginPhrase()
			j.Push(base, jitterFrameFor(base))
			if action == "reset" {
				j.Reset()
			}
			// With no explicit BeginPhrase, this large backwards jump is a
			// real restart. Neither path may inherit the old completed prefix.
			j.Push(0, jitterFrameFor(0))
			if got := j.Pop(dst); got != JitterIdle {
				t.Fatalf("%s retained prefix readiness: got %s", action, jitterResultName(got))
			}
			jitterPrime(j, 1, jitterStartFrames-1)
			for seq := int64(0); seq < jitterStartFrames; seq++ {
				jitterPopFrame(t, j, dst, "fresh stream", seq)
			}
		})
	}
}

func TestJitterPhraseQueueDoesNotAllocatePerFrame(t *testing.T) {
	j := NewJitter()
	frame := make([]int16, FrameSamples)
	dst := make([]int16, FrameSamples)
	const frames = 80
	valid := true
	allocs := testing.AllocsPerRun(20, func() {
		j.Reset()
		for range frames / 2 {
			base := j.BeginPhrase()
			j.Push(base, frame)
			j.Push(base+1, frame)
			j.PushFinal(base + 1)
		}
		for range frames {
			if j.Pop(dst) != JitterFrame {
				valid = false
			}
		}
		if j.Pop(dst) != JitterIdle {
			valid = false
		}
	})
	if !valid {
		t.Fatal("allocation probe failed to play every queued frame")
	}
	if allocs != 0 {
		t.Fatalf("queueing and playing adjacent phrases allocated %.2f times, want zero", allocs)
	}
}
