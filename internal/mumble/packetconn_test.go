package mumble

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// messageConn stands in for the WebSocket stream: every Write is one message,
// which is exactly the contract of websocket.NetConn.
type messageConn struct {
	messages [][]byte
	writeErr error
	closed   bool
}

func (c *messageConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	c.messages = append(c.messages, bytes.Clone(p))
	return len(p), nil
}

func (c *messageConn) Read([]byte) (int, error)         { return 0, errors.New("unused") }
func (c *messageConn) Close() error                     { c.closed = true; return nil }
func (c *messageConn) LocalAddr() net.Addr              { return nil }
func (c *messageConn) RemoteAddr() net.Addr             { return nil }
func (c *messageConn) SetDeadline(time.Time) error      { return nil }
func (c *messageConn) SetReadDeadline(time.Time) error  { return nil }
func (c *messageConn) SetWriteDeadline(time.Time) error { return nil }

// mumblePacket builds the wire form: 2-byte type, 4-byte big-endian length.
func mumblePacket(packetType uint16, payload []byte) []byte {
	packet := make([]byte, packetHeaderBytes+len(payload))
	binary.BigEndian.PutUint16(packet, packetType)
	binary.BigEndian.PutUint32(packet[2:], uint32(len(payload)))
	copy(packet[packetHeaderBytes:], payload)
	return packet
}

func TestPacketConnEmitsOneMessagePerPacket(t *testing.T) {
	// gumble writes the header first and the payload second; the wire must
	// still carry one message, not two.
	packet := mumblePacket(1, bytes.Repeat([]byte{0xa5}, 96))
	sink := &messageConn{}
	conn := newPacketConn(sink)

	if n, err := conn.Write(packet[:packetHeaderBytes]); err != nil || n != packetHeaderBytes {
		t.Fatalf("write header = (%d, %v)", n, err)
	}
	if len(sink.messages) != 0 {
		t.Fatalf("incomplete packet was sent as %d message(s)", len(sink.messages))
	}
	if n, err := conn.Write(packet[packetHeaderBytes:]); err != nil || n != len(packet)-packetHeaderBytes {
		t.Fatalf("write payload = (%d, %v)", n, err)
	}
	if len(sink.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(sink.messages))
	}
	if !bytes.Equal(sink.messages[0], packet) {
		t.Fatal("the emitted message is not the packet")
	}
}

func TestPacketConnSplitsAWriteThatSpansPackets(t *testing.T) {
	first := mumblePacket(3, []byte("ping"))
	second := mumblePacket(11, []byte("text message"))
	sink := &messageConn{}
	conn := newPacketConn(sink)

	both := append(bytes.Clone(first), second...)
	if n, err := conn.Write(both); err != nil || n != len(both) {
		t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(both))
	}

	if len(sink.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(sink.messages))
	}
	if !bytes.Equal(sink.messages[0], first) || !bytes.Equal(sink.messages[1], second) {
		t.Fatal("packets were not split on their own boundary")
	}
}

func TestPacketConnReassemblesAPacketSplitAcrossThreeWrites(t *testing.T) {
	packet := mumblePacket(9, []byte("user state payload"))
	sink := &messageConn{}
	conn := newPacketConn(sink)

	// Header, then two payload halves - and a trailing packet in the last
	// write, so the tail is flushed too rather than held.
	tail := mumblePacket(3, nil)
	chunks := [][]byte{
		packet[:4],
		packet[4:10],
		append(bytes.Clone(packet[10:]), tail...),
	}
	for i, chunk := range chunks {
		if _, err := conn.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if i < len(chunks)-1 && len(sink.messages) != 0 {
			t.Fatalf("write %d emitted %d message(s) for an incomplete packet", i, len(sink.messages))
		}
	}

	if len(sink.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(sink.messages))
	}
	if !bytes.Equal(sink.messages[0], packet) || !bytes.Equal(sink.messages[1], tail) {
		t.Fatal("reassembled messages do not match the packets")
	}
}

func TestPacketConnRejectsAPacketOverTheMessageLimit(t *testing.T) {
	oversize := make([]byte, packetHeaderBytes)
	binary.BigEndian.PutUint16(oversize, 1)
	binary.BigEndian.PutUint32(oversize[2:], relayproto.MaxMessageBytes)
	sink := &messageConn{}
	conn := newPacketConn(sink)

	_, err := conn.Write(oversize)
	if err == nil {
		t.Fatal("a packet the relay would refuse was accepted")
	}
	if !sink.closed {
		t.Fatal("a stream that cannot be framed must be closed")
	}
	if _, err := conn.Write(mumblePacket(3, nil)); err == nil {
		t.Fatal("writes continued after a framing failure")
	}
	if len(sink.messages) != 0 {
		t.Fatalf("messages = %d, want none", len(sink.messages))
	}
}

func TestPacketConnKeepsTheLargestAllowedPacket(t *testing.T) {
	payload := bytes.Repeat([]byte{7}, relayproto.MaxMessageBytes-packetHeaderBytes)
	packet := mumblePacket(1, payload)
	sink := &messageConn{}
	conn := newPacketConn(sink)

	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("write largest allowed packet: %v", err)
	}
	if len(sink.messages) != 1 || len(sink.messages[0]) != relayproto.MaxMessageBytes {
		t.Fatalf("messages = %d, first = %d bytes", len(sink.messages), len(sink.messages[0]))
	}
}

// BenchmarkPacketConnVoiceFrame is the steady state: one 10 ms Opus packet
// written the way gumble writes it. Allocating per frame here would allocate
// 100 times a second for the lifetime of a call.
func BenchmarkPacketConnVoiceFrame(b *testing.B) {
	packet := mumblePacket(1, bytes.Repeat([]byte{0x5c}, 100))
	sink := &discardConn{}
	conn := newPacketConn(sink)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := conn.Write(packet[:packetHeaderBytes]); err != nil {
			b.Fatal(err)
		}
		if _, err := conn.Write(packet[packetHeaderBytes:]); err != nil {
			b.Fatal(err)
		}
	}
}

type discardConn struct{ messageConn }

func (c *discardConn) Write(p []byte) (int, error) { return len(p), nil }

func TestPacketConnVoiceFrameDoesNotAllocate(t *testing.T) {
	packet := mumblePacket(1, bytes.Repeat([]byte{0x5c}, 100))
	conn := newPacketConn(&discardConn{})
	// Warm the buffer: the first packet may grow it.
	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("warm-up write: %v", err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = conn.Write(packet[:packetHeaderBytes])
		_, _ = conn.Write(packet[packetHeaderBytes:])
	})
	if allocs != 0 {
		t.Fatalf("allocations per voice frame = %.1f, want 0", allocs)
	}
}
