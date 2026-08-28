package audio

import (
	"log/slog"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// streamIdleTimeout drops the per-user decoder and jitter state after this
// long without packets (PLAN.md 4.5: our own silence timeout).
const streamIdleTimeout = 3 * time.Second

// rxStream is the per-sender receive state, owned by the DSP goroutine.
type rxStream struct {
	session    uint32
	key        string
	dec        *opus.Decoder
	jit        *Jitter
	pcm        []int16 // decode buffer, up to a 60 ms packet
	talking    bool
	lastPacket time.Time
}

// rxPipeline is the incoming path (PLAN.md 4.4): passthrough packets ->
// per-user decode -> repack to 10 ms -> jitter -> mixer -> reference for
// AEC3 -> playback frame.
type rxPipeline struct {
	cfg     Config
	log     *slog.Logger
	chain   *dspChain
	streams map[uint32]*rxStream
	cues    cuePlayer
	mixer   *Mixer
	mix     []int16
	frame   []int16
	silence []int16
	outRMS  float64
}

func newRxPipeline(cfg Config, chain *dspChain) *rxPipeline {
	return &rxPipeline{
		cfg:     cfg,
		log:     cfg.Log,
		chain:   chain,
		streams: make(map[uint32]*rxStream),
		mixer:   NewMixer(),
		mix:     make([]int16, FrameSamples),
		frame:   make([]int16, FrameSamples),
		silence: make([]int16, FrameSamples),
	}
}

// drain moves every pending packet into the per-user jitter buffers.
func (r *rxPipeline) drain(packets <-chan mumble.VoicePacket) {
	if packets == nil {
		return
	}
	for {
		select {
		case p := <-packets:
			r.ingest(p)
		default:
			return
		}
	}
}

func (r *rxPipeline) ingest(p mumble.VoicePacket) {
	s := r.streams[p.Session]
	if s == nil {
		dec, err := opus.NewDecoder()
		if err != nil {
			r.log.Error("opus decoder", "error", err)
			return
		}
		s = &rxStream{
			session: p.Session,
			key:     p.Key,
			dec:     dec,
			jit:     NewJitter(),
			pcm:     make([]int16, opus.MaxFrameSize),
		}
		r.streams[p.Session] = s
	}
	s.lastPacket = time.Now()

	frames := 0
	if len(p.Opus) > 0 {
		n, err := s.dec.Decode(p.Opus, s.pcm)
		if err != nil {
			r.log.Warn("opus decode", "error", err, "session", p.Session)
		} else {
			// Repack: a 20/40/60 ms packet becomes 2/4/6 sequence-numbered
			// 10 ms frames (the wire sequence is counted in 10 ms units).
			frames = n / FrameSamples
			for i := range frames {
				s.jit.Push(p.Sequence+int64(i), s.pcm[i*FrameSamples:(i+1)*FrameSamples])
			}
		}
	}
	if p.Final {
		last := p.Sequence + int64(frames) - 1
		if frames == 0 {
			// An empty terminator ends the transmission after the frames of
			// the previous packet.
			last = p.Sequence - 1
		}
		s.jit.PushFinal(last)
	}
}

// tick produces exactly one playback frame from all active streams.
//
// extraSilence is the number of silent frames the playback device padded in
// since the last tick (underruns): the speaker really played them, so the
// echo canceller has to see them too, or its reference timeline slips one
// frame behind reality per underrun.
func (r *rxPipeline) tick(sink FrameSink, deafened bool, users *userAudioState, extraSilence int) {
	now := time.Now()
	for session, s := range r.streams {
		switch s.jit.Pop(r.frame) {
		case JitterFrame:
			r.setTalking(s, true)
		case JitterConceal:
			// PLC: the decoder invents the missing 10 ms.
			if _, err := s.dec.Decode(nil, r.frame[:FrameSamples]); err != nil {
				copy(r.frame, silentFrame[:])
			}
			r.setTalking(s, true)
		case JitterIdle:
			r.setTalking(s, false)
			if now.Sub(s.lastPacket) > streamIdleTimeout {
				s.dec.Close()
				delete(r.streams, session)
			}
			continue
		}
		if deafened {
			continue
		}
		// The stream is still decoded, still popped from its jitter buffer
		// and still reported as talking: a locally silenced person is
		// speaking, we have simply chosen not to hear them. Only the mix
		// skips them, and their gain is kept for the unmute (users.go).
		treatment := users.get(s.key)
		if treatment.muted {
			continue
		}
		r.mixer.Add(r.frame, treatment.volume)
	}

	// DECISION: cues are mixed after the deafen check, so they play while
	// deafened. Deafen silences other people; a cue is this client's own
	// confirmation that an action landed - and the mute and deafen cues in
	// particular would be silenced by exactly the state they report.
	// Passing through the mixer also lands them in the AEC reference below
	// and on the user's chosen playback device.
	r.cues.mix(r.mixer)

	r.mixer.Mix(r.mix)
	r.outRMS = RMS(r.mix)

	// Reference discipline (PLAN.md 4.4, refined): AEC3 must see exactly
	// what the speaker plays. Underrun padding is replayed as silence, and
	// a frame the full ring rejected never reaches the canceller at all -
	// so clock drift between the tick and the device surfaces as matching
	// reference corrections instead of a growing misalignment. Feeding
	// after the successful write is safe: the ring guarantees the frame
	// reaches the speaker at least one period later, so the reference
	// still precedes its echo.
	for range extraSilence {
		clear(r.silence)
		r.chain.reverse(r.silence)
	}
	if sink.WriteFrame(r.mix) {
		r.chain.reverse(r.mix)
	}
}

func (r *rxPipeline) setTalking(s *rxStream, talking bool) {
	if s.talking == talking {
		return
	}
	s.talking = talking
	if cb := r.cfg.Callbacks.OnTalking; cb != nil {
		cb(s.session, s.key, talking)
	}
}

func (r *rxPipeline) close() {
	for session, s := range r.streams {
		r.setTalking(s, false)
		s.dec.Close()
		delete(r.streams, session)
	}
}

// silentFrame is the fallback when even PLC fails.
var silentFrame [FrameSamples]int16
