package opus

import (
	"math"
	"math/rand/v2"
	"testing"
)

// speechShaped builds frames with the shape of speech - syllables of varying
// loudness with gaps - rather than a steady tone, which both encoder modes
// would compress identically and prove nothing about.
func speechShaped(count int) [][]int16 {
	frames := make([][]int16, 0, count)
	phase := 0.0
	for i := range count {
		frame := make([]int16, FrameSize)
		amp := 0.0
		switch (i / 7) % 4 {
		case 0:
			amp = 0.30 * 32768
		case 1:
			amp = 0.08 * 32768
		case 2:
			amp = 0.18 * 32768
		}
		freq := 120.0 + float64((i*37)%400)
		for n := range frame {
			phase += 2 * math.Pi * freq / SampleRate
			noise := (rand.Float64() - 0.5) * amp * 0.3
			frame[n] = int16(math.Max(-32768, math.Min(32767, amp*math.Sin(phase)+noise)))
		}
		frames = append(frames, frame)
	}
	return frames
}

// A hundred identical packets a second is something a classifier recognises
// without reading a byte of them, and constant bitrate produces exactly that:
// measured on this signal it gave ONE size, 50 bytes, for every frame. The
// encoder must not go back to it.
func TestEncoderFrameSizesVary(t *testing.T) {
	t.Parallel()
	enc, err := NewEncoder(40000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()

	buf := make([]byte, MaxEncodedBytes)
	sizes := make(map[int]int)
	total := 0
	frames := speechShaped(400)
	for _, frame := range frames {
		data, err := enc.Encode(frame, buf)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		sizes[len(data)]++
		total += len(data)
	}

	if len(sizes) < 10 {
		t.Fatalf("only %d distinct frame sizes; the stream is as good as constant", len(sizes))
	}
	// A sanity bound on the average, not a check of the VBR constraint: on this
	// synthetic signal constrained and unconstrained land 51 and 55 bytes
	// apart, so no threshold here separates them without being fragile. The
	// constraint is kept because the server enforces a per-user bandwidth
	// limit and real speech has passages this signal does not.
	mean := float64(total) / float64(len(frames))
	if want := 40000.0 / 8 / 100; mean > want*1.3 {
		t.Fatalf("mean frame = %.1f bytes, past the %.0f the bitrate asks for", mean, want)
	}
}
