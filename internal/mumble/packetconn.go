package mumble

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
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
}

// newPacketConn wraps conn. The returned connection is only safe for the
// packet-at-a-time writer gumble provides: writes must arrive in packet order
// (gumble serializes them under Conn.Lock), a caller interleaving two packets
// would produce two corrupt ones.
func newPacketConn(conn net.Conn) net.Conn {
	return &packetConn{Conn: conn, buf: make([]byte, 0, packetBufferBytes)}
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
		if _, err := c.Conn.Write(p[:size]); err != nil {
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
		if _, err := c.Conn.Write(c.buf[:size]); err != nil {
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
