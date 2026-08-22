package audio

import "log/slog"

// ProcessOffline runs a whole recording through a fresh DSP chain, frame by
// frame, and returns the processed audio. It serves offline tooling - the
// blind A/B listening kit (cmd/gul-dsp) and file-based experiments - and is
// not part of the realtime path: it allocates, it builds and tears down the
// DSP states on every call, and it never runs on the DSP goroutine.
//
// Only the capture chain runs. There is no render stream in a file, so echo
// cancellation has no reference to work from (opts.EchoCancel processes the
// frames but cancels nothing) and the transmit gate is not consulted - the
// output holds every input frame, denoised.
//
// The chain constructor already swallows RNNoise's first output frame, which
// the network computes from an empty spectrum, so the result starts with real
// audio. A trailing partial frame is zero-padded for processing and trimmed
// back off, so the output has exactly the length of the input.
func ProcessOffline(samples []int16, opts DSPOptions) ([]int16, error) {
	chain, err := newDSPChain(opts, slog.Default())
	if err != nil {
		return nil, err
	}
	defer chain.close()

	out := make([]int16, len(samples))
	frame := make([]int16, FrameSamples)
	for off := 0; off < len(samples); off += FrameSamples {
		n := copy(frame, samples[off:])
		clear(frame[n:])
		chain.tx(frame)
		copy(out[off:], frame[:n])
	}
	return out, nil
}
