package audio

import (
	"log/slog"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// txPipeline is the outgoing path (PLAN.md 4.3): mic s16 frame -> APM ->
// RNNoise -> gate -> opus -> transport. The chain always runs, muted or
// not, so the DSP states stay warm and the mic meter shows the processed
// level; muted and gate-closed frames simply never reach the encoder.
type txPipeline struct {
	enc           *opus.Encoder
	chain         *dspChain
	gate          *Gate // nil when the DSP options disable gating
	send          func(opus []byte, final bool) error
	onSelfTalking func(talking bool)
	log           *slog.Logger
	frame         []int16
	packet        []byte
	talking       bool
	micRMS        float64
}

func newTxPipeline(cfg Config, chain *dspChain, gate *Gate) (*txPipeline, error) {
	enc, err := opus.NewEncoder(cfg.Bitrate)
	if err != nil {
		return nil, err
	}
	return &txPipeline{
		enc:           enc,
		chain:         chain,
		gate:          gate,
		send:          cfg.Send,
		onSelfTalking: cfg.Callbacks.OnSelfTalking,
		log:           cfg.Log,
		frame:         make([]int16, FrameSamples),
		packet:        make([]byte, opus.MaxEncodedBytes),
	}, nil
}

// tick drains every frame the capture ring has accumulated.
func (t *txPipeline) tick(src FrameSource, muted, ptt bool) {
	for src.ReadFrame(t.frame) {
		vad := t.chain.tx(t.frame)
		t.micRMS = RMS(t.frame)

		transmit := !muted
		if transmit && t.gate != nil {
			transmit = t.gate.Update(vad, ptt)
		}
		if !transmit {
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
		t.setTalking(true)
	}
}

// setTalking reports a change of our own transmit state. The callback runs on
// the DSP goroutine, like every other engine callback, and must not block.
func (t *txPipeline) setTalking(talking bool) {
	if t.talking == talking {
		return
	}
	t.talking = talking
	if cb := t.onSelfTalking; cb != nil {
		cb(talking)
	}
}

// finish closes an active transmission with one encoded frame of silence
// carrying the terminator flag. DECISION: murmur does not route empty
// audio packets (verified against the live stand), and holding every
// frame one tick to flag the last one would be the hidden 10 ms latency
// the plan explicitly forbids - so the transmission tail is a single
// silent frame instead.
func (t *txPipeline) finish() {
	if !t.talking {
		return
	}
	t.setTalking(false)
	clear(t.frame)
	data, err := t.enc.Encode(t.frame, t.packet)
	if err != nil {
		t.log.Warn("terminator encode", "error", err)
	} else if err := t.send(append([]byte(nil), data...), true); err != nil {
		t.log.Warn("voice terminator", "error", err)
	}
	if err := t.enc.Reset(); err != nil {
		t.log.Warn("encoder reset", "error", err)
	}
}

func (t *txPipeline) close() {
	t.enc.Close()
}
