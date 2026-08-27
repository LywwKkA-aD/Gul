package mumble

import (
	"context"
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
	// transportErr holds the first error the socket reported, in an errBox.
	transportErr atomic.Value
	// stalled latches the one-directional failure above.
	stalled atomic.Bool
	// writeTimeout is packetWriteTimeout; tests shorten it.
	writeTimeout time.Duration

	// The rest is the instrument panel (vitals.go). None of it changes what
	// the connection does; all of it exists because a session that dies
	// without an account of itself cannot be diagnosed from a journal.
	//
	// created dates the silence before the first byte ever arrives.
	created time.Time
	// sent and received are plaintext Mumble bytes.
	sent, received atomic.Int64
	// outbound and inbound tally packets by type. inbound is fed by framer,
	// which only the read loop touches.
	outbound, inbound tally
	framer            inboundFramer
	// writeStarted holds the Unix nanoseconds of the write in flight, or 0
	// when none is. It is the reading that names a block while it is still
	// happening, rather than six seconds later when the deadline fires.
	writeStarted atomic.Int64
	// slowestWrite is the longest completed write, in nanoseconds. Only the
	// writer goroutine stores it, under the same mutex every write holds.
	slowestWrite atomic.Int64
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
		created:      time.Now(),
	}
}

// Read records that the connection is still delivering. That is the half of
// the evidence a stalled write needs: writes that fail while reads keep
// arriving are a blocked direction, not a dead connection.
func (c *packetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.lastRead.Store(time.Now().UnixNano())
		c.received.Add(int64(n))
		c.framer.consume(p[:n], &c.inbound)
	}
	if err != nil {
		c.note(err)
	}
	return n, err
}

// note keeps the first error the transport saw.
//
// gumble reports a lost connection with an empty reason unless the server sent
// one, so a session that dies of a network fault reaches the log as bare
// "connection lost" - which is what a real user's diagnostics said, and it said
// nothing at all. The error is down here; keeping it costs one store and turns
// that line into a diagnosis. The first is kept rather than the last because
// everything after it is a consequence.
func (c *packetConn) note(err error) {
	if err != nil {
		c.transportErr.CompareAndSwap(nil, errBox{err})
	}
}

// TransportError is the first error the connection itself reported, or nil.
func (c *packetConn) TransportError() error {
	if box, ok := c.transportErr.Load().(errBox); ok {
		return box.err
	}
	return nil
}

// errBox makes an error storable in an atomic.Value, which refuses mixed
// concrete types.
type errBox struct{ err error }

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

// Vitals reads the instrument panel without disturbing the connection
// (vitals.go). It is safe to call from any goroutine while the session runs.
func (c *packetConn) Vitals() Vitals {
	v := Vitals{
		Sent:     c.sent.Load(),
		Received: c.received.Load(),
		Out:      c.outbound.String(),
		In:       c.inbound.String(),
		Silent:   c.SilentFor(c.created),
		Slowest:  time.Duration(c.slowestWrite.Load()),
		Stalled:  c.stalled.Load(),
		Err:      c.TransportError(),
	}
	if started := c.writeStarted.Load(); started != 0 {
		v.Blocked = time.Since(time.Unix(0, started))
	}
	return v
}

// writeWithDeadline bounds one write and diagnoses the stall.
//
// Failing the write is not enough on its own: the send path only logs a failed
// voice frame and carries on, and the ping goroutine ignores its error too, so
// the session would stay up until the server gave up on it. Closing the
// connection is what turns the stall into a drop the reconnect loop can act on
// and the user can be told about.
func (c *packetConn) writeWithDeadline(p []byte) (int, error) {
	if len(p) >= 2 {
		c.outbound.add(uint16(p[0])<<8 | uint16(p[1]))
	}
	// A connection that cannot carry a deadline still has to carry voice, so a
	// failure here is not a failure of the write. It does mean the stall below
	// cannot be diagnosed on this connection, which is why it is recorded.
	deadlined := c.SetWriteDeadline(time.Now().Add(c.writeTimeout)) == nil

	started := time.Now()
	c.writeStarted.Store(started.UnixNano())
	n, err := c.Conn.Write(p)
	c.writeStarted.Store(0)
	took := time.Since(started)
	c.sent.Add(int64(n))
	if int64(took) > c.slowestWrite.Load() {
		c.slowestWrite.Store(int64(took))
	}

	if !deadlined || !hitDeadline(err, took, c.writeTimeout) {
		return n, err
	}
	c.note(err)
	if last := c.lastRead.Load(); last != 0 && time.Since(time.Unix(0, last)) < uplinkReadWindow {
		c.stalled.Store(true)
		err = ErrUplinkStalled
	}
	_ = c.Close()
	return n, err
}

// hitDeadline reports whether a failed write ran into the deadline we set on
// it. The clock decides, not the words the transport chose.
//
// Asking the error was wrong twice, and the second time cost a user his
// evening. A raw socket says os.ErrDeadlineExceeded. A WebSocket says
// context.DeadlineExceeded - but only when the deadline passes between writes.
// When it passes during one, which is the whole point of a stall,
// coder/websocket cancels the connection's write context instead of the write
// (netconn.go), so the error is context.Canceled: not a timeout by any name,
// indistinguishable from an ordinary shutdown, and permanent - that context is
// never renewed, so every later write on that connection fails the same way.
// The user hears everybody and nobody hears the user.
//
// We set the deadline, so we know what it was. A write that failed after
// spending it is a write that hit it, whatever it is called.
func hitDeadline(err error, took, budget time.Duration) bool {
	if err == nil {
		return false
	}
	return timedOut(err) || (budget > 0 && took >= budget)
}

// timedOut reports whether a write hit its deadline, whichever transport is
// underneath.
//
// Asking only for os.ErrDeadlineExceeded was a real bug and a quiet one: that
// is what a raw socket returns, and the direct connection is the one road this
// never had to work on. A WebSocket returns context.DeadlineExceeded instead
// (coder/websocket netconn.go), and errors.Is does not bridge the two - so on
// the relay roads, which are the roads this whole diagnosis was written for,
// the stall was never recognised, the connection was never closed for it, and
// the user was told "connection lost" while their microphone went nowhere.
//
// Every transport is asked in its own words, and net.Error covers the ones that
// answer in neither.
func timedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
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
	c.note(err)
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
