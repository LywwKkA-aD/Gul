package audio

import (
	"math"
	"slices"
	"testing"
)

// gateStep is one 10 ms frame offered to the gate.
type gateStep struct {
	prob float32
	ptt  bool
}

// gateVoice repeats n frames of a given speech probability.
func gateVoice(prob float32, n int) []gateStep {
	steps := make([]gateStep, n)
	for i := range steps {
		steps[i].prob = prob
	}
	return steps
}

// gateKey repeats n frames of a given push-to-talk state, with a speech
// probability that must not influence the decision.
func gateKey(prob float32, held bool, n int) []gateStep {
	steps := gateVoice(prob, n)
	for i := range steps {
		steps[i].ptt = held
	}
	return steps
}

// gateRun feeds the steps in order, one per frame, and collects the
// decisions.
func gateRun(g *Gate, steps []gateStep) []bool {
	got := make([]bool, len(steps))
	for i, s := range steps {
		got[i] = g.Update(s.prob, s.ptt)
	}
	return got
}

// gateHold repeats a single decision n times, for readable want slices.
func gateHold(v bool, n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestGateVADTransitions(t *testing.T) {
	t.Parallel()

	// Defaults: open above 0.6, hold at or above 0.4, 30 frames of tail.
	tests := []struct {
		name  string
		steps []gateStep
		want  []bool
	}{
		{
			name:  "silence never opens",
			steps: gateVoice(0, 4),
			want:  gateHold(false, 4),
		},
		{
			name:  "the open threshold itself does not open",
			steps: gateVoice(gateOpenDefault, 4),
			want:  gateHold(false, 4),
		},
		{
			name:  "the hysteresis band does not open a closed gate",
			steps: gateVoice(0.5, 4),
			want:  gateHold(false, 4),
		},
		{
			name:  "the opening frame is transmitted",
			steps: append(gateVoice(0, 2), gateVoice(0.61, 1)...),
			want:  []bool{false, false, true},
		},
		{
			name:  "an open gate holds through the hysteresis band",
			steps: append(gateVoice(0.9, 1), gateVoice(0.5, 40)...),
			want:  gateHold(true, 41),
		},
		{
			name:  "the close threshold itself keeps the gate open",
			steps: append(gateVoice(0.9, 1), gateVoice(gateCloseDefault, 40)...),
			want:  gateHold(true, 41),
		},
		{
			name:  "one frame below close does not close the gate",
			steps: []gateStep{{prob: 0.9}, {prob: 0.1}, {prob: 0.9}},
			want:  []bool{true, true, true},
		},
		{
			name: "reopening after a full close transmits the new opening frame",
			steps: slices.Concat(
				gateVoice(0.9, 1),
				gateVoice(0, 31), // 30 frames of tail, then closed
				gateVoice(0.9, 1),
			),
			want: slices.Concat(
				gateHold(true, 31),
				[]bool{false},
				[]bool{true},
			),
		},
		{
			name:  "NaN is treated as no speech",
			steps: []gateStep{{prob: float32(math.NaN())}, {prob: float32(math.NaN())}},
			want:  []bool{false, false},
		},
		{
			name:  "push-to-talk is ignored in VAD mode",
			steps: gateKey(0, true, 4),
			want:  gateHold(false, 4),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := gateRun(NewGate(), tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("decisions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGateHangoverExpiresOnTheExactFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ms   int
		want int // tail frames transmitted after the last speech frame
	}{
		{"no hangover closes on the first quiet frame", 0, 0},
		{"one frame", 10, 1},
		{"the default 300 ms", gateHangoverDefaultMs, gateHangoverDefaultMs / FrameMs},
		{"rounded down to whole frames", 35, 3},
		{"clamped to the ceiling", 60_000, gateHangoverMaxMs / FrameMs},
		{"negative is clamped to zero", -100, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewGate()
			g.SetHangoverMs(tc.ms)
			if !g.Update(0.9, false) {
				t.Fatalf("the opening frame was not transmitted")
			}
			for i := 1; i <= tc.want; i++ {
				if !g.Update(0, false) {
					t.Fatalf("quiet frame %d of %d closed the gate early", i, tc.want)
				}
			}
			if g.Update(0, false) {
				t.Fatalf("gate still open after %d tail frames", tc.want)
			}
		})
	}
}

func TestGateHangoverRefreshesInFull(t *testing.T) {
	t.Parallel()

	const tail = gateHangoverDefaultMs / FrameMs

	g := NewGate()
	g.Update(0.9, false)
	for i := range tail - 1 {
		if !g.Update(0, false) {
			t.Fatalf("quiet frame %d closed the gate early", i+1)
		}
	}

	// A single frame back above the close threshold, one frame before the
	// tail would have run out: the gap inside the phrase must not count
	// towards closing it.
	if !g.Update(gateCloseDefault, false) {
		t.Fatalf("a frame at the close threshold closed the gate")
	}
	for i := 1; i <= tail; i++ {
		if !g.Update(0, false) {
			t.Fatalf("quiet frame %d of the refreshed tail closed the gate early", i)
		}
	}
	if g.Update(0, false) {
		t.Fatalf("gate still open after the refreshed tail ran out")
	}
}

func TestGateShorterHangoverAppliesToTheRunningTail(t *testing.T) {
	t.Parallel()

	g := NewGate()
	g.Update(0.9, false)
	g.Update(0, false) // one frame into the 300 ms tail

	g.SetHangoverMs(20)
	for i := 1; i <= 2; i++ {
		if !g.Update(0, false) {
			t.Fatalf("quiet frame %d closed the gate before the shortened tail ran out", i)
		}
	}
	if g.Update(0, false) {
		t.Fatalf("gate still open after the shortened tail ran out")
	}
}

func TestGatePTT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps []gateStep
		want  []bool
	}{
		{
			name:  "held transmits regardless of a silent probability",
			steps: gateKey(0, true, 4),
			want:  gateHold(true, 4),
		},
		{
			name:  "released stays silent regardless of speech",
			steps: gateKey(1, false, 4),
			want:  gateHold(false, 4),
		},
		{
			name:  "release stops on the same frame, with no tail",
			steps: slices.Concat(gateKey(0.9, true, 3), gateKey(0.9, false, 3)),
			want:  slices.Concat(gateHold(true, 3), gateHold(false, 3)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewGate()
			g.SetMode(GatePTT)
			got := gateRun(g, tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("decisions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGateModeSwitchResets(t *testing.T) {
	t.Parallel()

	g := NewGate()
	if !g.Update(0.9, false) {
		t.Fatalf("the opening frame was not transmitted")
	}

	// The VAD tail must not survive into push-to-talk: the key is not held.
	g.SetMode(GatePTT)
	if g.Update(0.9, false) {
		t.Fatalf("the VAD hangover leaked into PTT mode")
	}

	// Nor back the other way: a released key leaves nothing open, and the
	// VAD gate starts closed.
	g.SetMode(GateVAD)
	if g.Update(0.5, false) {
		t.Fatalf("the gate reopened without a frame above the open threshold")
	}
}

func TestGateSameModeDoesNotReset(t *testing.T) {
	t.Parallel()

	// The engine may push the configured mode every tick, so a no-op set
	// must not truncate a running transmission.
	g := NewGate()
	g.Update(0.9, false)
	g.SetMode(GateVAD)
	if !g.Update(0, false) {
		t.Fatalf("setting the current mode closed the gate")
	}

	g.SetMode(GateMode(42))
	if !g.Update(0, false) {
		t.Fatalf("an unknown mode was accepted and reset the gate")
	}
	if g.mode != GateVAD {
		t.Fatalf("mode = %d after an unknown value, want GateVAD", g.mode)
	}
}

func TestGateSetThresholdsClamping(t *testing.T) {
	t.Parallel()

	nan := float32(math.NaN())
	tests := []struct {
		name                string
		open, close         float32
		wantOpen, wantClose float32
	}{
		{"in range", 0.8, 0.2, 0.8, 0.2},
		{"below zero", -1, -2, 0, 0},
		{"above one", 2, 1.5, 1, 1},
		{"inverted pair pulls close down to open", 0.3, 0.8, 0.3, 0.3},
		{"equal pair is allowed", 0.5, 0.5, 0.5, 0.5},
		{"NaN pair keeps the previous band", nan, nan, gateOpenDefault, gateCloseDefault},
		{"NaN open keeps the previous band", nan, 0.2, gateOpenDefault, gateCloseDefault},
		{"NaN close keeps the previous band", 0.8, nan, gateOpenDefault, gateCloseDefault},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewGate()
			g.SetThresholds(tc.open, tc.close)
			if g.openLevel != tc.wantOpen || g.closeLevel != tc.wantClose {
				t.Fatalf("thresholds = (%v, %v), want (%v, %v)",
					g.openLevel, g.closeLevel, tc.wantOpen, tc.wantClose)
			}
			if g.closeLevel > g.openLevel {
				t.Fatalf("close %v above open %v: the hysteresis band is inverted",
					g.closeLevel, g.openLevel)
			}
		})
	}
}

func TestGateSetThresholdsChangesBehaviour(t *testing.T) {
	t.Parallel()

	g := NewGate()
	g.SetThresholds(0.9, 0.1)
	g.SetHangoverMs(0)

	if g.Update(0.8, false) {
		t.Fatalf("a frame below the raised open threshold opened the gate")
	}
	if !g.Update(0.95, false) {
		t.Fatalf("a frame above the raised open threshold did not open the gate")
	}
	if !g.Update(0.15, false) {
		t.Fatalf("a frame above the lowered close threshold closed the gate")
	}
	if g.Update(0.05, false) {
		t.Fatalf("a frame below the lowered close threshold did not close the gate")
	}
}

func TestGateSetHangoverMsClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ms   int
		want int // frames
	}{
		{"zero", 0, 0},
		{"below one frame", 5, 0},
		{"one frame", 10, 1},
		{"default", 300, 30},
		{"ceiling", gateHangoverMaxMs, gateHangoverMaxMs / FrameMs},
		{"above the ceiling", gateHangoverMaxMs * 10, gateHangoverMaxMs / FrameMs},
		{"negative", -1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewGate()
			g.SetHangoverMs(tc.ms)
			if g.hangover != tc.want {
				t.Fatalf("hangover = %d frames, want %d", g.hangover, tc.want)
			}
		})
	}
}

func TestGateReset(t *testing.T) {
	t.Parallel()

	g := NewGate()
	g.SetThresholds(0.8, 0.2)
	g.SetHangoverMs(100)
	if !g.Update(0.9, false) {
		t.Fatalf("the opening frame was not transmitted")
	}

	g.Reset()
	if g.Update(0, false) {
		t.Fatalf("the gate is still open after Reset")
	}

	// Settings are not state: they survive.
	if g.Update(0.5, false) {
		t.Fatalf("a frame below the configured open threshold opened the gate")
	}
	if !g.Update(0.85, false) {
		t.Fatalf("the configured open threshold was lost across Reset")
	}
	for i := 1; i <= 10; i++ {
		if !g.Update(0, false) {
			t.Fatalf("quiet frame %d closed the gate: the configured tail was lost across Reset", i)
		}
	}
	if g.Update(0, false) {
		t.Fatalf("gate still open after the configured 100 ms tail")
	}
}

func TestGatePhraseShape(t *testing.T) {
	t.Parallel()

	// A phrase with an internal pause shorter than the tail is one
	// transmission, and the trailing tail is exactly the configured length.
	const tail = gateHangoverDefaultMs / FrameMs

	steps := slices.Concat(
		gateVoice(0, 5),    // room tone before the phrase
		gateVoice(0.95, 8), // first word
		gateVoice(0.05, 5), // a stop inside the phrase
		gateVoice(0.95, 8), // second word
		gateVoice(0, tail+1),
	)
	want := slices.Concat(
		gateHold(false, 5),
		gateHold(true, 8+5+8+tail),
		[]bool{false},
	)

	got := gateRun(NewGate(), steps)
	if !slices.Equal(got, want) {
		t.Fatalf("decisions = %v, want %v", got, want)
	}
}

func BenchmarkGateUpdate(b *testing.B) {
	g := NewGate()

	b.ReportAllocs()
	for b.Loop() {
		g.Update(0.7, false)
	}
}
