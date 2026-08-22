package audio

import (
	"log/slog"
	"math"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
	"github.com/LywwKkA-aD/Gul/internal/dsp/wav"
)

// Golden tests of the DSP chain (PLAN.md 4.6): a committed speech recording
// plus deterministic synthetic disturbances go through the very chain the TX
// and RX pipelines run, and the result is measured in dB against pinned
// thresholds. Nothing here depends on wall clock time, on the machine or on
// a random source: the fixture is a file, the noise comes from a seeded LCG,
// and every measured number is logged so two runs can be compared directly.
//
// The thresholds are not targets, they are regression tripwires. Each one
// sits far below the value the chain actually reaches, so a rebuilt
// dependency or a different compiler cannot flake them, while a broken
// suppressor or a canceller that stopped converging fails immediately.

const goldenFixture = "testdata/speech_48k.wav"

// goldenFramesPerSecond is the frame rate of the fixed 10 ms grid.
const goldenFramesPerSecond = 1000 / FrameMs

// goldenRNG is a linear congruential generator: the noise of these tests has
// to be the same sequence on every machine and every run.
type goldenRNG struct{ state uint64 }

func newGoldenRNG() *goldenRNG { return &goldenRNG{state: 0x2545f4914f6cdd1d} }

// float returns the next sample in [-1, 1).
func (r *goldenRNG) float() float64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return float64(int32(r.state>>32)) / float64(1<<31)
}

// goldenClip folds a float64 sample into the int16 range.
func goldenClip(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

// goldenDB is the level of a relative to b in dB, with a floor so that a
// silent measurement cannot produce an infinity.
func goldenDB(a, b float64) float64 {
	return 20 * math.Log10(math.Max(a, 1e-9)/math.Max(b, 1e-9))
}

// goldenSpeech loads the committed speech fixture, trimmed to whole frames.
func goldenSpeech(t testing.TB) []int16 {
	t.Helper()
	samples, rate, err := wav.Read(goldenFixture)
	if err != nil {
		t.Fatalf("read %s: %v", goldenFixture, err)
	}
	if rate != SampleRate {
		t.Fatalf("%s is %d Hz, the grid is %d Hz", goldenFixture, rate, SampleRate)
	}
	frames := len(samples) / FrameSamples
	if frames < 3*goldenFramesPerSecond {
		t.Fatalf("%s holds %d frames, want at least 3 s of speech", goldenFixture, frames)
	}
	return samples[:frames*FrameSamples]
}

// goldenChain builds a chain of the given shape for the duration of the test.
func goldenChain(t testing.TB, opts DSPOptions) *dspChain {
	t.Helper()
	c, err := newDSPChain(opts, slog.Default())
	if err != nil {
		t.Fatalf("newDSPChain(%+v): %v", opts, err)
	}
	t.Cleanup(c.close)
	return c
}

// energy accumulates a sum of squares and reports its RMS.
type energy struct {
	sum    float64
	frames int
}

func (e *energy) add(rms float64) {
	e.sum += rms * rms
	e.frames++
}

func (e *energy) rms() float64 {
	if e.frames == 0 {
		return 0
	}
	return math.Sqrt(e.sum / float64(e.frames))
}

// goldenSection labels one frame of the test signal.
type goldenSection uint8

const (
	goldenIgnore goldenSection = iota // transition frames, measured by neither metric
	goldenSpeechSection
	goldenNoiseSection
)

// goldenMixture is the noise-reduction test signal: the speech fixture plus
// a noise-only tail, with white noise over the whole length, and a
// per-frame labelling taken from the clean fixture - before any noise is
// added, so the labels cannot depend on the chain under test.
type goldenMixture struct {
	pcm         []int16
	section     []goldenSection
	frames      int
	speechRMS   float64 // level of the clean speech, over the speech frames
	noiseRMS    float64 // level of the added noise
	speechCount int
	noiseCount  int
}

// goldenMix builds that signal at the given speech to noise ratio.
//
// The fixture is a real utterance with pauses between phrases and no
// meaningful lead-in silence, so the noise-only sections are those pauses
// plus an appended tail of pure noise. Frames in between - the fade in and
// out of every phrase - are labelled neither, because attributing them to
// one metric or the other would only measure where the cut was drawn.
func goldenMix(clean []int16, snrDB float64, tailFrames int) *goldenMixture {
	const (
		// A frame quieter than this carries no speech: the fixture drops to
		// a couple of LSB between phrases, which is 40 dB below the cut.
		silenceRMS = 50.0
		// A frame is speech above this share of the loudest frame.
		speechFrac = 0.15
	)

	fixtureFrames := len(clean) / FrameSamples
	m := &goldenMixture{frames: fixtureFrames + tailFrames}
	m.section = make([]goldenSection, m.frames)

	frameRMS := make([]float64, fixtureFrames)
	var peak float64
	for f := range fixtureFrames {
		lo := f * FrameSamples
		frameRMS[f] = RMS(clean[lo : lo+FrameSamples])
		peak = math.Max(peak, frameRMS[f])
	}
	var speech energy
	for f := range fixtureFrames {
		switch {
		case frameRMS[f] > speechFrac*peak:
			m.section[f] = goldenSpeechSection
			speech.add(frameRMS[f])
			m.speechCount++
		case frameRMS[f] < silenceRMS:
			m.section[f] = goldenNoiseSection
			m.noiseCount++
		}
	}
	for f := fixtureFrames; f < m.frames; f++ {
		m.section[f] = goldenNoiseSection
		m.noiseCount++
	}
	m.speechRMS = speech.rms()
	m.noiseRMS = m.speechRMS / math.Pow(10, snrDB/20)

	// The LCG is uniform on [-1, 1), so its RMS is the amplitude over sqrt(3).
	amp := m.noiseRMS * math.Sqrt(3)
	r := newGoldenRNG()
	m.pcm = make([]int16, m.frames*FrameSamples)
	for i := range m.pcm {
		var s float64
		if i < len(clean) {
			s = float64(clean[i])
		}
		m.pcm[i] = goldenClip(s + amp*r.float())
	}
	return m
}

// TestGoldenNoiseSuppression measures the denoising half of the M3 chain
// (APM NS low plus RNNoise, the product default of PLAN.md 4.3) on the
// speech fixture mixed with white noise at a fixed SNR: the pauses have to
// collapse and the speech has to come out at roughly the level it went in.
func TestGoldenNoiseSuppression(t *testing.T) {
	const (
		snrDB      = 10.0 // speech to noise ratio of the mixture
		tailFrames = 100  // one second of noise after the speech
		warmup     = 30   // frames excluded: NS estimate and AGC2 ramp
	)

	mix := goldenMix(goldenSpeech(t), snrDB, tailFrames)
	if mix.speechCount == 0 || mix.noiseCount == 0 {
		t.Fatalf("signal split into %d speech and %d noise frames",
			mix.speechCount, mix.noiseCount)
	}
	t.Logf("signal: %d frames, %d speech, %d noise-only (tail %d), %d transitional",
		mix.frames, mix.speechCount, mix.noiseCount, tailFrames,
		mix.frames-mix.speechCount-mix.noiseCount)
	t.Logf("levels: speech RMS %.0f, noise RMS %.0f, SNR %.1f dB",
		mix.speechRMS, mix.noiseRMS, goldenDB(mix.speechRMS, mix.noiseRMS))

	chain := goldenChain(t, DSPOptions{NS: apm.NSLow, RNNoise: true})
	frame := make([]int16, FrameSamples)
	var inNoise, outNoise, inSpeech, outSpeech energy
	for f := range mix.frames {
		lo := f * FrameSamples
		copy(frame, mix.pcm[lo:lo+FrameSamples])
		in := RMS(frame)
		chain.tx(frame)
		out := RMS(frame)
		if f < warmup {
			continue
		}
		switch mix.section[f] {
		case goldenSpeechSection:
			inSpeech.add(in)
			outSpeech.add(out)
		case goldenNoiseSection:
			inNoise.add(in)
			outNoise.add(out)
		case goldenIgnore:
		}
	}

	noiseDropDB := goldenDB(inNoise.rms(), outNoise.rms())
	speechGainDB := goldenDB(outSpeech.rms(), inSpeech.rms())
	cleanRefDB := goldenDB(outSpeech.rms(), mix.speechRMS)
	snrGainDB := noiseDropDB + speechGainDB
	t.Logf("noise-only sections: in RMS %.1f, out RMS %.1f, drop %.1f dB",
		inNoise.rms(), outNoise.rms(), noiseDropDB)
	t.Logf("speech sections: in RMS %.1f (noisy), out RMS %.1f, gain %+.1f dB "+
		"(%+.1f dB against the clean fixture)",
		inSpeech.rms(), outSpeech.rms(), speechGainDB, cleanRefDB)
	t.Logf("SNR improvement: %+.1f dB", snrGainDB)

	// Thresholds pinned against a measured run (macOS arm64, NEON):
	// 15.8 dB of noise drop, +1.1 dB on the speech, +16.5 dB of SNR. Each
	// one keeps most of that as headroom, so a rebuilt dependency or another
	// SIMD path cannot flake them while a dead suppressor fails at once.
	if noiseDropDB < 10 {
		t.Errorf("noise-only sections dropped by %.1f dB, want at least 10 dB", noiseDropDB)
	}
	if cleanRefDB < -9 {
		t.Errorf("speech attenuated by %.1f dB against the clean fixture, want at most 9 dB", -cleanRefDB)
	}
	if cleanRefDB > 15 {
		t.Errorf("speech amplified by %.1f dB against the clean fixture, want at most 15 dB", cleanRefDB)
	}
	if snrGainDB < 10 {
		t.Errorf("SNR improved by %.1f dB, want at least 10 dB", snrGainDB)
	}
}

// goldenEchoPath is a crude but linear and time-invariant speaker to
// microphone path: a bulk delay plus two reflections, all attenuated. AEC3
// is expected to identify it from the reference stream alone.
type goldenEchoPath struct {
	history []float64 // circular, next write at pos
	pos     int
	delay   int
}

func newGoldenEchoPath(delaySamples int) *goldenEchoPath {
	return &goldenEchoPath{history: make([]float64, delaySamples+512), delay: delaySamples}
}

func (e *goldenEchoPath) push(x float64) float64 {
	e.history[e.pos] = x
	e.pos = (e.pos + 1) % len(e.history)
	at := func(d int) float64 {
		return e.history[((e.pos-1-d)%len(e.history)+len(e.history))%len(e.history)]
	}
	return 0.5*at(e.delay) + 0.25*at(e.delay+160) + 0.12*at(e.delay+320)
}

// TestGoldenERLE measures echo cancellation through the chain rather than
// through the apm package alone: the fixture plays as the far end and goes
// into reverse() exactly the way pipeline_rx feeds it, the microphone hears
// nothing but the echo of that playback, and tx() runs the capture chain the
// way pipeline_tx does. With no near-end talker the capture output has to
// collapse; what is left of it is the residual echo.
//
// RNNoise stays off here on purpose: the number has to describe the
// canceller, not the DNN denoiser mopping up behind it. NS low and AGC2
// still run, but without AEC the same signal measures about -2 dB, so the
// metric is carried by the canceller alone.
func TestGoldenERLE(t *testing.T) {
	speech := goldenSpeech(t)

	const (
		delayMs      = 50
		delaySamples = delayMs * (SampleRate / 1000)
		converge     = 2 * goldenFramesPerSecond // AEC3 has this long to lock on
	)
	frames := len(speech) / FrameSamples
	if frames <= converge {
		t.Fatalf("fixture holds %d frames, need more than %d for convergence", frames, converge)
	}

	chain := goldenChain(t, DSPOptions{EchoCancel: true, NS: apm.NSLow})
	path := newGoldenEchoPath(delaySamples)
	render := make([]int16, FrameSamples)
	capture := make([]int16, FrameSamples)

	var echo, residual energy
	for f := range frames {
		lo := f * FrameSamples
		copy(render, speech[lo:lo+FrameSamples])
		// The echo comes from the samples that go to the speaker, taken
		// before reverse() is allowed to rewrite the render frame.
		for i, s := range render {
			capture[i] = goldenClip(path.push(float64(s)))
		}
		chain.reverse(render)
		chain.delayHint(delayMs)

		in := RMS(capture)
		chain.tx(capture)
		if f < converge {
			continue
		}
		echo.add(in)
		residual.add(RMS(capture))
	}

	erle := goldenDB(echo.rms(), residual.rms())
	t.Logf("echo RMS %.1f, residual RMS %.1f, ERLE %.1f dB over %d ms",
		echo.rms(), residual.rms(), erle, residual.frames*FrameMs)
	if echo.rms() < 100 {
		t.Fatalf("echo RMS %.1f: the test signal is too quiet to measure", echo.rms())
	}
	// Pinned against a measured run (macOS arm64, NEON): 32.8 dB. A
	// canceller that stopped converging lands under 10 dB, so 15 dB
	// separates the two states with room for another SIMD path in between.
	if erle < 15 {
		t.Errorf("ERLE = %.1f dB, want at least 15 dB", erle)
	}
}

// TestProcessOfflineMatchesTheChain pins the offline facade the A/B kit
// runs on (cmd/gul-dsp): it must be the same chain, frame by frame, applied
// to the whole recording, and it must return the audio at its input length
// rather than at the next frame boundary.
func TestProcessOfflineMatchesTheChain(t *testing.T) {
	speech := goldenSpeech(t)
	// One second is enough to compare, and keeps the golden set well inside
	// its runtime budget.
	const seconds = 1
	input := speech[:seconds*SampleRate]
	opts := DSPOptions{NS: apm.NSLow, RNNoise: true}

	got, err := ProcessOffline(input, opts)
	if err != nil {
		t.Fatalf("ProcessOffline: %v", err)
	}
	if len(got) != len(input) {
		t.Fatalf("ProcessOffline returned %d samples, want %d", len(got), len(input))
	}

	chain := goldenChain(t, opts)
	want := make([]int16, 0, len(input))
	frame := make([]int16, FrameSamples)
	for off := 0; off < len(input); off += FrameSamples {
		copy(frame, input[off:off+FrameSamples])
		chain.tx(frame)
		want = append(want, frame...)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %d, the chain gives %d", i, got[i], want[i])
		}
	}
	t.Logf("offline output over %d s: RMS %.1f (input %.1f)",
		seconds, RMS(got), RMS(input))

	// A recording that does not end on a frame boundary keeps its length.
	short, err := ProcessOffline(input[:FrameSamples+7], opts)
	if err != nil {
		t.Fatalf("ProcessOffline of a partial frame: %v", err)
	}
	if len(short) != FrameSamples+7 {
		t.Fatalf("partial frame: got %d samples, want %d", len(short), FrameSamples+7)
	}
}
