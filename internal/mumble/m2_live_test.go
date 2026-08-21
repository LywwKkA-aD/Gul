//go:build live

package mumble

import (
	"math"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// TestTwoManagersVoice is the M2 DoD smoke at the transport layer: client A
// encodes a tone and sends it through the dev stand, client B receives the
// passthrough packets and decodes them back to audible audio.
// Run with the stand up: task murmur:up && go test -tags live ./internal/mumble -run TestTwoManagersVoice
func TestTwoManagersVoice(t *testing.T) {
	a := newLiveManager(t, "gul-voice-a")
	defer a.mgr.Close()
	b := newLiveManager(t, "gul-voice-b")
	defer b.mgr.Close()

	a.mgr.Connect("127.0.0.1:64738", "gul-voice-a", "")
	b.mgr.Connect("127.0.0.1:64738", "gul-voice-b", "")
	waitState(t, a, domain.StateConnected)
	waitState(t, b, domain.StateConnected)
	waitRoot(t, b)

	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	// One second of a 440 Hz tone at 0.3 FS, paced like the real pipeline.
	const frames = 100
	frame := make([]int16, opus.FrameSize)
	const amp = 0.3 * 32767
	for i := range frames {
		for n := range frame {
			tm := float64(i*opus.FrameSize+n) / opus.SampleRate
			frame[n] = int16(amp * math.Sin(2*math.Pi*440*tm))
		}
		data, err := enc.Encode(frame, nil)
		if err != nil {
			t.Fatalf("frame %d: encode: %v", i, err)
		}
		if err := a.mgr.SendVoice(data, false); err != nil {
			t.Fatalf("frame %d: send: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The pipeline closes a transmission with one silent frame flagged as
	// the terminator: murmur does not route empty audio packets.
	clear(frame)
	data, err := enc.Encode(frame, nil)
	if err != nil {
		t.Fatalf("terminator encode: %v", err)
	}
	if err := a.mgr.SendVoice(data, true); err != nil {
		t.Fatalf("terminator: %v", err)
	}

	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	received := 0
	audible := 0
	sawFinal := false
	lastSeq := int64(-1)
	pcm := make([]int16, opus.MaxFrameSize)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !sawFinal {
		select {
		case p := <-b.mgr.VoicePackets():
			if p.Final {
				sawFinal = true
				continue
			}
			received++
			if p.Sequence <= lastSeq {
				t.Fatalf("sequence went from %d to %d", lastSeq, p.Sequence)
			}
			lastSeq = p.Sequence
			n, err := dec.Decode(p.Opus, pcm)
			if err != nil {
				t.Fatalf("packet %d: decode: %v", received, err)
			}
			var sum float64
			for _, s := range pcm[:n] {
				sum += float64(s) * float64(s)
			}
			if math.Sqrt(sum/float64(n)) > 1000 {
				audible++
			}
		case <-deadline.C:
			t.Fatalf("timed out: received %d packets, final=%v", received, sawFinal)
		}
	}

	if received < frames*9/10 {
		t.Fatalf("received %d of %d voice packets", received, frames)
	}
	if audible < received/2 {
		t.Fatalf("only %d of %d packets decoded to audible audio", audible, received)
	}
	if drops := b.mgr.VoiceDrops(); drops != 0 {
		t.Logf("receive path dropped %d packets", drops)
	}
	t.Logf("voice smoke: %d packets, %d audible, final received", received, audible)
}
