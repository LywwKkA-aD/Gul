package audio

import (
	"log/slog"

	"gul/internal/dsp/opus"
)

// txPipeline is the M2 outgoing path: mic s16 frame -> opus -> transport.
// APM and RNNoise slot in front of the encoder in M3.
type txPipeline struct {
	enc     *opus.Encoder
	send    func(opus []byte, final bool) error
	log     *slog.Logger
	frame   []int16
	packet  []byte
	talking bool
	micRMS  float64
}

func newTxPipeline(cfg Config) (*txPipeline, error) {
	enc, err := opus.NewEncoder(cfg.Bitrate)
	if err != nil {
		return nil, err
	}
	return &txPipeline{
		enc:    enc,
		send:   cfg.Send,
		log:    cfg.Log,
		frame:  make([]int16, FrameSamples),
		packet: make([]byte, opus.MaxEncodedBytes),
	}, nil
}

// tick drains every frame the capture ring has accumulated. Without a gate
// (PTT/VAD are M3) an unmuted mic transmits continuously.
func (t *txPipeline) tick(src FrameSource, muted bool) {
	for src.ReadFrame(t.frame) {
		t.micRMS = RMS(t.frame)
		if muted {
			t.finish()
			continue
		}
		data, err := t.enc.Encode(t.frame, t.packet)
		if err != nil {
			t.log.Error("opus encode", "error", err)
			continue
		}
		// Send takes ownership of the bytes, so the reusable encode buffer
		// cannot leave the pipeline: hand over a copy (~50 B per 10 ms).
		if err := t.send(append([]byte(nil), data...), false); err != nil {
			t.log.Warn("voice send", "error", err)
		}
		t.talking = true
	}
}

// finish closes an active transmission with an empty terminator packet.
// DECISION: the terminator carries no audio (the alternative - holding
// every frame one tick to flag the last one - is a hidden 10 ms latency
// the plan explicitly forbids). Interop with the official client is part
// of the M2 DoD checks.
func (t *txPipeline) finish() {
	if !t.talking {
		return
	}
	t.talking = false
	if err := t.send(nil, true); err != nil {
		t.log.Warn("voice terminator", "error", err)
	}
	if err := t.enc.Reset(); err != nil {
		t.log.Warn("encoder reset", "error", err)
	}
}

func (t *txPipeline) close() {
	t.enc.Close()
}
