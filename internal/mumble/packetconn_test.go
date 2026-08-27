package mumble

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
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

// stallingConn is a socket that stops draining: writes block until the write
// deadline the caller set, then fail the way a real one does.
type stallingConn struct {
	messageConn
	mu       sync.Mutex
	deadline time.Time
	writes   int
}

func (c *stallingConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *stallingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	deadline := c.deadline
	c.mu.Unlock()
	if deadline.IsZero() {
		return len(p), nil
	}
	time.Sleep(time.Until(deadline))
	return 0, os.ErrDeadlineExceeded
}

func (c *stallingConn) closedNow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// The failure nobody could read off the screen: the socket stops draining
// while the server keeps talking. Voice writes hang, and because gumble puts
// every write behind one connection mutex they take the 5-second pings with
// them, so the server eventually drops us for not pinging - twenty seconds of
// looking connected while nobody can hear us.
func TestPacketConnDiagnosesAStalledUplink(t *testing.T) {
	t.Parallel()
	inner := &stallingConn{}
	conn := newPacketConn(inner)
	conn.writeTimeout = 40 * time.Millisecond
	// The server was talking to us moments ago: this is one direction, not a
	// dead connection.
	conn.lastRead.Store(time.Now().UnixNano())

	_, err := conn.Write(mumblePacket(1, []byte("voice")))

	if !errors.Is(err, ErrUplinkStalled) {
		t.Fatalf("write error = %v, want ErrUplinkStalled", err)
	}
	if !conn.StalledUplink() {
		t.Fatal("the stall was not latched for the disconnect reason")
	}
	// Failing the write is not enough: the send path only logs a failed voice
	// frame and carries on. Closing is what turns the stall into a drop.
	if !inner.closedNow() {
		t.Fatal("the connection was left open, so the session would linger")
	}
}

// A connection that stopped delivering as well is simply gone, and calling
// that a blocked uplink would send the user hunting for a censor when their
// wifi dropped.
func TestPacketConnBlamesTheUplinkOnlyWhileTheDownlinkWorks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		lastRead time.Time
	}{
		{name: "nothing ever arrived"},
		{name: "the connection went quiet long ago", lastRead: time.Now().Add(-2 * uplinkReadWindow)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &stallingConn{}
			conn := newPacketConn(inner)
			conn.writeTimeout = 40 * time.Millisecond
			if !tc.lastRead.IsZero() {
				conn.lastRead.Store(tc.lastRead.UnixNano())
			}

			_, err := conn.Write(mumblePacket(1, []byte("voice")))

			if errors.Is(err, ErrUplinkStalled) {
				t.Fatal("an ordinary drop was reported as a blocked uplink")
			}
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("write error = %v, want the deadline", err)
			}
			if conn.StalledUplink() {
				t.Fatal("the stall was latched for an ordinary drop")
			}
		})
	}
}

// Reads are the other half of the evidence, and they arrive on their own
// goroutine while the writer is blocked.
func TestPacketConnRecordsThatTheServerIsStillTalking(t *testing.T) {
	t.Parallel()
	conn := newPacketConn(&readOnceConn{payload: []byte("hello")})
	if conn.lastRead.Load() != 0 {
		t.Fatal("a fresh connection claims to have read something")
	}

	buf := make([]byte, 8)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if conn.lastRead.Load() == 0 {
		t.Fatal("a delivered read was not recorded")
	}
}

// readOnceConn delivers one payload and then reports the connection is over.
type readOnceConn struct {
	messageConn
	payload []byte
	done    bool
}

func (c *readOnceConn) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	c.done = true
	return copy(p, c.payload), nil
}

// A sync that is slow is not a sync that is dead, and telling them apart by the
// clock is what broke a real user's connection.
//
// Their server log showed the login succeeding on every attempt while the
// client gave up at exactly ten seconds, every time, on both roads. The room
// was simply sending more than their link could carry in ten seconds - the
// channel tree, and everyone else's voice, which the server starts relaying the
// moment it has authenticated somebody. Nothing was blocking anything.
func TestSilentForMeasuresTheGapNotTheTotal(t *testing.T) {
	t.Parallel()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	packets := newPacketConn(client)
	started := time.Now().Add(-time.Hour) // long since started, by the clock

	// Nothing has arrived yet, so the wait is measured from the start and this
	// connection has been silent for an hour.
	if got := packets.SilentFor(started); got < time.Hour {
		t.Fatalf("silent for %s before the first byte, want the whole wait", got)
	}

	go func() { _, _ = server.Write([]byte{0x01}) }()
	buf := make([]byte, 1)
	if _, err := packets.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	// One byte, an hour late, and the connection is no longer silent at all:
	// what counts is the gap since the last delivery, not how long the whole
	// thing has taken.
	if got := packets.SilentFor(started); got > time.Second {
		t.Fatalf("silent for %s just after a read, want almost nothing", got)
	}
}

// The watchdog keeps a slow sync alive and ends a dead one. This is the rule
// the dial timeout used to get wrong, so it is checked on both sides.
func TestSyncingContextEndsOnSilenceNotOnSlowness(t *testing.T) {
	t.Parallel()
	t.Run("a trickle keeps it alive", func(t *testing.T) {
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		packets := newPacketConn(client)
		// The wait started long ago - longer than the whole budget - and data
		// is still trickling in. By the rule that broke the user's connection
		// this is over; by the rule that replaced it, it is merely slow. The
		// start is dated rather than waited out so the test costs a second
		// instead of a minute.
		ctx, cancel := syncingContextSince(packets, time.Now().Add(-4*syncSilence))
		t.Cleanup(cancel)

		buf := make([]byte, 1)
		for range 8 {
			go func() { _, _ = server.Write([]byte{0x01}) }()
			if _, err := packets.Read(buf); err != nil {
				t.Fatalf("read: %v", err)
			}
			time.Sleep(syncSilence / 20)
			if ctx.Err() != nil {
				t.Fatal("the sync was given up on while it was still arriving")
			}
		}
	})

	t.Run("silence ends it", func(t *testing.T) {
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		packets := newPacketConn(client)
		// Nothing ever arrives. Starting the clock in the past is what makes
		// this a test rather than a ten-second wait.
		ctx, cancel := syncingContextSince(packets, time.Now().Add(-2*syncSilence))
		t.Cleanup(cancel)

		select {
		case <-ctx.Done():
		case <-time.After(2 * syncSilence / 3):
			t.Fatal("a connection that delivered nothing at all was never given up on")
		}
	})
}

// The stalled-uplink diagnosis has to recognise a deadline whatever transport
// reports it, and it did not.
//
// It asked only for os.ErrDeadlineExceeded, which is what a raw socket returns
// - the direct connection, the one road this was never needed on. A WebSocket
// returns context.DeadlineExceeded instead, errors.Is does not bridge the two,
// and so on both relay roads the stall went unrecognised: the connection stayed
// open, nothing was diagnosed, and the user read "connection lost" while
// nothing of theirs was reaching the server. A user hit exactly this today.
func TestADeadlineIsRecognisedWhateverTransportReportsIt(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a raw socket":  fmt.Errorf("write tcp: %w", os.ErrDeadlineExceeded),
		"a websocket":   fmt.Errorf("failed to write: %w", context.DeadlineExceeded),
		"a net.Error":   timeoutError{},
		"wrapped twice": fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.DeadlineExceeded)),
	} {
		if !timedOut(err) {
			t.Errorf("%s: a write deadline was not recognised: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"no error at all":   nil,
		"an ordinary fault": errors.New("connection reset by peer"),
		"a cancelled write": context.Canceled,
	} {
		if timedOut(err) {
			t.Errorf("%s: taken for a write deadline: %v", name, err)
		}
	}
}

// timeoutError is a transport that answers in neither sentinel, the way
// quic-go and others may.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
