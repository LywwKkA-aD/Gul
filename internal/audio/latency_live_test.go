//go:build live

package audio

import (
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// latencySource produces a continuous 440 Hz tone at 0.3 FS, paced to the
// wall clock like a real capture ring: one frame per 10 ms.
type latencySource struct {
	start  time.Time
	offset int
}

func (s *latencySource) ReadFrame(dst []int16) bool {
	if s.start.IsZero() {
		s.start = time.Now()
	}
	produced := s.offset / FrameSamples
	if int(time.Since(s.start)/(FrameMs*time.Millisecond)) <= produced {
		return false
	}
	const amp = 0.3 * 32767
	for i := range dst {
		tm := float64(s.offset+i) / SampleRate
		dst[i] = int16(amp * math.Sin(2*math.Pi*440*tm))
	}
	s.offset += len(dst)
	return true
}

// onsetSink records the wall-clock instant the first audible frame lands
// after arm().
type onsetSink struct {
	mu    sync.Mutex
	armed bool
	at    time.Time
}

func (o *onsetSink) WriteFrame(src []int16) bool {
	if RMS(src) > 1000 {
		o.mu.Lock()
		if o.armed {
			o.armed = false
			o.at = time.Now()
		}
		o.mu.Unlock()
	}
	return true
}

func (o *onsetSink) arm() {
	o.mu.Lock()
	o.armed, o.at = true, time.Time{}
	o.mu.Unlock()
}

func (o *onsetSink) onset() (time.Time, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.at, !o.at.IsZero()
}

// TestMouthToEarLoopback measures the pipeline mouth-to-ear latency through
// the dev stand: engine TX -> murmur (VoiceTargetLoopback) -> engine RX, on
// synthetic frames. The number covers encode, transport, the server, jitter
// priming (JitterStartMs), decode and mixing; real devices add their period
// and ring on top (roughly 20-40 ms). PLAN.md 4.7 budgets <= 250 ms on a
// good network; the dev stand is loopback, so this is the pipeline floor.
// Run: task murmur:up && go test -tags live ./internal/audio -run TestMouthToEarLoopback -v
func TestMouthToEarLoopback(t *testing.T) {
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

	mgr.Connect("127.0.0.1:64738", "gul-m2e", "")
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

	e := NewEngine(Config{
		Packets: mgr.VoicePackets(),
		Send:    mgr.SendVoice,
		Bitrate: 40000,
		Log:     slog.Default(),
	})
	e.SetMute(true)

	src := &latencySource{}
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
		begin := time.Now()
		e.SetMute(false)

		var m2e time.Duration
		waitUntil := time.Now().Add(5 * time.Second)
		for {
			if at, ok := sink.onset(); ok {
				m2e = at.Sub(begin)
				break
			}
			if time.Now().After(waitUntil) {
				t.Fatalf("cycle %d: no audible loopback within 5s", i)
			}
			time.Sleep(5 * time.Millisecond)
		}
		samples = append(samples, m2e)
		t.Logf("cycle %d: mouth-to-ear %v", i, m2e.Round(time.Millisecond))

		// Close the transmission and let the RX stream drain to idle, so the
		// next cycle primes the jitter buffer from scratch like a fresh
		// utterance would.
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
