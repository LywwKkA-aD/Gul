package opus_test

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	pion "github.com/pion/opus"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// fillSine writes a 440 Hz tone at ~0.3 of full scale, keeping the phase
// continuous across frames (frame counts samples already generated).
func fillSine(buf []int16, offset int) {
	const amp = 0.3 * 32767
	for i := range buf {
		t := float64(offset+i) / opus.SampleRate
		buf[i] = int16(amp * math.Sin(2*math.Pi*440*t))
	}
}

func rms(pcm []int16) float64 {
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

func TestVersion(t *testing.T) {
	t.Parallel()
	if v := opus.Version(); !strings.Contains(v, "1.6.1") {
		t.Fatalf("Version() = %q, want it to contain 1.6.1", v)
	}
}

func TestEncodeRejectsWrongFrame(t *testing.T) {
	t.Parallel()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	if _, err := enc.Encode(make([]int16, 100), nil); err == nil {
		t.Fatal("Encode accepted a 100-sample frame, want an error")
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	frame := make([]int16, opus.FrameSize)
	pcm := make([]int16, opus.FrameSize)
	packet := make([]byte, opus.MaxEncodedBytes)
	for i := range 50 {
		fillSine(frame, i*opus.FrameSize)
		data, err := enc.Encode(frame, packet)
		if err != nil {
			t.Fatalf("frame %d: Encode: %v", i, err)
		}
		if len(data) == 0 || len(data) > opus.MaxEncodedBytes {
			t.Fatalf("frame %d: packet of %d bytes", i, len(data))
		}
		n, err := dec.Decode(data, pcm)
		if err != nil {
			t.Fatalf("frame %d: Decode: %v", i, err)
		}
		if n != opus.FrameSize {
			t.Fatalf("frame %d: decoded %d samples, want %d", i, n, opus.FrameSize)
		}
		// After codec warm-up the tone must survive with comparable energy
		// (input RMS is ~6950 for a 0.3 FS sine).
		if got := rms(pcm[:n]); i > 10 && got < 3000 {
			t.Fatalf("frame %d: decoded RMS %.0f, tone lost", i, got)
		}
	}
}

func TestConcealment(t *testing.T) {
	t.Parallel()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	frame := make([]int16, opus.FrameSize)
	pcm := make([]int16, opus.FrameSize)
	for i := range 5 {
		fillSine(frame, i*opus.FrameSize)
		data, err := enc.Encode(frame, nil)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if _, err := dec.Decode(data, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	}

	// A lost frame: concealment must still produce a full frame.
	n, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if n != opus.FrameSize {
		t.Fatalf("concealed %d samples, want %d", n, opus.FrameSize)
	}
}

func TestUseAfterClose(t *testing.T) {
	t.Parallel()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.Close()
	if _, err := enc.Encode(make([]int16, opus.FrameSize), nil); !errors.Is(err, opus.ErrClosed) {
		t.Fatalf("Encode after Close: %v, want ErrClosed", err)
	}

	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	dec.Close()
	if _, err := dec.Decode(nil, make([]int16, opus.FrameSize)); !errors.Is(err, opus.ErrClosed) {
		t.Fatalf("Decode after Close: %v, want ErrClosed", err)
	}
}

// TestPionInterop is the independent conformance net: packets produced by
// the vendored libopus must decode with the pure-Go pion/opus decoder over
// every application profile we can emit.
func TestPionInterop(t *testing.T) {
	t.Parallel()
	bitrates := []int{24000, 40000, 64000, 96000}
	for _, bitrate := range bitrates {
		t.Run(fmt.Sprintf("%dbps", bitrate), func(t *testing.T) {
			t.Parallel()
			enc, err := opus.NewEncoder(bitrate)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			defer enc.Close()

			pdec := pion.NewDecoder()
			frame := make([]int16, opus.FrameSize)
			out := make([]int16, opus.MaxFrameSize)
			packet := make([]byte, opus.MaxEncodedBytes)
			for i := range 500 {
				fillSine(frame, i*opus.FrameSize)
				data, err := enc.Encode(frame, packet)
				if err != nil {
					t.Fatalf("frame %d: Encode: %v", i, err)
				}
				n, err := pdec.DecodeToInt16(data, out)
				if err != nil {
					t.Fatalf("frame %d: pion decode: %v", i, err)
				}
				if n != opus.FrameSize {
					t.Fatalf("frame %d: pion decoded %d samples, want %d", i, n, opus.FrameSize)
				}
			}
		})
	}
}

func BenchmarkEncode(b *testing.B) {
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	frame := make([]int16, opus.FrameSize)
	fillSine(frame, 0)
	packet := make([]byte, opus.MaxEncodedBytes)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := enc.Encode(frame, packet); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	dec, err := opus.NewDecoder()
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	frame := make([]int16, opus.FrameSize)
	fillSine(frame, 0)
	data, err := enc.Encode(frame, nil)
	if err != nil {
		b.Fatal(err)
	}
	pcm := make([]int16, opus.FrameSize)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := dec.Decode(data, pcm); err != nil {
			b.Fatal(err)
		}
	}
}
