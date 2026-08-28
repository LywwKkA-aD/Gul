package audio

import (
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// sineSource produces a 440 Hz tone at 0.3 FS paced to the wall clock the
// way a real capture ring is: one frame becomes available per 10 ms, so a
// drain loop hits false once it catches up.
type sineSource struct {
	start  time.Time
	offset int
}

func (s *sineSource) ReadFrame(dst []int16) bool {
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

// collectSink records the RMS of every played frame.
type collectSink struct {
	mu  sync.Mutex
	rms []float64
}

func (c *collectSink) WriteFrame(src []int16) bool {
	c.mu.Lock()
	c.rms = append(c.rms, RMS(src))
	c.mu.Unlock()
	return true
}

func (c *collectSink) loudFrames() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.rms {
		if v > 1000 {
			n++
		}
	}
	return n
}

// TestEngineLoopback runs the full DSP loop without devices: TX encodes
// the sine, the test loops packets back as a remote stream, RX must mix
// them into audible playback frames and report talking transitions.
func TestEngineLoopback(t *testing.T) {
	packets := make(chan mumble.VoicePacket, 64)
	var seq atomic.Int64
	var finals atomic.Int64
	send := func(opus []byte, final bool) error {
		if final {
			finals.Add(1)
			return nil
		}
		p := mumble.VoicePacket{
			Session:  7,
			Key:      "peer-hash",
			Sequence: seq.Add(1) - 1,
			Opus:     opus,
		}
		select {
		case packets <- p:
		default:
		}
		return nil
	}

	var talkingOn, talkingOff atomic.Int64
	// The M3 TX shape minus AEC3: in a loopback test the returned stream IS
	// the echo reference, so a working canceller would (correctly) erase it.
	dsp := DSPOptions{EchoCancel: false, NS: apm.NSLow, RNNoise: true, Gate: true}
	e := NewEngine(Config{
		Packets: packets,
		Send:    send,
		Bitrate: 40000,
		DSP:     &dsp,
		Log:     slog.Default(),
		Callbacks: Callbacks{
			OnTalking: func(session uint32, hash string, talking bool) {
				if session != 7 || hash != "peer-hash" {
					t.Errorf("talking for session %d hash %q", session, hash)
				}
				if talking {
					talkingOn.Add(1)
				} else {
					talkingOff.Add(1)
				}
			},
		},
	})

	src := &sineSource{}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)

	// Let the loop transmit and play the looped-back stream, then mute:
	// the transmission must close with a terminator and the stream drain
	// to idle.
	time.Sleep(600 * time.Millisecond)
	e.SetMute(true)
	time.Sleep(400 * time.Millisecond)
	close(stop)
	<-done

	if got := seq.Load(); got < 30 {
		t.Fatalf("sent %d voice packets, want at least 30", got)
	}
	if got := finals.Load(); got != 1 {
		t.Fatalf("sent %d terminators, want exactly 1", got)
	}
	if got := sink.loudFrames(); got < 20 {
		t.Fatalf("only %d audible playback frames, want at least 20", got)
	}
	if talkingOn.Load() != 1 || talkingOff.Load() != 1 {
		t.Fatalf("talking transitions on=%d off=%d, want 1/1",
			talkingOn.Load(), talkingOff.Load())
	}
}

// burstSource plays a tone for toneFrames frames, then silence, paced to
// the wall clock like a real capture ring.
type burstSource struct {
	start      time.Time
	offset     int
	toneFrames int
}

func (s *burstSource) ReadFrame(dst []int16) bool {
	if s.start.IsZero() {
		s.start = time.Now()
	}
	produced := s.offset / FrameSamples
	if int(time.Since(s.start)/(FrameMs*time.Millisecond)) <= produced {
		return false
	}
	if produced >= s.toneFrames {
		clear(dst)
		s.offset += len(dst)
		return true
	}
	const amp = 0.3 * 32767
	for i := range dst {
		tm := float64(s.offset+i) / SampleRate
		dst[i] = int16(amp * math.Sin(2*math.Pi*440*tm))
	}
	s.offset += len(dst)
	return true
}

// TestEngineGateClosesOnSilence drives the M3 gate end to end: the VAD
// opens on the tone, the hangover carries over the first quiet frames, and
// the transmission then closes with exactly one terminator - without any
// mute involved.
func TestEngineGateClosesOnSilence(t *testing.T) {
	var sent, finals atomic.Int64
	send := func(opus []byte, final bool) error {
		if final {
			finals.Add(1)
		} else {
			sent.Add(1)
		}
		return nil
	}
	dsp := DSPOptions{EchoCancel: false, NS: apm.NSLow, RNNoise: true, Gate: true}
	e := NewEngine(Config{Send: send, DSP: &dsp, Log: slog.Default()})

	const toneFrames = 60
	src := &burstSource{toneFrames: toneFrames}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)

	// Tone 600 ms, then a second of silence: enough for the 300 ms
	// hangover to expire and the terminator to go out.
	time.Sleep(1600 * time.Millisecond)
	close(stop)
	<-done

	if got := finals.Load(); got != 1 {
		t.Fatalf("terminators = %d, want exactly 1", got)
	}
	got := sent.Load()
	if got < toneFrames/2 {
		t.Fatalf("sent only %d voice frames during the tone", got)
	}
	// The tail may carry the full hangover plus the VAD's own inertia, but
	// a gate that never closes would keep transmitting the whole second.
	if max := int64(toneFrames + 30 + 20); got > max {
		t.Fatalf("sent %d frames, want at most %d - the gate did not close", got, max)
	}
}

// TestEngineReportsOwnTalking pins the local speaking indication: our voice
// never comes back from the server, so the transmit gate is its only source.
// The callback fires on transitions only, never once per frame.
func TestEngineReportsOwnTalking(t *testing.T) {
	send := func([]byte, bool) error { return nil }
	var mu sync.Mutex
	var transitions []bool
	dsp := DSPOptions{EchoCancel: false, NS: apm.NSLow, RNNoise: true, Gate: true}
	e := NewEngine(Config{
		Send: send,
		DSP:  &dsp,
		Log:  slog.Default(),
		Callbacks: Callbacks{OnSelfTalking: func(talking bool) {
			mu.Lock()
			transitions = append(transitions, talking)
			mu.Unlock()
		}},
	})

	const toneFrames = 60
	src := &burstSource{toneFrames: toneFrames}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)
	// Tone, then a second of silence: long enough for the hangover to expire.
	time.Sleep(1600 * time.Millisecond)
	close(stop)
	<-done

	mu.Lock()
	got := append([]bool(nil), transitions...)
	mu.Unlock()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("self talking transitions = %v, want [true false]", got)
	}
}

// TestEngineDeafenSilencesOutput checks that deafen keeps frames flowing
// to the sink but silent.
func TestEngineDeafenSilencesOutput(t *testing.T) {
	packets := make(chan mumble.VoicePacket, 64)
	var seq atomic.Int64
	send := func(opus []byte, final bool) error {
		if final {
			return nil
		}
		select {
		case packets <- mumble.VoicePacket{Session: 9, Sequence: seq.Add(1) - 1, Opus: opus}:
		default:
		}
		return nil
	}
	// The bare M2 shape pins the passthrough path: no APM, no RNNoise, no
	// gate - deafen semantics must not depend on the DSP chain.
	e := NewEngine(Config{Packets: packets, Send: send, DSP: &DSPOptions{}, Log: slog.Default()})
	e.SetDeafen(true)

	src := &sineSource{}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)
	time.Sleep(500 * time.Millisecond)
	close(stop)
	<-done

	if got := sink.loudFrames(); got != 0 {
		t.Fatalf("%d audible frames while deafened, want 0", got)
	}
	sink.mu.Lock()
	frames := len(sink.rms)
	sink.mu.Unlock()
	if frames < 30 {
		t.Fatalf("only %d frames reached the sink, want a steady flow", frames)
	}
}
