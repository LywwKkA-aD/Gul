//go:build live

package audio

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
	"github.com/LywwKkA-aD/Gul/internal/dsp/wav"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// latencySource plays a real recorded utterance from its start while
// speaking is set, and silence otherwise, paced to the wall clock like a
// real capture ring: one frame per 10 ms.
//
// The probe has to be real speech and it has to pause between cycles;
// three synthetic shortcuts failed live against RNNoise. A steady sine
// died on the second cycle, an exactly periodic "syllable" pattern faded
// out progressively, and an aperiodic random-walk vowel drone was crushed
// to RMS 74 while transmitting continuously - the model has no reason to
// treat a formantless harmonic stack that never stops as anything but a
// tone-like noise source. Real formants plus the silence-to-speech onset
// each cycle is exactly what the model preserves.
type latencySource struct {
	start    time.Time
	offset   int
	speaking atomic.Bool

	speech []int16
	pos    int
}

func newLatencySource(t *testing.T) *latencySource {
	t.Helper()
	samples, rate, err := wav.Read("testdata/speech_48k.wav")
	if err != nil {
		t.Fatalf("speech fixture: %v", err)
	}
	if rate != SampleRate {
		t.Fatalf("speech fixture rate %d, want %d", rate, SampleRate)
	}
	// Trim the leading TTS silence: the cycle timer starts when speaking is
	// set, so a quiet head would be billed to the pipeline.
	head := 0
	for head < len(samples) && samples[head] > -500 && samples[head] < 500 {
		head++
	}
	if head == len(samples) {
		t.Fatal("speech fixture is silent")
	}
	return &latencySource{speech: samples[head:]}
}

func (s *latencySource) ReadFrame(dst []int16) bool {
	if s.start.IsZero() {
		s.start = time.Now()
	}
	produced := s.offset / FrameSamples
	if int(time.Since(s.start)/(FrameMs*time.Millisecond)) <= produced {
		return false
	}
	s.offset += len(dst)
	if !s.speaking.Load() {
		s.pos = 0 // the next utterance starts from the top of the recording
		clear(dst)
		return true
	}
	n := copy(dst, s.speech[s.pos:])
	clear(dst[n:]) // silence once the recording runs out mid-cycle
	s.pos += n
	return true
}

// onsetSink records the wall-clock instant the first audible frame lands
// after arm(), plus the loudest frame seen while armed (diagnostics for a
// missed onset: quiet audio versus no audio at all).
type onsetSink struct {
	mu     sync.Mutex
	armed  bool
	at     time.Time
	maxRMS float64
}

func (o *onsetSink) WriteFrame(src []int16) bool {
	rms := RMS(src)
	o.mu.Lock()
	if o.armed && rms > o.maxRMS {
		o.maxRMS = rms
	}
	if o.armed && rms > 1000 {
		o.armed = false
		o.at = time.Now()
	}
	o.mu.Unlock()
	return true
}

func (o *onsetSink) arm() {
	o.mu.Lock()
	o.armed, o.at, o.maxRMS = true, time.Time{}, 0
	o.mu.Unlock()
}

func (o *onsetSink) onset() (time.Time, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.at, !o.at.IsZero()
}

func (o *onsetSink) loudest() float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxRMS
}

// TestMouthToEarLoopback measures the pipeline mouth-to-ear latency through
// the dev stand: engine TX -> murmur (VoiceTargetLoopback) -> engine RX, on
// synthetic frames. The number covers encode, transport, the server, jitter
// priming (JitterStartMs), decode and mixing; real devices add their period
// and ring on top (roughly 20-40 ms). PLAN.md 4.7 budgets <= 250 ms on a
// good network; the dev stand is loopback, so this is the pipeline floor.
//
// Two shapes are measured: the bare M2 pipeline and the M3 DSP chain
// without AEC3. The canceller has to stay out of a loopback measurement -
// the returned stream is literally the echo reference, so a converged AEC3
// would (correctly) erase the very signal whose onset is being timed; it
// also adds no algorithmic delay of its own, unlike RNNoise.
// Run: task murmur:up && go test -tags live ./internal/audio -run TestMouthToEarLoopback -v
func TestMouthToEarLoopback(t *testing.T) {
	shapes := []struct {
		name string
		dsp  DSPOptions
	}{
		// The gate stays out of both shapes: it measures the VAD's opening
		// threshold, not the audio path, and has its own tests. What is
		// timed here is the M3 audio-path budget: APM + RNNoise + encode +
		// transport + jitter + decode.
		{"m2-bare", DSPOptions{}},
		{"m3-dsp", DSPOptions{EchoCancel: false, NS: apm.NSLow, RNNoise: true}},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			measureMouthToEar(t, "gul-m2e-"+shape.name, shape.dsp)
		})
	}
}

func measureMouthToEar(t *testing.T, user string, dsp DSPOptions) {
	var (
		mu sync.Mutex
		st domain.ConnState
	)
	mgr, err := mumble.NewManager(t.TempDir(), slog.Default(), mumble.Callbacks{
		OnStatus: func(s domain.ConnectionStatus) {
			mu.Lock()
			st = s.State
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	mgr.Connect("127.0.0.1:64738", user, "")
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		state := st
		mu.Unlock()
		if state == domain.StateConnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never connected, last state %s", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := mgr.SetVoiceTarget(mumble.VoiceTargetLoopback); err != nil {
		t.Fatalf("SetVoiceTarget: %v", err)
	}

	var sent atomic.Int64
	send := func(opus []byte, final bool) error {
		if !final {
			sent.Add(1)
		}
		return mgr.SendVoice(opus, final)
	}
	e := NewEngine(Config{
		Packets: mgr.VoicePackets(),
		Send:    send,
		Bitrate: 40000,
		DSP:     &dsp,
		Log:     slog.Default(),
	})
	e.SetMute(true)

	src := newLatencySource(t)
	sink := &onsetSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)
	defer func() {
		close(stop)
		<-done
	}()

	// Let the loop settle on the grid before the first cycle.
	time.Sleep(300 * time.Millisecond)

	const cycles = 5
	var samples []time.Duration
	for i := range cycles {
		sink.arm()
		sentBefore := sent.Load()
		begin := time.Now()
		src.speaking.Store(true)
		e.SetMute(false)

		var m2e time.Duration
		waitUntil := time.Now().Add(5 * time.Second)
		for {
			if at, ok := sink.onset(); ok {
				m2e = at.Sub(begin)
				break
			}
			if time.Now().After(waitUntil) {
				st := mgr.VoiceStats()
				t.Fatalf("cycle %d: no audible loopback within 5s (tx frames %d, loudest rx frame %.0f, stats %+v)",
					i, sent.Load()-sentBefore, sink.loudest(), st)
			}
			time.Sleep(5 * time.Millisecond)
		}
		samples = append(samples, m2e)
		t.Logf("cycle %d: mouth-to-ear %v", i, m2e.Round(time.Millisecond))

		// Close the transmission and let the RX stream drain to idle, so the
		// next cycle primes the jitter buffer from scratch like a fresh
		// utterance would. The source falls silent too - between utterances
		// a real microphone hears the room, not an endless drone.
		src.speaking.Store(false)
		e.SetMute(true)
		time.Sleep(1500 * time.Millisecond)
	}

	sorted := append([]time.Duration(nil), samples...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	median := sorted[len(sorted)/2]
	t.Logf("mouth-to-ear median %v (min %v, max %v) over %d cycles",
		median.Round(time.Millisecond), sorted[0].Round(time.Millisecond),
		sorted[len(sorted)-1].Round(time.Millisecond), cycles)

	// The 250 ms budget of PLAN.md 4.7 includes a real network; on loopback
	// anything near it means the pipeline itself is eating the budget.
	if median > 250*time.Millisecond {
		t.Fatalf("median mouth-to-ear %v exceeds the full 250 ms budget on loopback", median)
	}
}
