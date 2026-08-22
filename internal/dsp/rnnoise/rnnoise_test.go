package rnnoise_test

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/rnnoise"
)

const (
	sampleRate = 48000
	// framesPerSecond frames of 10 ms make one second of audio.
	framesPerSecond = 100
	// warmupFrames covers the algorithmic delay (window overlap plus the
	// delayed frame) and the first adaptation of the network.
	warmupFrames = 50
)

// fillSine writes a 440 Hz tone of the given amplitude, keeping the phase
// continuous across frames (offset counts the samples already generated).
func fillSine(buf []float32, offset int, amp float64) {
	for i := range buf {
		t := float64(offset+i) / sampleRate
		buf[i] = float32(amp * math.Sin(2*math.Pi*440*t))
	}
}

func rms(buf []float32) float64 {
	var sum float64
	for _, s := range buf {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(buf)))
}

func newDenoiser(t *testing.T) *rnnoise.Denoiser {
	t.Helper()
	d, err := rnnoise.NewDenoiser()
	if err != nil {
		t.Fatalf("NewDenoiser: %v", err)
	}
	return d
}

// TestSpeechProbabilityInS16Scale is the scale test required by PLAN.md M3:
// a tone at 0.3 of full scale, fed in the S16 scale the library expects, must
// drive the network at all.
func TestSpeechProbabilityInS16Scale(t *testing.T) {
	t.Parallel()
	d := newDenoiser(t)
	defer d.Close()

	frame := make([]float32, rnnoise.FrameSamples)
	var maxProb float32
	for i := range framesPerSecond {
		fillSine(frame, i*rnnoise.FrameSamples, 0.3*32768)
		prob, err := d.Process(frame)
		if err != nil {
			t.Fatalf("Process frame %d: %v", i, err)
		}
		if prob > maxProb {
			maxProb = prob
		}
	}
	if maxProb <= 0 {
		t.Fatalf("max speech probability over 1 s = %v, want > 0", maxProb)
	}
	t.Logf("max speech probability over 1 s of a 0.3 FS tone: %v", maxProb)
}

// TestNormalizedInputIsSilence documents the trap: the same tone normalized
// to +/-1.0 stays below the internal silence threshold, so the network never
// runs and the denoiser is effectively off.
func TestNormalizedInputIsSilence(t *testing.T) {
	t.Parallel()
	d := newDenoiser(t)
	defer d.Close()

	frame := make([]float32, rnnoise.FrameSamples)
	for i := range framesPerSecond {
		fillSine(frame, i*rnnoise.FrameSamples, 0.3)
		prob, err := d.Process(frame)
		if err != nil {
			t.Fatalf("Process frame %d: %v", i, err)
		}
		if prob != 0 {
			t.Fatalf("frame %d of normalized input reported probability %v, want exactly 0", i, prob)
		}
	}
}

func TestSuppressesSteadyNoise(t *testing.T) {
	t.Parallel()
	d := newDenoiser(t)
	defer d.Close()

	rng := rand.New(rand.NewSource(1))
	frame := make([]float32, rnnoise.FrameSamples)
	var inSum, outSum float64
	var measured int
	for i := range 2 * framesPerSecond {
		for j := range frame {
			frame[j] = float32(rng.NormFloat64() * 2000)
		}
		in := rms(frame)
		if _, err := d.Process(frame); err != nil {
			t.Fatalf("Process frame %d: %v", i, err)
		}
		if i < warmupFrames {
			continue
		}
		inSum += in
		outSum += rms(frame)
		measured++
	}
	inAvg := inSum / float64(measured)
	outAvg := outSum / float64(measured)
	if outAvg >= 0.5*inAvg {
		t.Fatalf("average RMS %.1f in, %.1f out: want the output well below half the input", inAvg, outAvg)
	}
	t.Logf("white noise: average RMS %.1f in, %.1f out", inAvg, outAvg)
}

func TestProcessRejectsWrongFrameLength(t *testing.T) {
	t.Parallel()
	d := newDenoiser(t)
	defer d.Close()

	for _, n := range []int{0, 1, rnnoise.FrameSamples - 1, rnnoise.FrameSamples + 1} {
		if _, err := d.Process(make([]float32, n)); err == nil {
			t.Fatalf("Process accepted a %d-sample frame, want an error", n)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	d := newDenoiser(t)

	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := d.Process(make([]float32, rnnoise.FrameSamples)); !errors.Is(err, rnnoise.ErrClosed) {
		t.Fatalf("Process after Close: %v, want ErrClosed", err)
	}
}

// TestModelFromBufferInitializesFile pins a vendored RNNoise lifetime fix.
// Upstream's buffer constructor used to leave RNNModel.file uninitialised,
// while rnnoise_model_free treated any non-NULL value as a FILE*. On Linux,
// closing a denoiser could consequently pass allocator garbage to fclose.
func TestModelFromBufferInitializesFile(t *testing.T) {
	t.Parallel()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the test source")
	}
	path := filepath.Join(filepath.Dir(testFile), "..", "..", "..", "third_party", "rnnoise", "src", "denoise.c")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vendored denoise.c: %v", err)
	}
	const signature = "RNNModel *rnnoise_model_from_buffer(const void *ptr, int len) {"
	start := strings.Index(string(source), signature)
	if start < 0 {
		t.Fatalf("vendored denoise.c does not contain %q", signature)
	}
	end := strings.Index(string(source[start:]), "\n}")
	if end < 0 {
		t.Fatal("cannot find the end of rnnoise_model_from_buffer")
	}
	body := string(source[start : start+end])
	if !strings.Contains(body, "model->file = NULL;") {
		t.Fatal("rnnoise_model_from_buffer does not initialise model->file before the model can be freed")
	}
}

// TestCloseWithPoisonedAllocator exercises the same bug at the public Go API.
// MALLOC_PERTURB_ makes otherwise-indeterminate malloc bytes non-zero, and
// disabling glibc's tcache keeps that poisoning deterministic in the child.
func TestCloseWithPoisonedAllocator(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("glibc allocator regression test")
	}
	const childEnv = "GUL_RNNOISE_POISONED_ALLOCATOR_CHILD"
	if os.Getenv(childEnv) == "1" {
		d := newDenoiser(t)
		if err := d.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestCloseWithPoisonedAllocator$")
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		"MALLOC_PERTURB_=165",
		"GLIBC_TUNABLES=glibc.malloc.tcache_count=0",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("denoiser child failed with poisoned allocator: %v\n%s", err, output)
	}
}

func TestDenoisersAreIndependent(t *testing.T) {
	t.Parallel()
	first := newDenoiser(t)
	defer first.Close()
	second := newDenoiser(t)
	defer second.Close()

	// Closing one must not disturb the weights the other one borrows.
	third := newDenoiser(t)
	if err := third.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	a := make([]float32, rnnoise.FrameSamples)
	b := make([]float32, rnnoise.FrameSamples)
	for i := range framesPerSecond {
		fillSine(a, i*rnnoise.FrameSamples, 0.3*32768)
		copy(b, a)
		probA, err := first.Process(a)
		if err != nil {
			t.Fatalf("first.Process frame %d: %v", i, err)
		}
		probB, err := second.Process(b)
		if err != nil {
			t.Fatalf("second.Process frame %d: %v", i, err)
		}
		if probA != probB {
			t.Fatalf("frame %d: probabilities %v and %v differ", i, probA, probB)
		}
	}
}

// TestProcessDoesNotAllocate guards the DSP goroutine against the garbage
// collector: one frame every 10 ms, forever.
func TestProcessDoesNotAllocate(t *testing.T) {
	d := newDenoiser(t)
	defer d.Close()

	frame := make([]float32, rnnoise.FrameSamples)
	fillSine(frame, 0, 0.3*32768)
	if n := testing.AllocsPerRun(100, func() {
		if _, err := d.Process(frame); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}); n != 0 {
		t.Fatalf("Process allocates %v times per call, want 0", n)
	}
}

func BenchmarkProcessFrame(b *testing.B) {
	b.ReportAllocs()
	d, err := rnnoise.NewDenoiser()
	if err != nil {
		b.Fatalf("NewDenoiser: %v", err)
	}
	defer d.Close()

	// The input must be refreshed every iteration. Processing the previous
	// output in place lets the signal decay until the internal silence
	// detector short-circuits the network, and the benchmark then reports
	// the cost of skipping the DNN - roughly ten times below the real
	// per-frame cost (0.047 ms against 0.44 ms measured on an M4).
	const frames = 100
	src := make([]float32, frames*rnnoise.FrameSamples)
	fillSine(src, 0, 0.3*32768)
	frame := make([]float32, rnnoise.FrameSamples)
	i := 0
	b.ResetTimer()
	for b.Loop() {
		copy(frame, src[i*rnnoise.FrameSamples:(i+1)*rnnoise.FrameSamples])
		i = (i + 1) % frames
		if _, err := d.Process(frame); err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}
