package apm_test

import (
	"errors"
	"math"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
)

const framesPerSecond = 100 // 10 ms frames

// rng is a deterministic linear congruential generator: the DSP tests must
// produce the same numbers on every machine and every run.
type rng struct{ state uint64 }

func newRNG() *rng { return &rng{state: 0x2545f4914f6cdd1d} }

// float returns the next sample in [-1, 1).
func (r *rng) float() float64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return float64(int32(r.state>>32)) / float64(1<<31)
}

func rms(frame []int16) float64 {
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(frame)))
}

func clip(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

func newAPM(t *testing.T, cfg apm.Config) *apm.APM {
	t.Helper()
	p, err := apm.New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestNewRejectsUnknownNSLevel(t *testing.T) {
	t.Parallel()
	if p, err := apm.New(apm.Config{NS: apm.NSLevel(42)}); err == nil {
		p.Close()
		t.Fatal("New accepted NSLevel(42), want an error")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	p, err := apm.New(apm.Config{EchoCancel: true, NS: apm.NSModerate})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestUseAfterClose(t *testing.T) {
	t.Parallel()
	p, err := apm.New(apm.Config{EchoCancel: true, NS: apm.NSLow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	frame := make([]int16, apm.FrameSamples)
	if err := p.ProcessStream(frame); !errors.Is(err, apm.ErrClosed) {
		t.Fatalf("ProcessStream after Close = %v, want ErrClosed", err)
	}
	if err := p.ProcessReverseStream(frame); !errors.Is(err, apm.ErrClosed) {
		t.Fatalf("ProcessReverseStream after Close = %v, want ErrClosed", err)
	}
	p.SetStreamDelayMs(40) // must not panic on a closed instance
}

func TestFrameLengthIsValidated(t *testing.T) {
	t.Parallel()
	p := newAPM(t, apm.Config{EchoCancel: true, NS: apm.NSModerate})

	for _, n := range []int{0, 1, apm.FrameSamples - 1, apm.FrameSamples + 1, 960} {
		if err := p.ProcessStream(make([]int16, n)); err == nil {
			t.Errorf("ProcessStream accepted %d samples, want an error", n)
		}
		if err := p.ProcessReverseStream(make([]int16, n)); err == nil {
			t.Errorf("ProcessReverseStream accepted %d samples, want an error", n)
		}
	}
}

func TestSetStreamDelayMsAcceptsOutOfRange(t *testing.T) {
	t.Parallel()
	p := newAPM(t, apm.Config{EchoCancel: true, NS: apm.NSModerate})

	// The clamp is the contract: out-of-range values are corrected, not
	// rejected, because they come from device latency measurements.
	frame := make([]int16, apm.FrameSamples)
	for _, ms := range []int{-1000, -1, 0, 500, 100000} {
		p.SetStreamDelayMs(ms)
		if err := p.ProcessStream(frame); err != nil {
			t.Fatalf("ProcessStream after SetStreamDelayMs(%d): %v", ms, err)
		}
	}
}

// echoPath is a crude but linear and time-invariant loudspeaker-to-microphone
// path: a bulk delay plus two reflections. AEC3 is expected to identify it.
type echoPath struct {
	history []float64 // circular, next write at pos
	pos     int
	delay   int
}

func newEchoPath(delaySamples int) *echoPath {
	return &echoPath{history: make([]float64, delaySamples+512), delay: delaySamples}
}

func (e *echoPath) push(x float64) float64 {
	e.history[e.pos] = x
	e.pos = (e.pos + 1) % len(e.history)
	at := func(d int) float64 {
		return e.history[((e.pos-1-d)%len(e.history)+len(e.history))%len(e.history)]
	}
	return 0.5*at(e.delay) + 0.25*at(e.delay+160) + 0.12*at(e.delay+320)
}

// TestEchoReturnLossEnhancement drives the module the way the RX and TX paths
// do: the far-end mix goes through ProcessReverseStream just before playout,
// and what the microphone picks up (here: only echo) goes through
// ProcessStream. With no near-end talker the output has to collapse.
func TestEchoReturnLossEnhancement(t *testing.T) {
	t.Parallel()
	p := newAPM(t, apm.Config{EchoCancel: true, NS: apm.NSModerate})

	const (
		delayMs      = 50
		delaySamples = delayMs * 48
		totalFrames  = 3 * framesPerSecond
		measureFrom  = 2 * framesPerSecond // let AEC3 converge first
	)

	r := newRNG()
	path := newEchoPath(delaySamples)
	render := make([]int16, apm.FrameSamples)
	capture := make([]int16, apm.FrameSamples)

	var echoEnergy, outEnergy float64
	var measured int
	for f := range totalFrames {
		// Far end: noise shaped by a slow amplitude envelope, so the signal
		// is broadband (good for the delay estimator) but not stationary.
		env := 0.35 * (0.6 + 0.4*math.Sin(2*math.Pi*1.7*float64(f)/framesPerSecond))
		for i := range render {
			render[i] = clip(env * r.float() * math.MaxInt16)
		}

		// The echo the microphone will see is derived from the samples that
		// go to the speaker, before ProcessReverseStream rewrites them.
		for i, s := range render {
			capture[i] = clip(path.push(float64(s)))
		}

		if err := p.ProcessReverseStream(render); err != nil {
			t.Fatalf("ProcessReverseStream frame %d: %v", f, err)
		}
		p.SetStreamDelayMs(delayMs)

		in := rms(capture)
		if err := p.ProcessStream(capture); err != nil {
			t.Fatalf("ProcessStream frame %d: %v", f, err)
		}
		if f >= measureFrom {
			echoEnergy += in * in
			outEnergy += rms(capture) * rms(capture)
			measured++
		}
	}

	if measured == 0 || echoEnergy == 0 {
		t.Fatal("no echo energy generated, the test signal is broken")
	}
	erle := 10 * math.Log10(echoEnergy/math.Max(outEnergy, 1e-12))
	t.Logf("ERLE over the last %d ms: %.1f dB", measured*10, erle)
	if erle < 10 {
		t.Errorf("ERLE = %.1f dB, want > 10 dB", erle)
	}
}

// speechLike synthesises a voiced signal: a moving pitch with harmonics under
// a syllabic envelope. A steady tone would be a bad probe here, because a
// stationary signal is exactly what the noise suppressor is built to remove.
func speechLike(frame []int16, frameIndex int) {
	const amp = 0.25 * math.MaxInt16
	base := float64(frameIndex*len(frame)) / 48000
	for i := range frame {
		t := base + float64(i)/48000
		f0 := 130 + 25*math.Sin(2*math.Pi*0.9*t)
		env := 0.5 + 0.5*math.Sin(2*math.Pi*3.5*t)
		v := math.Sin(2*math.Pi*f0*t) +
			0.5*math.Sin(2*math.Pi*2*f0*t) +
			0.25*math.Sin(2*math.Pi*3*f0*t)
		frame[i] = clip(amp * env * v / 1.75)
	}
}

// TestSpeechSurvivesNSAndAGC guards against the failure mode where the
// suppressor and the gain controller between them turn speech into silence.
func TestSpeechSurvivesNSAndAGC(t *testing.T) {
	t.Parallel()

	levels := []struct {
		name  string
		level apm.NSLevel
	}{
		{"off", apm.NSOff},
		{"low", apm.NSLow},
		{"moderate", apm.NSModerate},
		{"high", apm.NSHigh},
	}
	for _, tc := range levels {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newAPM(t, apm.Config{EchoCancel: true, NS: tc.level})

			const totalFrames = 2 * framesPerSecond
			frame := make([]int16, apm.FrameSamples)
			silence := make([]int16, apm.FrameSamples)

			var inEnergy, outEnergy float64
			var measured int
			for f := range totalFrames {
				for i := range silence {
					silence[i] = 0
				}
				if err := p.ProcessReverseStream(silence); err != nil {
					t.Fatalf("ProcessReverseStream frame %d: %v", f, err)
				}

				speechLike(frame, f)
				in := rms(frame)
				if err := p.ProcessStream(frame); err != nil {
					t.Fatalf("ProcessStream frame %d: %v", f, err)
				}
				if f >= framesPerSecond { // skip the AGC2 ramp
					inEnergy += in * in
					outEnergy += rms(frame) * rms(frame)
					measured++
				}
			}

			gainDB := 10 * math.Log10(math.Max(outEnergy, 1e-12)/inEnergy)
			t.Logf("output level relative to input over %d ms: %+.1f dB", measured*10, gainDB)
			if gainDB < -12 {
				t.Errorf("speech attenuated by %.1f dB, want no more than 12 dB", -gainDB)
			}
			if gainDB > 26 {
				t.Errorf("speech amplified by %.1f dB, want no more than 26 dB", gainDB)
			}
		})
	}
}

// TestEchoCancellerCanBeDisabled exercises the echo_cancel=0 branch of the
// shim: the pipeline still runs (HPF, NS, AGC2) and speech is not silenced.
// Without this, an ignored or inverted flag in the config mapping would go
// unnoticed - every other functional test enables the canceller.
func TestEchoCancellerCanBeDisabled(t *testing.T) {
	t.Parallel()
	p := newAPM(t, apm.Config{EchoCancel: false, NS: apm.NSLow})

	const totalFrames = 2 * framesPerSecond
	frame := make([]int16, apm.FrameSamples)
	silence := make([]int16, apm.FrameSamples)

	var inEnergy, outEnergy float64
	for f := range totalFrames {
		clear(silence)
		if err := p.ProcessReverseStream(silence); err != nil {
			t.Fatalf("ProcessReverseStream frame %d: %v", f, err)
		}
		speechLike(frame, f)
		in := rms(frame)
		if err := p.ProcessStream(frame); err != nil {
			t.Fatalf("ProcessStream frame %d: %v", f, err)
		}
		if f >= framesPerSecond { // skip the AGC2 ramp
			inEnergy += in * in
			outEnergy += rms(frame) * rms(frame)
		}
	}

	gainDB := 10 * math.Log10(math.Max(outEnergy, 1e-12)/inEnergy)
	t.Logf("output level relative to input: %+.1f dB", gainDB)
	if gainDB < -12 {
		t.Errorf("speech attenuated by %.1f dB with the canceller off", -gainDB)
	}
}

// TestProcessStreamDoesNotAllocate pins the hot path: the DSP goroutine runs
// this 100 times a second and must not feed the garbage collector.
func TestProcessStreamDoesNotAllocate(t *testing.T) {
	p := newAPM(t, apm.Config{EchoCancel: true, NS: apm.NSModerate})

	capture := make([]int16, apm.FrameSamples)
	render := make([]int16, apm.FrameSamples)
	for f := range framesPerSecond { // warm up: AEC3 allocates while it grows its buffers
		speechLike(capture, f)
		if err := p.ProcessReverseStream(render); err != nil {
			t.Fatalf("ProcessReverseStream: %v", err)
		}
		if err := p.ProcessStream(capture); err != nil {
			t.Fatalf("ProcessStream: %v", err)
		}
	}

	got := testing.AllocsPerRun(200, func() {
		if err := p.ProcessStream(capture); err != nil {
			t.Fatalf("ProcessStream: %v", err)
		}
	})
	if got != 0 {
		t.Errorf("ProcessStream allocates %.1f times per call, want 0", got)
	}

	got = testing.AllocsPerRun(200, func() {
		if err := p.ProcessReverseStream(render); err != nil {
			t.Fatalf("ProcessReverseStream: %v", err)
		}
	})
	if got != 0 {
		t.Errorf("ProcessReverseStream allocates %.1f times per call, want 0", got)
	}
}

// BenchmarkProcessFrame measures one 10 ms slot of the pipeline: capture plus
// reference. The budget is the frame itself, 10 ms.
func BenchmarkProcessFrame(b *testing.B) {
	p, err := apm.New(apm.Config{EchoCancel: true, NS: apm.NSModerate})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer p.Close()

	capture := make([]int16, apm.FrameSamples)
	render := make([]int16, apm.FrameSamples)
	speechLike(capture, 0)
	speechLike(render, 7)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := p.ProcessReverseStream(render); err != nil {
			b.Fatal(err)
		}
		p.SetStreamDelayMs(40)
		if err := p.ProcessStream(capture); err != nil {
			b.Fatal(err)
		}
	}
}
