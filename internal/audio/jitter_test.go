package audio

import "testing"

// jitterMarker gives every sequence number a distinct sample value so that a
// popped frame can be matched back to the frame that was pushed.
func jitterMarker(seq int64) int16 { return int16(seq%3000) + 1 }

func jitterFilled(v int16) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = v
	}
	return f
}

func jitterFrameFor(seq int64) []int16 { return jitterFilled(jitterMarker(seq)) }

func jitterResultName(r JitterResult) string {
	switch r {
	case JitterFrame:
		return "JitterFrame"
	case JitterConceal:
		return "JitterConceal"
	case JitterIdle:
		return "JitterIdle"
	default:
		return "JitterResult(unknown)"
	}
}

func jitterCheckFrame(t *testing.T, ctx string, dst []int16, want int16) {
	t.Helper()
	for i, v := range dst {
		if v != want {
			t.Fatalf("%s: dst[%d] = %d, want every sample %d", ctx, i, v, want)
		}
	}
}

// jitterPopFrame asserts that the tick yields the frame carrying seq.
func jitterPopFrame(t *testing.T, j *Jitter, dst []int16, ctx string, seq int64) {
	t.Helper()
	if got := j.Pop(dst); got != JitterFrame {
		t.Fatalf("%s: Pop = %s, want JitterFrame (seq %d)", ctx, jitterResultName(got), seq)
	}
	jitterCheckFrame(t, ctx, dst, jitterMarker(seq))
}

// jitterPrime fills the buffer with count frames starting at seq, which is what
// the start depth requires before playout may begin.
func jitterPrime(j *Jitter, seq int64, count int) {
	for i := 0; i < count; i++ {
		j.Push(seq+int64(i), jitterFrameFor(seq+int64(i)))
	}
}

// jitterStep drives one 10 ms tick: everything in push (and final) arrives
// first, then Pop is expected to answer want (carrying wantSeq for a frame).
type jitterStep struct {
	push    []int64
	final   []int64
	want    JitterResult
	wantSeq int64
}

func TestJitterScenarios(t *testing.T) {
	t.Parallel()

	// jitterStartFrames frames are needed before the first frame is handed out.
	fill := func(from, n int64) []int64 {
		seqs := make([]int64, 0, n)
		for i := int64(0); i < n; i++ {
			seqs = append(seqs, from+i)
		}
		return seqs
	}

	tests := []struct {
		name  string
		steps []jitterStep
	}{
		{
			name: "happy path in order",
			steps: []jitterStep{
				{push: fill(0, jitterStartFrames), want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterFrame, wantSeq: 3},
				{want: JitterFrame, wantSeq: 4},
				{want: JitterFrame, wantSeq: 5},
				{want: JitterFrame, wantSeq: 6},
				{want: JitterFrame, wantSeq: 7},
				{want: JitterConceal}, // ran dry, stream still open
			},
		},
		{
			name: "start depth withholds playout",
			steps: []jitterStep{
				{push: []int64{0}, want: JitterIdle},
				{push: []int64{1}, want: JitterIdle},
				{push: []int64{2}, want: JitterIdle},
				{push: []int64{3}, want: JitterIdle},
				{push: []int64{4}, want: JitterIdle},
				{push: []int64{5}, want: JitterIdle},
				{push: []int64{6}, want: JitterIdle},
				{push: []int64{7}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
			},
		},
		{
			name: "reordering inside the buffer",
			steps: []jitterStep{
				{push: []int64{3, 1, 0, 6, 2, 7, 5, 4}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterFrame, wantSeq: 3},
				{want: JitterFrame, wantSeq: 4},
				{want: JitterFrame, wantSeq: 5},
				{want: JitterFrame, wantSeq: 6},
				{want: JitterFrame, wantSeq: 7},
			},
		},
		{
			name: "missing sequence conceals exactly one slot",
			steps: []jitterStep{
				{push: []int64{0, 1, 2, 4, 5, 6, 7, 8}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterConceal}, // seq 3 never arrived
				{want: JitterFrame, wantSeq: 4},
				{want: JitterFrame, wantSeq: 5},
				{want: JitterFrame, wantSeq: 6},
				{want: JitterFrame, wantSeq: 7},
				{want: JitterFrame, wantSeq: 8},
			},
		},
		{
			name: "late frame for a played slot is ignored",
			steps: []jitterStep{
				{push: fill(0, jitterStartFrames), want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{push: []int64{0}, want: JitterFrame, wantSeq: 2},
				{want: JitterFrame, wantSeq: 3},
				{want: JitterFrame, wantSeq: 4},
				{want: JitterFrame, wantSeq: 5},
				{want: JitterFrame, wantSeq: 6},
				{want: JitterFrame, wantSeq: 7},
				{want: JitterConceal},
			},
		},
		{
			name: "terminator drains below the start depth",
			steps: []jitterStep{
				{push: []int64{0, 1}, want: JitterIdle},
				{push: []int64{2}, final: []int64{2}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterIdle},
				{want: JitterIdle},
			},
		},
		{
			name: "terminator conceals the tail it announced",
			steps: []jitterStep{
				{push: []int64{0, 1, 2, 3}, final: []int64{5}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterFrame, wantSeq: 3},
				{want: JitterConceal}, // seq 4 was sent but never made it in
				{want: JitterConceal}, // seq 5 likewise
				{want: JitterIdle},
			},
		},
		{
			name: "new transmission restarts at sequence zero",
			steps: []jitterStep{
				{push: []int64{0, 1}, final: []int64{1}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterIdle},
				{push: []int64{0, 1, 2}, final: []int64{2}, want: JitterFrame, wantSeq: 0},
				{want: JitterFrame, wantSeq: 1},
				{want: JitterFrame, wantSeq: 2},
				{want: JitterIdle},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j := NewJitter()
			dst := make([]int16, FrameSamples)
			for i, s := range tc.steps {
				for _, seq := range s.push {
					j.Push(seq, jitterFrameFor(seq))
				}
				for _, seq := range s.final {
					j.PushFinal(seq)
				}
				got := j.Pop(dst)
				if got != s.want {
					t.Fatalf("step %d: Pop = %s, want %s", i, jitterResultName(got), jitterResultName(s.want))
				}
				if got == JitterFrame {
					jitterCheckFrame(t, "step", dst, jitterMarker(s.wantSeq))
				}
			}
		})
	}
}

func TestJitterDuplicateKeepsFirstCopy(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	const poison = -321
	j.Push(3, jitterFilled(poison)) // duplicate of a queued frame
	j.Push(0, jitterFilled(poison)) // duplicate of the head

	if j.count != jitterStartFrames {
		t.Fatalf("queued frames = %d, want %d (duplicates must not be stored)", j.count, jitterStartFrames)
	}
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "duplicate run", seq)
	}
	if got := j.Pop(dst); got != JitterConceal {
		t.Fatalf("after drain: Pop = %s, want JitterConceal", jitterResultName(got))
	}
}

func TestJitterBurstAfterStall(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "first spurt", seq)
	}
	if j.Depth() != jitterStartFrames {
		t.Fatalf("depth before the stall = %d, want %d", j.Depth(), jitterStartFrames)
	}

	// Head-of-line blocking: 300 ms without a single arrival.
	const stallTicks = 30
	for i := 0; i < stallTicks; i++ {
		if got := j.Pop(dst); got != JitterConceal {
			t.Fatalf("stall tick %d: Pop = %s, want JitterConceal", i, jitterResultName(got))
		}
	}
	wantDepth := jitterStartFrames + stallTicks
	if j.Depth() != wantDepth {
		t.Fatalf("depth after the stall = %d, want %d", j.Depth(), wantDepth)
	}

	// The stalled segment lands in one burst; not a frame of it may be lost.
	const burst = 40
	for i := 0; i < burst; i++ {
		seq := int64(jitterStartFrames + i)
		j.Push(seq, jitterFrameFor(seq))
	}
	for i := 0; i < burst; i++ {
		jitterPopFrame(t, j, dst, "burst", int64(jitterStartFrames+i))
	}
}

func TestJitterDepthIsCapped(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	// A stall longer than the ceiling, still shorter than the silence timeout.
	for i := 0; i < jitterMaxFrames+20; i++ {
		if got := j.Pop(dst); got != JitterConceal {
			t.Fatalf("stall tick %d: Pop = %s, want JitterConceal", i, jitterResultName(got))
		}
	}
	if j.Depth() != jitterMaxFrames {
		t.Fatalf("depth = %d, want the ceiling %d", j.Depth(), jitterMaxFrames)
	}
}

func TestJitterShrinksAfterStablePeriod(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	const stallTicks = 5
	for i := 0; i < stallTicks; i++ {
		if got := j.Pop(dst); got != JitterConceal {
			t.Fatalf("stall tick %d: Pop = %s, want JitterConceal", i, jitterResultName(got))
		}
	}
	grown := jitterStartFrames + stallTicks
	if j.Depth() != grown {
		t.Fatalf("grown depth = %d, want %d", j.Depth(), grown)
	}

	// Refill to the new depth, then run a long stable period of one arrival
	// per tick: the target must decay by exactly one frame per shrink window.
	seq := int64(jitterStartFrames)
	jitterPrime(j, seq, grown)
	seq += int64(grown)

	const windows = 3
	for i := 0; i < windows*jitterShrinkTicks; i++ {
		j.Push(seq, jitterFrameFor(seq))
		seq++
		if got := j.Pop(dst); got != JitterFrame {
			t.Fatalf("stable tick %d: Pop = %s, want JitterFrame (depth %d)", i, jitterResultName(got), j.Depth())
		}
	}
	if want := grown - windows; j.Depth() != want {
		t.Fatalf("depth after %d stable ticks = %d, want %d", windows*jitterShrinkTicks, j.Depth(), want)
	}
}

func TestJitterTrimsExcessQueue(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	// A burst far above the ceiling: playout must claw the latency back
	// without ever concealing, i.e. by skipping single queued frames.
	const burst = 90
	jitterPrime(j, 0, burst)
	if j.count != burst {
		t.Fatalf("queued frames = %d, want %d", j.count, burst)
	}

	const ticks = 40
	var last int64
	for i := 0; i < ticks; i++ {
		if got := j.Pop(dst); got != JitterFrame {
			t.Fatalf("tick %d: Pop = %s, want JitterFrame", i, jitterResultName(got))
		}
		last = j.next - 1
	}
	if j.count >= burst-ticks {
		t.Fatalf("queued frames = %d, want fewer than %d (excess must be trimmed)", j.count, burst-ticks)
	}
	if last < ticks {
		t.Fatalf("last played sequence = %d, want more than %d (trim must advance playout)", last, ticks)
	}
	if j.count < j.Depth() {
		t.Fatalf("queued frames = %d, trimmed below the target depth %d", j.count, j.Depth())
	}
}

func TestJitterSilenceEndsStream(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	// No terminator ever arrives: silence alone must close the stream.
	idleAt := -1
	for i := 0; i < 4*jitterSilenceTicks; i++ {
		if j.Pop(dst) == JitterIdle {
			idleAt = i
			break
		}
	}
	if idleAt < 0 {
		t.Fatal("stream never went idle after a long silence")
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("Pop after idle = %s, want JitterIdle", jitterResultName(got))
	}
	// A finished talk spurt is not a stalled link: the depth speculatively
	// grown while waiting must be handed back.
	if j.Depth() != jitterStartFrames {
		t.Fatalf("depth after a plain end of speech = %d, want %d", j.Depth(), jitterStartFrames)
	}

	// The buffer is armed again, including for a sequence starting at zero.
	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "second spurt", seq)
	}
}

func TestJitterRestartOnSequenceJump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    int64
		restart int64
	}{
		{name: "far forward", base: 1000, restart: 1000 + jitterRestartGap + 7},
		{name: "back to zero", base: 5000, restart: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j := NewJitter()
			dst := make([]int16, FrameSamples)

			jitterPrime(j, tc.base, jitterStartFrames)
			jitterPopFrame(t, j, dst, "old stream", tc.base)
			jitterPopFrame(t, j, dst, "old stream", tc.base+1)

			// A jump this large is a new transmission, not a reordering.
			jitterPrime(j, tc.restart, jitterStartFrames)
			for i := int64(0); i < jitterStartFrames; i++ {
				jitterPopFrame(t, j, dst, "new stream", tc.restart+i)
			}
			if got := j.Pop(dst); got != JitterConceal {
				t.Fatalf("after the new stream drained: Pop = %s, want JitterConceal", jitterResultName(got))
			}
		})
	}
}

func TestJitterReset(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	for i := 0; i < 12; i++ {
		j.Pop(dst) // starve the buffer so the target grows away from the default
	}
	if j.Depth() == jitterStartFrames {
		t.Fatal("depth did not grow during the stall, nothing to reset")
	}

	j.Reset()
	if j.Depth() != jitterStartFrames {
		t.Fatalf("depth after Reset = %d, want %d", j.Depth(), jitterStartFrames)
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("Pop after Reset = %s, want JitterIdle", jitterResultName(got))
	}
	jitterPrime(j, 0, jitterStartFrames)
	jitterPopFrame(t, j, dst, "after Reset", 0)
}

func TestJitterFrameLengthMismatch(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	// First transmission dirties the ring slots.
	jitterPrime(j, 0, jitterStartFrames)
	j.PushFinal(int64(jitterStartFrames - 1))
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "first spurt", seq)
	}
	if got := j.Pop(dst); got != JitterIdle {
		t.Fatalf("after the terminator: Pop = %s, want JitterIdle", jitterResultName(got))
	}

	// A short frame must be zero padded, a long one truncated, and neither may
	// panic the DSP goroutine.
	const short = 200
	j.Push(0, jitterFilled(7)[:short])
	j.Push(1, make([]int16, FrameSamples+120))
	jitterPrime(j, 2, jitterStartFrames-2)

	if got := j.Pop(dst); got != JitterFrame {
		t.Fatalf("short frame: Pop = %s, want JitterFrame", jitterResultName(got))
	}
	for i, v := range dst {
		want := int16(0)
		if i < short {
			want = 7
		}
		if v != want {
			t.Fatalf("short frame: dst[%d] = %d, want %d", i, v, want)
		}
	}
	if got := j.Pop(dst); got != JitterFrame {
		t.Fatalf("long frame: Pop = %s, want JitterFrame", jitterResultName(got))
	}
	jitterCheckFrame(t, "long frame", dst, 0)
}

func BenchmarkJitterSteadyState(b *testing.B) {
	j := NewJitter()
	src := jitterFilled(1234)
	dst := make([]int16, FrameSamples)

	var seq int64
	for ; seq < int64(jitterStartFrames); seq++ {
		j.Push(seq, src)
	}

	b.ReportAllocs()
	for b.Loop() {
		j.Push(seq, src)
		seq++
		if got := j.Pop(dst); got != JitterFrame {
			b.Fatalf("Pop = %s, want JitterFrame", jitterResultName(got))
		}
	}
}
