package livekit

import (
	"sort"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/session"
	"github.com/pion/rtp"
)

const (
	reorderWait     = 40 * time.Millisecond
	phraseSilence   = 200 * time.Millisecond
	reorderCapacity = 64
)

type queuedRTP struct {
	packet   *rtp.Packet
	arrived  time.Time
	sequence int64
}
type rtpQueue struct {
	session           uint32
	key               string
	pending           map[int64]queuedRTP
	initialized       bool
	expected          int64
	highest           int64
	baseTimestamp     uint32
	lastTimestamp     uint32
	extendedTimestamp int64
	lastEnd           int64
	lastDelivered     int64
	lastArrival       time.Time
	ended             bool
}

func newRTPQueue(id uint32, key string) *rtpQueue {
	return &rtpQueue{session: id, key: key, pending: make(map[int64]queuedRTP)}
}

func (q *rtpQueue) push(p *rtp.Packet, now time.Time) []session.VoicePacket {
	if opusSamples(p.Payload) == 0 {
		return nil
	}
	seq := int64(p.SequenceNumber)
	if !q.initialized {
		q.initialized = true
		q.expected = seq
		q.highest = seq
		q.lastDelivered = seq - 1
		q.baseTimestamp = p.Timestamp
		q.lastTimestamp = p.Timestamp
	} else {
		seq = q.highest + int64(int16(p.SequenceNumber-uint16(q.highest)))
	}
	if seq < q.expected {
		return nil
	}
	if _, exists := q.pending[seq]; exists {
		return nil
	}
	if seq > q.highest {
		q.highest = seq
	}
	if len(q.pending) >= reorderCapacity {
		return q.flush(now.Add(reorderWait))
	}
	copied := *p
	copied.Payload = append([]byte(nil), p.Payload...)
	q.pending[seq] = queuedRTP{packet: &copied, arrived: now, sequence: seq}
	q.lastArrival = now
	return q.flush(now)
}

func (q *rtpQueue) flush(now time.Time) []session.VoicePacket {
	var out []session.VoicePacket
	for len(q.pending) > 0 {
		item, ok := q.pending[q.expected]
		if !ok {
			keys := make([]int64, 0, len(q.pending))
			for seq := range q.pending {
				keys = append(keys, seq)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
			item = q.pending[keys[0]]
			if now.Sub(item.arrived) < reorderWait {
				break
			}
		}
		delete(q.pending, item.sequence)
		out = append(out, q.deliver(item)...)
		q.expected = item.sequence + 1
	}
	if len(q.pending) == 0 && q.initialized && !q.ended && now.Sub(q.lastArrival) >= phraseSilence {
		out = append(out, q.finish())
	}
	return out
}

func (q *rtpQueue) deliver(item queuedRTP) []session.VoicePacket {
	p := item.packet
	delta := int64(int32(p.Timestamp - q.lastTimestamp))
	if delta < 0 {
		return nil
	}
	q.extendedTimestamp += delta
	q.lastTimestamp = p.Timestamp
	sequence := q.extendedTimestamp / 480
	var out []session.VoicePacket
	gap := sequence - q.lastEnd
	if !q.ended && q.lastEnd > 0 && gap > 0 {
		if item.sequence > q.lastDelivered+1 && gap <= 12 && !p.Marker {
			out = append(out, session.VoicePacket{Session: q.session, Key: q.key, Sequence: q.lastEnd, LostFrames: int(gap)})
		} else {
			out = append(out, q.finish())
		}
	}
	out = append(out, session.VoicePacket{Session: q.session, Key: q.key, Sequence: sequence, Opus: p.Payload})
	q.lastEnd = sequence + int64(opusSamples(p.Payload)/480)
	q.lastDelivered = item.sequence
	q.ended = false
	return out
}

func (q *rtpQueue) finish() session.VoicePacket {
	q.ended = true
	return session.VoicePacket{Session: q.session, Key: q.key, Sequence: q.lastEnd, Final: true}
}

// The native engine consumes 10ms units and decodes at most 60ms per packet.
// Reject unsupported durations before they can disturb its decoder timeline.
func opusSamples(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	config := payload[0] >> 3
	frames := 1
	switch payload[0] & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(payload) < 2 {
			return 0
		}
		frames = int(payload[1] & 63)
	}
	var samples int
	switch {
	case config >= 16:
		samples = 120 << uint(config&3)
	case config >= 12:
		samples = 480 << uint(config&1)
	default:
		samples = 480 << uint(config&3)
		if config&3 == 3 {
			samples = 2880
		}
	}
	samples *= frames
	if samples < 480 || samples > 2880 || samples%480 != 0 {
		return 0
	}
	return samples
}

type txClock struct {
	sequence  uint16
	base      uint32
	at        time.Time
	timestamp uint32
	fresh     bool
}

func newTXClock(seq uint16, ts uint32, at time.Time) txClock {
	return txClock{sequence: seq, base: ts, at: at, timestamp: ts, fresh: true}
}
func (c *txClock) packet(payload []byte, final bool, now time.Time) *rtp.Packet {
	if c.fresh {
		candidate := c.base + uint32(max(0, now.Sub(c.at)/(10*time.Millisecond)))*480
		if int32(candidate-c.timestamp) > 0 {
			c.timestamp = candidate
		}
	}
	p := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: c.sequence, Timestamp: c.timestamp, Marker: c.fresh}, Payload: append([]byte(nil), payload...)}
	c.sequence++
	c.timestamp += 480
	c.fresh = final
	return p
}
