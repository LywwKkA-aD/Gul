package audio

import (
	"io"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

func checkPhrasePlayback(t *testing.T, packets []mumble.VoicePacket, want [][]int16) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	chain, err := newDSPChain(DSPOptions{}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.close()
	rx := newRxPipeline(Config{Log: log}, chain)
	defer rx.close()
	for _, p := range packets {
		rx.ingest(p)
	}
	sink := &phraseSink{}
	for i, frame := range want {
		rx.tick(sink, false, &userAudioState{}, 0)
		if !slices.Equal(sink.frame, frame) {
			t.Fatalf("frame %d differs or was delayed; vitals=%+v", i, rx.vitals())
		}
	}
	for range 2 * jitterSilenceTicks {
		rx.tick(sink, false, &userAudioState{}, 0)
		if RMS(sink.frame) != 0 {
			t.Fatal("finished input produced extra audio")
		}
	}
	if got := rx.vitals().JitterCounts; got != (JitterCounts{Played: uint64(len(want))}) {
		t.Fatalf("phrase boundaries changed counters: %+v", got)
	}
}

func TestReceivePhraseBoundaryEdgeCases(t *testing.T) {
	for _, mode := range []string{"nonzero_starts", "empty_phrases", "invalid_finals", "stale_finals", "nonzero_stale_finals", "invalid_sequences", "offset_overflow"} {
		t.Run(mode, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			phrases, want := phrasePackets(t, []int{3, 2, 4}, true)
			var packets []mumble.VoicePacket
			for i, phrase := range phrases {
				for _, p := range phrase {
					switch mode {
					case "nonzero_starts":
						p.Sequence += int64(i+1) * 5000
					case "invalid_finals":
						if p.Final {
							p.Opus = []byte{3} // code 3 without its frame-count byte
						}
					case "stale_finals":
						if p.Final {
							p.Sequence = 0
						}
					case "nonzero_stale_finals":
						p.Sequence += int64(i+1) * 5000
						if p.Final {
							p.Sequence = 0
						}
					case "offset_overflow":
						if i == 0 {
							p.Sequence += 1000
						}
						if i == 2 {
							// The old offset would overflow, but a new phrase
							// must compute a fresh mapping before using it.
							p.Sequence += math.MaxInt64 - 300
						}
					}
					packets = append(packets, p)
					if mode == "invalid_sequences" || (mode == "offset_overflow" && i == 1 && !p.Final) {
						for _, seq := range []int64{-1, math.MinInt64, math.MaxInt64, math.MaxInt64 - 300} {
							if mode == "invalid_sequences" && seq == math.MaxInt64-300 {
								continue
							}
							invalid := p
							invalid.Sequence = seq
							packets = append(packets, invalid)
						}
					}
				}
				if mode == "empty_phrases" {
					packets = append(packets,
						mumble.VoicePacket{Session: 1, Final: true},
						mumble.VoicePacket{Session: 1, Opus: []byte{3}, Final: true})
				}
			}
			checkPhrasePlayback(t, packets, want)
		})
	}
}

// Build legal Opus code-3 CBR packets from repeated single CELT frames.
// Every payload has the same TOC and length; the real decoder verifies the
// framing and provides reference PCM for the 20/40/60 ms repacking path.
func TestReceiveMultiFramePhraseBoundaries(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	enc, err := opus.NewEncoder(64000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	data, err := enc.Encode(jitterFilled(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if data[0]&3 != 0 {
		t.Fatal("fixture must contain one Opus frame")
	}
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var packets []mumble.VoicePacket
	var want [][]int16
	pcm := make([]int16, opus.MaxFrameSize)
	for _, frames := range []int{2, 4, 6, 2} {
		packet := []byte{data[0] | 3, byte(frames)}
		for range frames {
			packet = append(packet, data[1:]...)
		}
		n, err := dec.Decode(packet, pcm)
		if err != nil || n != frames*FrameSamples {
			t.Fatalf("%d-frame fixture decoded %d samples: %v", frames, n, err)
		}
		packets = append(packets, mumble.VoicePacket{Session: 1, Key: "phrases", Opus: packet, Final: true})
		for i := range frames {
			want = append(want, slices.Clone(pcm[i*FrameSamples:(i+1)*FrameSamples]))
		}
	}
	checkPhrasePlayback(t, packets, want)
}

func TestReceiveSequenceRestartAfterMissingFinal(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	phrases, want := phrasePackets(t, []int{3, 3, 3}, false)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	chain, err := newDSPChain(DSPOptions{}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.close()
	rx := newRxPipeline(Config{Log: log}, chain)
	defer rx.close()
	for i, phrase := range phrases[:2] {
		for _, p := range phrase {
			if i == 1 {
				p.Sequence += 5000
				p.Final = false // this sender never announced its end
			}
			rx.ingest(p)
		}
	}
	sink := &phraseSink{}
	for i := range 6 {
		rx.tick(sink, false, &userAudioState{}, 0)
		if !slices.Equal(sink.frame, want[i]) {
			t.Fatalf("initial frame %d differs", i)
		}
	}
	// A genuine backwards restart must still recover after a previous phrase
	// introduced a negative wire-to-local offset.
	for _, p := range phrases[2] {
		rx.ingest(p)
	}
	for i := 6; i < len(want); i++ {
		rx.tick(sink, false, &userAudioState{}, 0)
		if !slices.Equal(sink.frame, want[i]) {
			t.Fatalf("restarted frame %d differs; vitals=%+v", i, rx.vitals())
		}
	}
	if got := rx.vitals().JitterCounts; got != (JitterCounts{Played: uint64(len(want))}) {
		t.Fatalf("restart lost or invented frames: %+v", got)
	}
}
