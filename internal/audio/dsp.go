package audio

import (
	"log/slog"

	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
	"github.com/LywwKkA-aD/Gul/internal/dsp/rnnoise"
)

// DSPOptions selects the processing shape of the voice engine (PLAN.md 4.3).
// The zero value is the bare M2 pipeline - no processing at all - which is
// what tests and latency diagnostics ask for explicitly; the product default
// is DefaultDSP.
type DSPOptions struct {
	// EchoCancel enables AEC3. The high-pass filter and AGC2 come with the
	// APM instance, which exists while either this or NS asks for it.
	EchoCancel bool
	// NS is the suppressor level inside APM. Soft levels leave the heavy
	// lifting to RNNoise; NSHigh is the APM-only fallback of the M3 A/B.
	NS apm.NSLevel
	// RNNoise puts the DNN denoiser in the audio path.
	RNNoise bool
	// Gate holds transmission until the VAD or PTT opens it. RNNoise runs
	// as the VAD source even when RNNoise itself is off the audio path.
	Gate bool
}

// DefaultDSP is the M3 product default: full chain with the APM suppressor
// soft under RNNoise (PLAN.md 4.3; the A/B against NSHigh-only is pending).
func DefaultDSP() DSPOptions {
	return DSPOptions{EchoCancel: true, NS: apm.NSLow, RNNoise: true, Gate: true}
}

// dspChain owns the DSP states of one engine run. Everything here lives on
// the DSP goroutine (LockOSThread) and is released by an explicit close.
type dspChain struct {
	log     *slog.Logger
	proc    *apm.APM          // nil when neither AEC nor NS is configured
	dn      *rnnoise.Denoiser // nil when neither RNNoise nor Gate is configured
	denoise bool              // write the RNNoise output back into the frame
	fbuf    []float32
}

func newDSPChain(o DSPOptions, log *slog.Logger) (*dspChain, error) {
	c := &dspChain{log: log, denoise: o.RNNoise}
	if o.EchoCancel || o.NS != apm.NSOff {
		p, err := apm.New(apm.Config{EchoCancel: o.EchoCancel, NS: o.NS})
		if err != nil {
			return nil, err
		}
		c.proc = p
	}
	if o.RNNoise || o.Gate {
		dn, err := rnnoise.NewDenoiser()
		if err != nil {
			c.close()
			return nil, err
		}
		c.dn = dn
		c.fbuf = make([]float32, FrameSamples)
		// The main-branch denoiser applies the gains of frame N to the
		// spectrum of frame N-1, so its very first output frame is garbage
		// and the upstream demo throws it away. Swallow it here, on
		// silence, instead of special-casing the first pipeline tick.
		if _, err := dn.Process(c.fbuf); err != nil {
			c.close()
			return nil, err
		}
	}
	return c, nil
}

// tx runs the send side in place: APM (HPF -> AEC3 -> NS -> AGC2), then
// RNNoise in the S16-scale float domain. The return value is the voice
// probability for the gate - 1 when no VAD source is configured, and on a
// chain error, because failing open beats silently muting the user.
func (c *dspChain) tx(frame []int16) float32 {
	if c.proc != nil {
		if err := c.proc.ProcessStream(frame); err != nil {
			c.log.Warn("apm process", "error", err)
		}
	}
	if c.dn == nil {
		return 1
	}
	// Direct cast, no normalization: RNNoise works in the S16 scale and
	// treats +/-1.0 input as silence (the trap PLAN.md 4.3 warns about).
	// In VAD-only mode fbuf is a scratch copy and the frame stays as APM
	// left it.
	for i, s := range frame {
		c.fbuf[i] = float32(s)
	}
	vad, err := c.dn.Process(c.fbuf)
	if err != nil {
		c.log.Warn("rnnoise process", "error", err)
		return 1
	}
	if c.denoise {
		// RNNoise does not bound its output amplitude; clip on the way back.
		for i, v := range c.fbuf {
			frame[i] = clipS16(v)
		}
	}
	return vad
}

// reverse hands one frame of speaker audio to the echo canceller. The
// caller keeps the invariant of PLAN.md 4.4: what goes here is exactly what
// the playback device accepted, in order, silence included.
func (c *dspChain) reverse(frame []int16) {
	if c.proc == nil {
		return
	}
	if err := c.proc.ProcessReverseStream(frame); err != nil {
		c.log.Warn("apm reverse", "error", err)
	}
}

// delayHint passes a rough playback-path delay to APM. AEC3 estimates the
// true delay from the signals; the hint only centers its search.
func (c *dspChain) delayHint(ms int) {
	if c.proc != nil {
		c.proc.SetStreamDelayMs(ms)
	}
}

func (c *dspChain) close() {
	if c.dn != nil {
		_ = c.dn.Close()
		c.dn = nil
	}
	if c.proc != nil {
		_ = c.proc.Close()
		c.proc = nil
	}
}

func clipS16(v float32) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}
