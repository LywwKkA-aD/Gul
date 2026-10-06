package audio

import (
	"io"
	"log/slog"
	"runtime"
	"slices"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

// PLC must advance the decoder before later compressed frames, even when a
// reordered RTP burst is ingested completely before playback starts.
func TestReceiveRTPLossBeforeFutureDecode(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	packets, _ := phrasePackets(t, []int{18}, false)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	chain, err := newDSPChain(DSPOptions{}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.close()
	rx := newRxPipeline(Config{Log: log}, chain)
	defer rx.close()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var want [][]int16
	for i, p := range packets[0] {
		data := p.Opus
		if i == 6 || i == 7 {
			data = nil
			if i == 6 {
				rx.ingest(session.VoicePacket{Session: 1, Sequence: 6, LostFrames: 2})
			}
		} else {
			rx.ingest(p)
		}
		pcm := make([]int16, FrameSamples)
		if _, err := dec.Decode(data, pcm); err != nil {
			t.Fatal(err)
		}
		want = append(want, pcm)
	}
	sink := &phraseSink{}
	played := 0
	for range 100 {
		before := rx.vitals().Played + rx.vitals().Concealed
		rx.tick(sink, false, &userAudioState{}, 0)
		if rx.vitals().Played+rx.vitals().Concealed == before {
			continue
		}
		if played >= len(want) || !slices.Equal(sink.frame, want[played]) {
			t.Fatalf("frame %d differs from ordered Opus/PLC decoding", played)
		}
		played++
	}
	if counts := rx.vitals(); counts.Played != 16 || counts.Concealed != 2 {
		t.Fatalf("concealed audio must be counted separately: %+v", counts)
	}
	if played != len(want) {
		t.Fatalf("played %d, want %d", played, len(want))
	}
}

func TestReceiveRejectsInvalidLossMarkers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rx := newRxPipeline(Config{Log: log}, nil)
	defer rx.close()
	for _, n := range []int{-1, 1, 12, 13, 1000000} {
		rx.ingest(session.VoicePacket{Session: 7, LostFrames: n})
	}
	if len(rx.streams) != 0 {
		t.Fatal("loss without decoder history created a stream")
	}
}

func TestReceiveEpochResetDropsQueuedPCM(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	packets, _ := phrasePackets(t, []int{18}, false)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rx := newRxPipeline(Config{Log: log}, nil)
	defer rx.close()
	for _, p := range packets[0] {
		rx.ingest(p)
	}
	if len(rx.streams) != 1 {
		t.Fatal("missing queued stream")
	}
	rx.ingest(session.VoicePacket{Reset: true})
	if len(rx.streams) != 0 {
		t.Fatal("previous channel still has queued audio")
	}
	rx.ingest(packets[0][0])
	if len(rx.streams) != 1 {
		t.Fatal("new channel cannot start audio")
	}
}
