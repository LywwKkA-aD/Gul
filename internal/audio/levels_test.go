package audio

import (
	"math"
	"testing"
)

// levelsSine renders a frame with an integer number of cycles, so its RMS
// is the analytic amp/sqrt(2) up to int16 rounding.
func levelsSine(amp float64, cycles int) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		phase := 2 * math.Pi * float64(cycles) * float64(i) / float64(FrameSamples)
		f[i] = int16(math.Round(amp * math.Sin(phase)))
	}
	return f
}

func levelsConst(v int16) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestRMS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pcm  []int16
		want float64
		tol  float64
	}{
		{"empty frame", nil, 0, 0},
		{"silence", make([]int16, FrameSamples), 0, 0},
		{"dc offset", levelsConst(1000), 1000, 0},
		{"negative dc offset", levelsConst(-1000), 1000, 0},
		{"full scale square", levelsConst(math.MaxInt16), math.MaxInt16, 0},
		{"full scale sine", levelsSine(math.MaxInt16, 4), math.MaxInt16 / math.Sqrt2, 1},
		{"half scale sine", levelsSine(fullScale/2, 4), fullScale / 2 / math.Sqrt2, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := RMS(tc.pcm)
			if math.Abs(got-tc.want) > tc.tol {
				t.Fatalf("RMS = %v, want %v (+-%v)", got, tc.want, tc.tol)
			}
		})
	}
}

func TestDBFS(t *testing.T) {
	t.Parallel()

	const tol = 0.05

	tests := []struct {
		name string
		rms  float64
		want float64
	}{
		// 0 dBFS is defined as a sine spanning full scale.
		{"full scale sine", fullScale / math.Sqrt2, 0},
		{"half amplitude sine", fullScale / 2 / math.Sqrt2, -6.0206},
		{"tenth amplitude sine", fullScale / 10 / math.Sqrt2, -20},
		// A square wave carries more power than a sine of the same peak.
		{"full scale square", fullScale, 3.0103},
		{"silence", 0, dbfsFloor},
		{"below the floor", 0.1, dbfsFloor},
		{"exactly at the floor", fullScale / math.Sqrt2 * math.Pow(10, dbfsFloor/20), dbfsFloor},
		{"negative input", -1, dbfsFloor},
		{"nan input", math.NaN(), dbfsFloor},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := DBFS(tc.rms)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("DBFS(%v) = %v, want a finite value", tc.rms, got)
			}
			if got < dbfsFloor {
				t.Fatalf("DBFS(%v) = %v, want at least the floor %v", tc.rms, got, dbfsFloor)
			}
			if math.Abs(got-tc.want) > tol {
				t.Fatalf("DBFS(%v) = %v, want %v (+-%v)", tc.rms, got, tc.want, tol)
			}
		})
	}
}

func TestDBFSOfFullScaleSineFrameIsZero(t *testing.T) {
	t.Parallel()

	// The end to end path a meter takes: frame -> RMS -> dBFS.
	got := DBFS(RMS(levelsSine(math.MaxInt16, 4)))
	if math.Abs(got) > 0.05 {
		t.Fatalf("full scale sine measured %v dBFS, want 0 (+-0.05)", got)
	}

	silent := DBFS(RMS(make([]int16, FrameSamples)))
	if silent != dbfsFloor {
		t.Fatalf("silence measured %v dBFS, want the floor %v", silent, dbfsFloor)
	}
}

func TestDBFSIsMonotonic(t *testing.T) {
	t.Parallel()

	prev := math.Inf(-1)
	for amp := 1.0; amp < fullScale; amp *= 1.5 {
		got := DBFS(RMS(levelsSine(amp, 4)))
		if got < prev {
			t.Fatalf("amplitude %v measured %v dBFS, below the previous %v", amp, got, prev)
		}
		prev = got
	}
}

func BenchmarkRMS(b *testing.B) {
	frame := levelsSine(0.3*fullScale, 4)
	b.ReportAllocs()
	for b.Loop() {
		DBFS(RMS(frame))
	}
}
