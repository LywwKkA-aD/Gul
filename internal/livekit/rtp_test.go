package livekit

import (
	"testing"
	"time"

	"github.com/pion/rtp"
)

func packet(seq uint16, ts uint32) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: seq, Timestamp: ts}, Payload: []byte{0xF0, 0x01}}
}

func TestRTPReordersAndDropsDuplicatesBeforeDecoding(t *testing.T) {
	start := time.Unix(1, 0)
	q := newRTPQueue(17, "s:livekit:17")
	first := q.push(packet(100, 900), start)
	if len(first) != 1 || first[0].Sequence != 0 {
		t.Fatalf("first: %+v", first)
	}
	if got := q.push(packet(102, 1860), start); len(got) != 0 {
		t.Fatal("out-of-order escaped")
	}
	got := q.push(packet(101, 1380), start)
	if len(got) != 2 || got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Fatalf("order: %+v", got)
	}
	if got := q.push(packet(101, 1380), start); len(got) != 0 {
		t.Fatal("duplicate escaped")
	}
}

func TestRTPWrapAndMissingPacketDeadline(t *testing.T) {
	now := time.Unix(1, 0)
	q := newRTPQueue(1, "peer")
	q.push(packet(65535, 0xffffff00), now)
	q.push(packet(1, 704), now)
	if got := q.flush(now.Add(reorderWait / 2)); len(got) != 0 {
		t.Fatal("released gap too soon")
	}
	got := q.flush(now.Add(reorderWait))
	if len(got) != 2 || got[0].LostFrames != 1 || got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Fatalf("wrap: %+v", got)
	}
	if got := q.push(packet(0, 224), now); len(got) != 0 {
		t.Fatal("late packet escaped")
	}
}

func TestRTPInactivityEndsShortPhraseOnceAndResumesWithoutSilenceBacklog(t *testing.T) {
	now := time.Unix(1, 0)
	q := newRTPQueue(1, "peer")
	q.push(packet(5, 1000), now)
	got := q.flush(now.Add(phraseSilence))
	if len(got) != 1 || !got[0].Final || got[0].Sequence != 1 {
		t.Fatalf("final: %+v", got)
	}
	if got := q.flush(now.Add(2 * phraseSilence)); len(got) != 0 {
		t.Fatal("repeated final")
	}
	got = q.push(packet(6, 241000), now.Add(5*time.Second))
	if len(got) != 1 || got[0].Sequence != 500 {
		t.Fatalf("DTX clock: %+v", got)
	}
}

func TestOpusDurationRejectsUnsupportedFrames(t *testing.T) {
	for _, payload := range [][]byte{nil, {0xff}, {0x80, 1}, {0x83, 63}} {
		if opusSamples(payload) != 0 {
			t.Fatalf("accepted unsupported %x", payload)
		}
	}
	if got := opusSamples([]byte{0xf0, 1}); got != 480 {
		t.Fatalf("10ms=%d", got)
	}
	if got := opusSamples([]byte{0xf8, 1}); got != 960 {
		t.Fatalf("20ms=%d", got)
	}
}

func TestTXClockKeepsSequenceAcrossTalkspurtsAndAdvancesThroughSilence(t *testing.T) {
	now := time.Unix(1, 0)
	c := newTXClock(65535, 0xffffff00, now)
	a := c.packet([]byte{1}, false, now)
	b := c.packet([]byte{2}, true, now.Add(10*time.Millisecond))
	d := c.packet([]byte{3}, false, now.Add(time.Second))
	if a.SequenceNumber != 65535 || b.SequenceNumber != 0 || d.SequenceNumber != 1 {
		t.Fatal("sequence reset")
	}
	if !a.Marker || b.Marker || !d.Marker {
		t.Fatal("incorrect talkspurt markers")
	}
	if b.Timestamp-a.Timestamp != 480 || d.Timestamp-a.Timestamp != 48000 {
		t.Fatal("incorrect timestamp clock")
	}
}

func TestTXClockOwnsEncoderBufferBeforeAsyncSend(t *testing.T) {
	now := time.Unix(1, 0)
	c := newTXClock(1, 1, now)
	payload := []byte{1, 2, 3}
	p := c.packet(payload, false, now)
	payload[0] = 9
	if p.Payload[0] != 1 {
		t.Fatal("queued RTP aliases reused encoder storage")
	}
}
