package audio

import (
	"math"
	"testing"
)

// mixerSine renders a full frame of a sine with the given peak amplitude
// in the int16 scale and an integer number of cycles per frame.
func mixerSine(amp float64, cycles int) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		phase := 2 * math.Pi * float64(cycles) * float64(i) / float64(FrameSamples)
		f[i] = int16(math.Round(amp * math.Sin(phase)))
	}
	return f
}

// mixerConst renders a full frame holding a single value, so a chosen
// volume turns it into an exact target sum.
func mixerConst(v int16) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = v
	}
	return f
}

// mixerGarbage is a non-silent destination buffer, used to prove that Mix
// always overwrites every sample it is given.
func mixerGarbage() []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = int16(i - FrameSamples/2)
	}
	return f
}

func TestMixerSingleSourceIsTransparent(t *testing.T) {
	t.Parallel()

	// Every amplitude here stays below the soft-clip knee, so the mixer
	// must be bit-identical: no compression on the common case.
	tests := []struct {
		name string
		amp  float64
	}{
		{"very quiet", 0.01 * fullScale},
		{"quiet", 0.1 * fullScale},
		{"normal", 0.3 * fullScale},
		{"loud", 0.5 * fullScale},
		{"very loud", 0.8 * fullScale},
		{"just below the knee", 0.89 * fullScale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := mixerSine(tc.amp, 4)
			out := mixerGarbage()

			m := NewMixer()
			m.Add(in, 1.0)
			m.Mix(out)

			for i := range in {
				if out[i] != in[i] {
					t.Fatalf("sample %d: got %d, want %d (bit-identical)", i, out[i], in[i])
				}
			}
		})
	}
}

func TestMixerSumsSources(t *testing.T) {
	t.Parallel()

	a := mixerSine(0.3*fullScale, 3)
	b := mixerSine(0.25*fullScale, 7)
	out := mixerGarbage()

	m := NewMixer()
	m.Add(a, 1.0)
	m.Add(b, 1.0)
	m.Mix(out)

	for i := range out {
		want := int16(a[i]) + int16(b[i]) // peak stays below the knee
		if out[i] != want {
			t.Fatalf("sample %d: got %d, want %d", i, out[i], want)
		}
	}
}

func TestMixerVolumeScales(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		amp    float64
		volume float32
	}{
		{"attenuate", 0.6 * fullScale, 0.5},
		{"quarter", 0.8 * fullScale, 0.25},
		{"amplify", 0.2 * fullScale, 2.0},
		{"unity", 0.4 * fullScale, 1.0},
		{"mute", 0.4 * fullScale, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := mixerSine(tc.amp, 5)
			out := mixerGarbage()

			m := NewMixer()
			m.Add(in, tc.volume)
			m.Mix(out)

			for i := range in {
				want := math.Round(float64(in[i]) * float64(tc.volume))
				if diff := math.Abs(float64(out[i]) - want); diff > 1 {
					t.Fatalf("sample %d: got %d, want %v (+-1)", i, out[i], want)
				}
			}
		})
	}
}

func TestMixerSoftClipLimitsWithoutWrapAround(t *testing.T) {
	t.Parallel()

	// A ramp over the whole int16 range at double volume sweeps the sum
	// from -2.0 to +2.0 full scale, which covers both sides of the knee.
	ramp := make([]int16, FrameSamples)
	for i := range ramp {
		ramp[i] = int16(math.MinInt16 + i*65535/(FrameSamples-1))
	}
	out := make([]int16, FrameSamples)

	m := NewMixer()
	m.Add(ramp, 2.0)
	m.Mix(out)

	prev := int16(math.MinInt16)
	for i, got := range out {
		sum := 2 * float64(ramp[i])
		if sum > 0 && got <= 0 || sum < 0 && got >= 0 {
			t.Fatalf("sample %d: sum %v came out as %d (wrap-around)", i, sum, got)
		}
		if math.Abs(float64(got)) > math.Abs(sum)+1 {
			t.Fatalf("sample %d: |%d| exceeds |sum %v| (gain instead of limiting)", i, got, sum)
		}
		if got < prev {
			t.Fatalf("sample %d: output fell from %d to %d on a rising ramp", i, prev, got)
		}
		prev = got
	}
}

func TestMixerSoftClipShape(t *testing.T) {
	t.Parallel()

	const knee = softClipKnee * fullScale

	tests := []struct {
		name  string
		sumFS float64
		check func(t *testing.T, sum float64, got int16)
	}{
		{
			name:  "below the knee is untouched",
			sumFS: 0.5,
			check: func(t *testing.T, sum float64, got int16) {
				if float64(got) != math.Round(sum) {
					t.Fatalf("got %d, want %v", got, math.Round(sum))
				}
			},
		},
		{
			name:  "at the knee is untouched",
			sumFS: softClipKnee,
			check: func(t *testing.T, sum float64, got int16) {
				if diff := math.Abs(float64(got) - sum); diff > 1 {
					t.Fatalf("got %d, want %v (+-1)", got, sum)
				}
			},
		},
		{
			name:  "just above the knee saturates softly",
			sumFS: 0.95,
			check: func(t *testing.T, sum float64, got int16) {
				if float64(got) >= sum {
					t.Fatalf("got %d, want less than the sum %v", got, sum)
				}
				if float64(got) <= knee {
					t.Fatalf("got %d, want more than the knee %v (hard clip)", got, knee)
				}
			},
		},
		{
			name:  "far above full scale stays inside the range",
			sumFS: 3.0,
			check: func(t *testing.T, _ float64, got int16) {
				// Close to positive full scale: also proves no wrap-around.
				if float64(got) < 0.97*fullScale {
					t.Fatalf("got %d, want close to full scale", got)
				}
			},
		},
		{
			name:  "negative side is limited as well",
			sumFS: -1.5,
			check: func(t *testing.T, _ float64, got int16) {
				// A deep negative peak: also proves no wrap to positive.
				if float64(got) > -softClipKnee*fullScale {
					t.Fatalf("got %d, want a limited negative peak", got)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sum := tc.sumFS * fullScale
			out := mixerGarbage()

			// A constant source of 1 turns the volume into the exact sum.
			m := NewMixer()
			m.Add(mixerConst(1), float32(sum))
			m.Mix(out)

			for i, got := range out {
				if got != out[0] {
					t.Fatalf("sample %d: got %d, want the constant %d", i, got, out[0])
				}
			}
			tc.check(t, sum, out[0])
		})
	}
}

func TestMixerAccumulatorIsResetAfterMix(t *testing.T) {
	t.Parallel()

	m := NewMixer()
	m.Add(mixerSine(0.5*fullScale, 2), 1.0)
	m.Mix(make([]int16, FrameSamples))

	out := mixerGarbage()
	m.Mix(out)
	for i, got := range out {
		if got != 0 {
			t.Fatalf("sample %d: got %d, want silence after the accumulator reset", i, got)
		}
	}
}

func TestMixerWithoutSourcesIsSilence(t *testing.T) {
	t.Parallel()

	m := NewMixer()
	out := mixerGarbage()
	m.Mix(out)
	for i, got := range out {
		if got != 0 {
			t.Fatalf("sample %d: got %d, want silence", i, got)
		}
	}
}

func TestMixerTickAfterTick(t *testing.T) {
	t.Parallel()

	// A source that goes silent for a tick must not leak into the next.
	a := mixerSine(0.4*fullScale, 3)
	out := make([]int16, FrameSamples)

	m := NewMixer()
	for tick := range 4 {
		if tick%2 == 0 {
			m.Add(a, 1.0)
		}
		m.Mix(out)

		for i := range out {
			want := int16(0)
			if tick%2 == 0 {
				want = a[i]
			}
			if out[i] != want {
				t.Fatalf("tick %d sample %d: got %d, want %d", tick, i, out[i], want)
			}
		}
	}
}

func BenchmarkMixerMix(b *testing.B) {
	sources := [][]int16{
		mixerSine(0.3*fullScale, 3),
		mixerSine(0.4*fullScale, 7),
		mixerSine(0.2*fullScale, 11),
	}
	loud := [][]int16{
		mixerSine(0.9*fullScale, 3),
		mixerSine(0.9*fullScale, 7),
		mixerSine(0.9*fullScale, 11),
	}
	out := make([]int16, FrameSamples)

	b.Run("three sources below the knee", func(b *testing.B) {
		m := NewMixer()
		b.ReportAllocs()
		for b.Loop() {
			for _, s := range sources {
				m.Add(s, 1.0)
			}
			m.Mix(out)
		}
	})

	b.Run("three sources into the soft clip", func(b *testing.B) {
		m := NewMixer()
		b.ReportAllocs()
		for b.Loop() {
			for _, s := range loud {
				m.Add(s, 1.0)
			}
			m.Mix(out)
		}
	})

	b.Run("silence", func(b *testing.B) {
		m := NewMixer()
		b.ReportAllocs()
		for b.Loop() {
			m.Mix(out)
		}
	})
}
