package audio

import (
	"log/slog"
	"sync"
	"time"

	"gul/internal/dsp/opus"
	"gul/internal/mumble"
)

// streamIdleTimeout drops the per-user decoder and jitter state after this
// long without packets (PLAN.md 4.5: our own silence timeout).
const streamIdleTimeout = 3 * time.Second

// rxStream is the per-sender receive state, owned by the DSP goroutine.
type rxStream struct {
	session    uint32
	hash       string
	dec        *opus.Decoder
	jit        *Jitter
	pcm        []int16 // decode buffer, up to a 60 ms packet
	talking    bool
	lastPacket time.Time
}

// rxPipeline is the M2 incoming path: passthrough packets -> per-user
// decode -> repack to 10 ms -> jitter -> mixer -> playback frame.
type rxPipeline struct {
	cfg     Config
	log     *slog.Logger
	streams map[uint32]*rxStream
	mixer   *Mixer
	mix     []int16
	frame   []int16
	outRMS  float64
}

func newRxPipeline(cfg Config) *rxPipeline {
	return &rxPipeline{
		cfg:     cfg,
		log:     cfg.Log,
		streams: make(map[uint32]*rxStream),
		mixer:   NewMixer(),
		mix:     make([]int16, FrameSamples),
		frame:   make([]int16, FrameSamples),
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
			hash:    p.Hash,
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
func (r *rxPipeline) tick(sink FrameSink, deafened bool, volumes *sync.Map) {
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
		volume := float32(1.0)
		if v, ok := volumes.Load(s.hash); ok {
			volume = v.(float32)
		}
		r.mixer.Add(r.frame, volume)
	}

	r.mixer.Mix(r.mix)
	r.outRMS = RMS(r.mix)
	sink.WriteFrame(r.mix)
}

func (r *rxPipeline) setTalking(s *rxStream, talking bool) {
	if s.talking == talking {
		return
	}
	s.talking = talking
	if cb := r.cfg.Callbacks.OnTalking; cb != nil {
		cb(s.session, s.hash, talking)
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
