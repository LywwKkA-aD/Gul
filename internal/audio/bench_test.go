package audio

import (
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// Per-frame cost of the voice pipelines (PLAN.md 4.6). The DSP goroutine
// gets one 10 ms slot per frame and has to run both directions inside it,
// so what these benchmarks report has to stay far under 10 ms per iteration
// - and allocate nothing, because a garbage collection that lands on the
// audio thread is a dropout.
//
// Both run on the committed speech fixture rather than on synthetic tones:
// Opus and the DSP states are signal dependent, and silence or a steady sine
// is the cheapest input either of them will ever see.

// benchBitrate is the M2/M3 default encoder target (Config.Bitrate).
const benchBitrate = 40000

// benchFrameAt copies the fixture frame at index f into dst, cycling. The
// copy is part of the realistic cost: the capture ring hands the pipeline a
// copy of every frame too (FrameSource.ReadFrame).
func benchFrameAt(dst []int16, speech []int16, f int) {
	frames := len(speech) / FrameSamples
	lo := (f % frames) * FrameSamples
	copy(dst, speech[lo:lo+FrameSamples])
}

// BenchmarkTxFrame is one full TX tick: the echo reference of the frame that
// just went to the speaker, the capture chain (APM with AEC3 plus RNNoise),
// the transmit gate and the Opus encoder - the product default shape,
// DefaultDSP.
//
// The reverse stream belongs in the measurement: AEC3 only does real work
// while it has a live render signal, so timing tx() against a silent render
// path would report a fraction of the true per-tick cost. The two streams
// are different parts of the fixture so the canceller cannot trivially match
// one to the other.
//
// Measured on an Apple M4: 0.58 ms per frame, about 6 percent of the 10 ms
// slot, of which RNNoise is 0.44 ms, the Opus encoder 0.08 ms and APM with
// AEC3 0.06 ms.
func BenchmarkTxFrame(b *testing.B) {
	speech := goldenSpeech(b)
	chain := goldenChain(b, DefaultDSP())
	gate := NewGate()

	enc, err := opus.NewEncoder(benchBitrate)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	b.Cleanup(enc.Close)

	frame := make([]int16, FrameSamples)
	render := make([]int16, FrameSamples)
	packet := make([]byte, opus.MaxEncodedBytes)
	renderOffset := len(speech) / FrameSamples / 2

	b.ReportAllocs()
	b.ResetTimer()
	f := 0
	for b.Loop() {
		benchFrameAt(render, speech, f+renderOffset)
		chain.reverse(render)

		benchFrameAt(frame, speech, f)
		vad := chain.tx(frame)
		if !gate.Update(vad, false) {
			// A closed gate skips the encoder in the pipeline as well; the
			// fixture keeps it open for all but the pauses.
			f++
			continue
		}
		if _, err := enc.Encode(frame, packet); err != nil {
			b.Fatalf("Encode: %v", err)
		}
		f++
	}
}

// BenchmarkRxFrame is one full RX tick for a single remote speaker: decode
// the arriving packet, put it through the jitter buffer, mix it and hand the
// result to the echo canceller as the reference, the way pipeline_rx does
// (per-user volume lookup and multi-stream mixing aside). Playback itself
// is a memcpy into the device ring and is not part of the DSP cost.
func BenchmarkRxFrame(b *testing.B) {
	speech := goldenSpeech(b)
	chain := goldenChain(b, DefaultDSP())

	// Encode the fixture once, into the 10 ms packets the wire carries.
	enc, err := opus.NewEncoder(benchBitrate)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	b.Cleanup(enc.Close)
	frames := len(speech) / FrameSamples
	packets := make([][]byte, frames)
	for f := range frames {
		lo := f * FrameSamples
		data, err := enc.Encode(speech[lo:lo+FrameSamples], nil)
		if err != nil {
			b.Fatalf("Encode frame %d: %v", f, err)
		}
		packets[f] = append([]byte(nil), data...)
	}

	dec, err := opus.NewDecoder()
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}
	b.Cleanup(dec.Close)

	jit := NewJitter()
	mixer := NewMixer()
	pcm := make([]int16, opus.MaxFrameSize)
	frame := make([]int16, FrameSamples)
	mix := make([]int16, FrameSamples)

	// Prime the buffer to its start depth: a jitter buffer that is still
	// filling hands out nothing, and an empty one is not the steady state
	// the DSP tick is budgeted for.
	seq := int64(0)
	for range jitterStartFrames {
		n, err := dec.Decode(packets[seq%int64(frames)], pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
		jit.Push(seq, pcm[:n])
		seq++
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		n, err := dec.Decode(packets[seq%int64(frames)], pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
		jit.Push(seq, pcm[:n])
		seq++

		if jit.Pop(frame) == JitterFrame {
			mixer.Add(frame, 1)
		}
		mixer.Mix(mix)
		chain.reverse(mix)
	}
}

// TestTxFrameDoesNotAllocate pins the steady state of the DSP work in the
// send path: after the states have grown their buffers, chain + gate +
// encode must not allocate (PLAN.md 4.6). The one allocation the real
// pipeline does make - the ~50 B ownership copy handed to the transport per
// transmitted frame - is pipeline_tx's documented cost, outside this pin.
func TestTxFrameDoesNotAllocate(t *testing.T) {
	speech := goldenSpeech(t)
	chain := goldenChain(t, DefaultDSP())
	gate := NewGate()

	enc, err := opus.NewEncoder(benchBitrate)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	t.Cleanup(enc.Close)

	frame := make([]int16, FrameSamples)
	render := make([]int16, FrameSamples)
	packet := make([]byte, opus.MaxEncodedBytes)

	tick := func(f int) {
		benchFrameAt(render, speech, f+37)
		chain.reverse(render)
		benchFrameAt(frame, speech, f)
		vad := chain.tx(frame)
		gate.Update(vad, false)
		if _, err := enc.Encode(frame, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}

	// Warm up: AEC3 and the encoder allocate while they size their state.
	for f := range 2 * goldenFramesPerSecond {
		tick(f)
	}

	f := 0
	got := testing.AllocsPerRun(200, func() {
		tick(f)
		f++
	})
	if got != 0 {
		t.Errorf("the TX tick allocates %.1f times per frame, want 0", got)
	}
}
