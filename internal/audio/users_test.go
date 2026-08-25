package audio

import (
	"log/slog"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// ---------------------------------------------------------------------------
// the state itself
// ---------------------------------------------------------------------------

// The reason local mute is not "volume zero": the gain the listener chose has
// to survive the round trip, or unmuting would have to invent one.
func TestUnmutingRestoresTheChosenGain(t *testing.T) {
	t.Parallel()
	var s userAudioState

	s.setVolume("peer", 0.4)
	s.setMuted("peer", true)
	if got := s.get("peer"); !got.muted || got.volume != 0.4 {
		t.Fatalf("while muted = %+v, want the gain kept", got)
	}
	s.setMuted("peer", false)
	if got := s.get("peer"); got.muted || got.volume != 0.4 {
		t.Fatalf("after unmute = %+v, want {0.4 false}", got)
	}
}

func TestPeerNobodyTouchedIsHeardAtUnity(t *testing.T) {
	t.Parallel()
	var s userAudioState
	if got := s.get("stranger"); got.muted || got.volume != 1 {
		t.Fatalf("untouched peer = %+v, want unity and unmuted", got)
	}
}

// A gain change while muted is a decision about what to hear on unmute, not a
// request to hear them now.
func TestVolumeChangeWhileMutedDoesNotUnmute(t *testing.T) {
	t.Parallel()
	var s userAudioState
	s.setMuted("peer", true)
	s.setVolume("peer", 1.8)
	if got := s.get("peer"); !got.muted || got.volume != 1.8 {
		t.Fatalf("after a gain change while muted = %+v", got)
	}
}

// Both are keyed by the certificate hash, so two people are two records.
func TestPeersAreIndependent(t *testing.T) {
	t.Parallel()
	var s userAudioState
	s.setMuted("a", true)
	s.setVolume("b", 0.25)
	if got := s.get("a"); !got.muted || got.volume != 1 {
		t.Errorf("a = %+v", got)
	}
	if got := s.get("b"); got.muted || got.volume != 0.25 {
		t.Errorf("b = %+v", got)
	}
}

// ---------------------------------------------------------------------------
// the mix
// ---------------------------------------------------------------------------

// rxRig drives the receive path deterministically: one remote stream, one
// tick per step, no devices, no encoder and no engine ticker. The DSP shape is
// the bare one, so what reaches the sink is the mix itself.
type rxRig struct {
	rx    *rxPipeline
	sink  *collectSink
	users userAudioState
	s     *rxStream
	frame []int16
	seq   int64
	phase float64
}

const rxRigHash = "0badcafe"

func newRxRig(t *testing.T) *rxRig {
	t.Helper()
	chain, err := newDSPChain(DSPOptions{}, slog.Default())
	if err != nil {
		t.Fatalf("newDSPChain: %v", err)
	}
	t.Cleanup(chain.close)

	cfg := Config{Log: slog.Default()}
	rx := newRxPipeline(cfg, chain)
	t.Cleanup(rx.close)

	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	s := &rxStream{
		session:    7,
		hash:       rxRigHash,
		dec:        dec,
		jit:        NewJitter(),
		pcm:        make([]int16, opus.MaxFrameSize),
		lastPacket: time.Now(),
	}
	rx.streams[s.session] = s

	return &rxRig{rx: rx, sink: &collectSink{}, s: s, frame: make([]int16, FrameSamples)}
}

// push queues n frames of a loud continuous tone, straight into the jitter
// buffer: the codec is not what these tests are about.
func (r *rxRig) push(n int) {
	const amplitude = 12000
	for range n {
		for i := range r.frame {
			r.frame[i] = int16(amplitude * math.Sin(r.phase))
			r.phase += 2 * math.Pi * 440 / SampleRate
		}
		r.s.jit.Push(r.seq, r.frame)
		r.seq++
		r.s.lastPacket = time.Now()
	}
}

func (r *rxRig) step(n int) {
	for range n {
		r.rx.tick(r.sink, false, &r.users, 0)
	}
}

// heard returns the RMS of the frames played since the last call, and clears
// the record.
func (r *rxRig) heard() []float64 {
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := append([]float64(nil), r.sink.rms...)
	r.sink.rms = r.sink.rms[:0]
	return out
}

// mean of the audible frames; zero when nothing was audible.
func mean(values []float64) float64 {
	sum, n := 0.0, 0
	for _, v := range values {
		if v > 0 {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// prime fills the jitter buffer past its start depth and drains the frames it
// holds back, so what follows is steady playout.
func (r *rxRig) prime() {
	r.push(jitterStartFrames * 3)
	r.step(jitterStartFrames * 2)
	r.heard()
}

func TestMutedStreamContributesNothingToTheMix(t *testing.T) {
	rig := newRxRig(t)
	rig.prime()

	// Baseline: the stream is audible.
	rig.push(20)
	rig.step(20)
	if unity := mean(rig.heard()); unity < 1000 {
		t.Fatalf("baseline RMS = %.0f, want an audible stream", unity)
	}

	rig.users.setMuted(rxRigHash, true)
	rig.push(20)
	rig.step(20)
	played := rig.heard()
	if len(played) != 20 {
		t.Fatalf("%d frames reached the sink, want 20 - the device must keep being fed", len(played))
	}
	for i, rms := range played {
		if rms != 0 {
			t.Fatalf("frame %d has RMS %.0f while the peer is muted, want silence", i, rms)
		}
	}
}

func TestUnmutingRestoresTheGainInTheMix(t *testing.T) {
	rig := newRxRig(t)
	rig.prime()

	// A listener who had already chosen a quieter setting.
	rig.users.setVolume(rxRigHash, 0.4)
	rig.push(20)
	rig.step(20)
	quiet := mean(rig.heard())
	if quiet < 100 {
		t.Fatalf("RMS at 0.4 = %.0f, want audible", quiet)
	}

	rig.users.setMuted(rxRigHash, true)
	rig.push(20)
	rig.step(20)
	if silent := mean(rig.heard()); silent != 0 {
		t.Fatalf("RMS while muted = %.0f, want 0", silent)
	}

	rig.users.setMuted(rxRigHash, false)
	rig.push(20)
	rig.step(20)
	back := mean(rig.heard())
	// The same tone at the same gain: the mix is bit-transparent below the
	// soft-clip knee, so this is a tight comparison, not a rough one.
	if math.Abs(back-quiet) > 0.02*quiet {
		t.Fatalf("RMS after unmute = %.0f, want the chosen gain back (%.0f)", back, quiet)
	}
}

// The receive tick runs every 10 ms for the life of a call. Reading the local
// treatment of a peer must not put anything on the heap - that is the rule the
// whole DSP goroutine is built on (PLAN.md 4.6).
func TestRxTickDoesNotAllocate(t *testing.T) {
	rig := newRxRig(t)
	// One muted peer and one heard peer, so both branches of the mix are
	// measured.
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	second := &rxStream{
		session:    8,
		hash:       "second",
		dec:        dec,
		jit:        NewJitter(),
		pcm:        make([]int16, opus.MaxFrameSize),
		lastPacket: time.Now(),
	}
	rig.rx.streams[second.session] = second
	rig.users.setMuted("second", true)
	rig.users.setVolume(rxRigHash, 0.8)

	// A sink that counts instead of appending: the measurement is about the
	// tick, not about the test's own bookkeeping.
	sink := &countSink{}
	frame := make([]int16, FrameSamples)
	for i := range frame {
		frame[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}

	seq := int64(0)
	push := func() {
		rig.s.jit.Push(seq, frame)
		second.jit.Push(seq, frame)
		seq++
		now := time.Now()
		rig.s.lastPacket, second.lastPacket = now, now
	}
	tick := func() {
		push()
		rig.rx.tick(sink, false, &rig.users, 0)
	}

	// Warm up past priming, and past the buffers the states size on first use.
	for range 4 * jitterStartFrames {
		tick()
	}

	if got := testing.AllocsPerRun(200, tick); got != 0 {
		t.Errorf("the RX tick allocates %.1f times per frame, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// through the engine
// ---------------------------------------------------------------------------

// The engine-level statement of the same fact, over the real DSP loop: a peer
// the listener silenced reaches the speaker as nothing at all, while the
// device keeps being fed.
func TestEngineUserMuteSilencesOnePeer(t *testing.T) {
	const hash = "peerhash"
	packets := make(chan mumble.VoicePacket, 64)
	var seq atomic.Int64
	send := func(opus []byte, final bool) error {
		if final {
			return nil
		}
		select {
		case packets <- mumble.VoicePacket{Session: 9, Hash: hash, Sequence: seq.Add(1) - 1, Opus: opus}:
		default:
		}
		return nil
	}
	// The bare M2 shape: local mute must not depend on the DSP chain.
	e := NewEngine(Config{Packets: packets, Send: send, DSP: &DSPOptions{}, Log: slog.Default()})
	e.SetUserVolume(hash, 0.6)
	e.SetUserMute(hash, true)

	src := &sineSource{}
	sink := &collectSink{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go e.run(src, sink, stop, done)
	time.Sleep(500 * time.Millisecond)
	close(stop)
	<-done

	if got := sink.loudFrames(); got != 0 {
		t.Fatalf("%d audible frames from a locally muted peer, want 0", got)
	}
	sink.mu.Lock()
	frames := len(sink.rms)
	sink.mu.Unlock()
	if frames < 30 {
		t.Fatalf("only %d frames reached the sink, want a steady flow", frames)
	}
	// The gain survived the mute, ready for the unmute.
	if got := e.state.users.get(hash); !got.muted || got.volume != 0.6 {
		t.Fatalf("engine state = %+v, want the chosen gain kept", got)
	}
}
