package mumble

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// ErrUplinkStalled is the drop nobody could diagnose: the session keeps
// receiving - the user hears everyone - while nothing of ours reaches the
// server any more.
//
// Seen live on 2026-08-26. A user connected fifty times in a row, authenticated
// every time, and the relay counted the same ~3.3 KB from them in every single
// session whatever its length: the inner TLS handshake and the login, then
// nothing. Not even the 5-second pings, because gumble serializes every write
// behind one connection mutex, so a voice frame stuck in the socket takes the
// ping goroutine down with it. The server then dropped them on its own ping
// deadline (murmur timeout=30) roughly every twenty seconds, and the window
// said only "connection lost".
var ErrUplinkStalled = errors.New("outgoing traffic is not getting through")

const (
	// packetWriteTimeout bounds one write to the server. A healthy link takes
	// microseconds; the only thing this can hit is a socket that stopped
	// draining. Six seconds is well past any ordinary retransmit and well
	// inside the server's own ping deadline, so we diagnose the stall instead
	// of waiting to be dropped for it.
	packetWriteTimeout = 6 * time.Second
	// uplinkReadWindow is how recently the connection must have delivered
	// something for a stalled write to mean "one direction only". Beyond it
	// the whole connection is simply gone, which is an ordinary drop.
	uplinkReadWindow = 15 * time.Second
)

const (
	// packetHeaderBytes is the Mumble TCP header: 2-byte type, 4-byte
	// big-endian payload length.
	packetHeaderBytes = 6
	// packetBufferBytes holds a voice packet (header plus one 10 ms Opus
	// frame) and every ordinary control packet without growing.
	packetBufferBytes = 4096
)

// packetConn makes one Mumble packet cost exactly one WebSocket message.
//
// It wraps the inner TLS connection rather than the WebSocket stream, because
// the framing that matters is produced above TLS: gumble writes a packet in
// two or three Write calls (header, voice header, payload), each becoming its
// own TLS record and therefore its own WebSocket message. At 100 packets per
// second that is 100-200 extra frames per second per direction, each carrying
// WebSocket and TLS overhead of its own. Coalescing has to happen before the
// TLS layer, so this is the connection handed to gumble.
//
// Latency is unchanged: bytes are held only while a packet is incomplete, and
// the packet is written the moment its last byte arrives.
type packetConn struct {
	net.Conn
	mu  sync.Mutex
	buf []byte
	// err latches a framing failure: once the stream is out of sync there is
	// no way back, and the connection is closed.
	err error
	// lastRead is when the connection last delivered anything, in Unix
	// nanoseconds. Reads and writes run on different goroutines, so it is
	// read without the mutex the writer holds.
	lastRead atomic.Int64
	// stalled latches the one-directional failure above.
	stalled atomic.Bool
	// writeTimeout is packetWriteTimeout; tests shorten it.
	writeTimeout time.Duration
}

// newPacketConn wraps conn. The returned connection is only safe for the
// packet-at-a-time writer gumble provides: writes must arrive in packet order
// (gumble serializes them under Conn.Lock), a caller interleaving two packets
// would produce two corrupt ones.
func newPacketConn(conn net.Conn) *packetConn {
	return &packetConn{
		Conn:         conn,
		buf:          make([]byte, 0, packetBufferBytes),
		writeTimeout: packetWriteTimeout,
	}
}

// Read records that the connection is still delivering. That is the half of
// the evidence a stalled write needs: writes that fail while reads keep
// arriving are a blocked direction, not a dead connection.
func (c *packetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.lastRead.Store(time.Now().UnixNano())
	}
	return n, err
}

// StalledUplink reports whether this connection died because our own traffic
// stopped getting through while the server's kept arriving.
func (c *packetConn) StalledUplink() bool { return c.stalled.Load() }

// SilentFor is how long the connection has delivered nothing. Before the first
// byte it is measured from whenever the caller started watching, which is what
// the sync watchdog wants: a server that never answers at all has to time out
// too.
func (c *packetConn) SilentFor(since time.Time) time.Duration {
	if last := c.lastRead.Load(); last != 0 {
		return time.Since(time.Unix(0, last))
	}
	return time.Since(since)
}

// writeWithDeadline bounds one write and diagnoses the stall.
//
// Failing the write is not enough on its own: the send path only logs a failed
// voice frame and carries on, and the ping goroutine ignores its error too, so
// the session would stay up until the server gave up on it. Closing the
// connection is what turns the stall into a drop the reconnect loop can act on
// and the user can be told about.
func (c *packetConn) writeWithDeadline(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		// A connection that cannot carry a deadline still has to carry voice.
		return c.Conn.Write(p)
	}
	n, err := c.Conn.Write(p)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		return n, err
	}
	if last := c.lastRead.Load(); last != 0 && time.Since(time.Unix(0, last)) < uplinkReadWindow {
		c.stalled.Store(true)
		err = ErrUplinkStalled
	}
	_ = c.Close()
	return n, err
}

func (c *packetConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return 0, c.err
	}
	total := len(p)

	// Whole packets that arrive in one Write go out without a copy. This is
	// the path a Write spanning several packets takes after the first.
	for len(c.buf) == 0 && len(p) > 0 {
		size, complete, err := packetBounds(p)
		if err != nil {
			return total - len(p), c.fail(err)
		}
		if !complete {
			break
		}
		if _, err := c.writeWithDeadline(p[:size]); err != nil {
			return total - len(p), err
		}
		p = p[size:]
	}
	if len(p) == 0 {
		return total, nil
	}

	// A partial packet is buffered until its last byte arrives, then flushed
	// as a whole - including any packets that followed it in the same Write.
	c.buf = append(c.buf, p...)
	for {
		size, complete, err := packetBounds(c.buf)
		if err != nil {
			return total, c.fail(err)
		}
		if !complete {
			return total, nil
		}
		if _, err := c.writeWithDeadline(c.buf[:size]); err != nil {
			return total, err
		}
		// Keep the tail, keep the array: the steady state never allocates.
		c.buf = c.buf[:copy(c.buf, c.buf[size:])]
	}
}

// fail latches err and drops the connection: a stream whose framing cannot be
// trusted must not keep carrying packets.
func (c *packetConn) fail(err error) error {
	c.err = err
	_ = c.Close()
	return err
}

// packetBounds reports the total size of the packet starting at b and whether
// b already holds all of it. A packet that cannot fit in one WebSocket message
// is an error: the relay applies the same bound and would drop the connection.
func packetBounds(b []byte) (size int, complete bool, err error) {
	if len(b) < packetHeaderBytes {
		return 0, false, nil
	}
	length := binary.BigEndian.Uint32(b[packetHeaderBytes-4 : packetHeaderBytes])
	if length > relayproto.MaxMessageBytes-packetHeaderBytes {
		return 0, false, fmt.Errorf(
			"mumble packet of %d bytes exceeds the %d byte relay message limit",
			uint64(length)+packetHeaderBytes, relayproto.MaxMessageBytes)
	}
	size = packetHeaderBytes + int(length)
	return size, len(b) >= size, nil
}
