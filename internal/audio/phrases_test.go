package audio

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// phrasePackets uses the real codec and the sender's sequence reset. The
// separate decoder gives the exact PCM that lossless, unmodified playback
// must retain, including the encoded-silence terminator's delayed speech.
func phrasePackets(t *testing.T, lengths []int, emptyFinal bool) ([][]mumble.VoicePacket, [][]int16) {
	t.Helper()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var phrases [][]mumble.VoicePacket
	var want [][]int16
	pcm := make([]int16, FrameSamples)
	decoded := make([]int16, opus.MaxFrameSize)
	for phrase, length := range lengths {
		var packets []mumble.VoicePacket
		for seq := range length {
			for i := range pcm {
				pcm[i] = int16(2000 * math.Sin(2*math.Pi*float64(193+phrase*71)*float64(seq*FrameSamples+i)/SampleRate))
			}
			final := !emptyFinal && seq == length-1
			if final {
				clear(pcm)
			}
			data, err := enc.Encode(pcm, nil)
			if err != nil {
				t.Fatal(err)
			}
			packets = append(packets, mumble.VoicePacket{Session: 1, Key: "phrases", Sequence: int64(seq), Opus: data, Final: final})
			n, err := dec.Decode(data, decoded)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, slices.Clone(decoded[:n]))
		}
		if emptyFinal {
			packets = append(packets, mumble.VoicePacket{Session: 1, Key: "phrases", Sequence: int64(length), Final: true})
		}
		phrases = append(phrases, packets)
		if err := enc.Reset(); err != nil {
			t.Fatal(err)
		}
	}
	return phrases, want
}

type phraseSink struct{ frame []int16 }

func (s *phraseSink) WriteFrame(pcm []int16) bool {
	s.frame = append(s.frame[:0], pcm...)
	return true
}

func TestReceiveAdjacentPhrasesPreservesPCM(t *testing.T) {
	for _, emptyFinal := range []bool{false, true} {
		for _, nextLength := range []int{5, 20, 21} {
			for _, paced := range []bool{false, true} {
				name := fmt.Sprintf("empty_final_%t/next_%d/paced_%t", emptyFinal, nextLength, paced)
				t.Run(name, func(t *testing.T) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					packets, want := phrasePackets(t, []int{20, nextLength}, emptyFinal)
					log := slog.New(slog.NewTextHandler(io.Discard, nil))
					chain, err := newDSPChain(DSPOptions{}, log)
					if err != nil {
						t.Fatal(err)
					}
					defer chain.close()
					var talking bool
					rx := newRxPipeline(Config{Log: log, Callbacks: Callbacks{
						OnTalking: func(_ uint32, _ string, on bool) { talking = on },
					}}, chain)
					defer rx.close()
					sink := &phraseSink{}
					played := 0
					step := func() {
						t.Helper()
						before := rx.vitals().Played
						rx.tick(sink, false, &userAudioState{}, 0)
						if rx.vitals().Played == before {
							return
						}
						if played >= len(want) || !slices.Equal(sink.frame, want[played]) {
							t.Fatalf("playback frame %d differs from ordered decoder output; vitals=%+v", played, rx.vitals())
						}
						played++
					}
					for _, p := range packets[0] {
						rx.ingest(p)
					}
					for range 12 {
						step()
					}
					// Eight frames of the first phrase remain when the second
					// starts at wire sequence zero. Neither may be discarded.
					for _, p := range packets[1] {
						rx.ingest(p)
						if paced {
							step()
						}
					}
					for range len(want) + jitterSilenceTicks {
						step()
					}
					if played != len(want) {
						t.Fatalf("played %d/%d frames; vitals=%+v", played, len(want), rx.vitals())
					}
					if got := rx.vitals().JitterCounts; got != (JitterCounts{Played: uint64(len(want))}) {
						t.Fatalf("lossless adjacent phrases changed counters: %+v", got)
					}
					if talking || RMS(sink.frame) != 0 {
						t.Fatal("completed phrases did not return to silent idle")
					}
				})
			}
		}
	}
}
