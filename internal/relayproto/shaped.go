package relayproto

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// The WebSocket road carries a byte stream, and a byte stream has a shape.
//
// One write becomes one WebSocket message becomes one TLS record, so the
// length of every record the tunnel produces is visible from outside, and for
// voice those lengths follow the encoder, which follows the speech. Variable
// bitrate made that worse on purpose - a constant frame size was a fingerprint
// of its own - and it left the sizes carrying something about what was said.
// The QUIC road closed this by holding every datagram to one size; this is the
// same answer for the other road.
//
// The shape is a frame:
//
//	kind    1 byte
//	payload 2 bytes, big endian
//	padding 2 bytes, big endian
//	then the payload, then the padding
//
// and the whole frame is rounded up to a fixed grid. Every voice record lands
// in the first cell of that grid, so they all leave as the same number of
// bytes, and so does the chaff that fills the silences.
//
// The stream underneath has no message boundaries on the way back - a read may
// span two of them - which is why the lengths are in the frame rather than
// implied by the transport.
const (
	shapedHeaderLen = 5
	shapedKindData  = 0x00
	shapedKindChaff = 0x01

	// shapedBucket is the grid. It has to clear a voice record with room to
	// spare: Opus at this bitrate produces 34 to 99 bytes, the inner Mumble
	// and TLS layers add a few dozen more, and the frame header five. One cell
	// swallows all of it, which is the point - every voice frame is the same
	// size on the wire, not merely a padded one.
	shapedBucket = 256

	// shapedMaxPayload bounds a frame read from the wire. Nothing we send
	// reaches it any more - shapedCellPayload is the sending bound - but a
	// length field arriving from outside still needs one.
	shapedMaxPayload = 1 << 15

	// shapedCellPayload is the most one frame carries, chosen so that every
	// frame we send is exactly one cell.
	//
	// Rounding up to the grid was not enough, and the gap was measured rather
	// than reasoned about. A voice record is 34 to 99 bytes and disappears
	// into a cell, which is what the grid was designed for and what it does.
	// The inner TLS handshake is not voice: its records are 1300 to 1500
	// bytes, and rounding turned them into frames of 1536 and 1280 - the only
	// two sizes in an entire session that were not 256. An observer needed no
	// cleverness at all, just "the first frame is bigger than the rest".
	//
	// Splitting instead of rounding costs nothing: 1500 bytes become six cells
	// of 256, which is the 1536 bytes the single padded frame took anyway. The
	// session now has exactly one frame size from its first byte.
	shapedCellPayload = shapedBucket - shapedHeaderLen
)

// ShapedCellBytes is the size of every frame this contract puts on the wire.
// Exported so a test can measure the wire against it rather than against a
// number written down twice.
const ShapedCellBytes = shapedBucket

// ShapedConn is a net.Conn whose writes leave as fixed-size frames, and which
// keeps talking while nothing is being said.
//
// It is symmetric: both ends wrap their side with it. Shaping only the client
// would leave the other half of the conversation a metronome, and an observer
// sitting next to the user sees both halves.
type ShapedConn struct {
	net.Conn
	reader *bufio.Reader

	// pending is what is left of the frame currently being handed upward. It
	// points into in, which is only refilled once pending is empty.
	pending []byte
	in      []byte

	mu  sync.Mutex
	out []byte
}

// MessageConn is a stream whose every Write leaves as exactly one message.
//
// The marker method is never called. It exists so that the guarantee Shape
// depends on has to be stated by whoever knows it holds, instead of being
// assumed - handing Shape a stream without message boundaries compiles fine
// otherwise, the frames still parse (the header carries its own lengths), and
// the only thing that quietly stops happening is the privacy.
type MessageConn interface {
	net.Conn
	// MessageBoundaries asserts that one Write is one message.
	MessageBoundaries()
}

// AsMessageConn attests that a connection delivers one message per Write.
//
// Exactly one kind of connection in this project qualifies: the one
// websocket.NetConn returns, whose documentation promises it. A QUIC stream
// does not - quic-go repacketises, and cells of 256 bytes have no say in what
// a datagram ends up carrying, which is why the QUIC road covers sizes with
// the obfuscator instead and reports a different contract for it.
//
// Grep for this name to find every place the claim is made.
func AsMessageConn(conn net.Conn) MessageConn { return messageConn{conn} }

type messageConn struct{ net.Conn }

func (messageConn) MessageBoundaries() {}

// Shape wraps a stream whose every Write becomes exactly one message. The
// guarantee is load-bearing: a frame split across two messages would be padded
// to the grid and still arrive as two different lengths.
func Shape(conn MessageConn) *ShapedConn {
	return &ShapedConn{
		Conn:   conn,
		reader: bufio.NewReaderSize(conn, 8*shapedBucket),
	}
}

// Write sends p as one or more frames, every one of them exactly one cell.
// Nothing is ever held back waiting for more: the rule on both roads is that
// shaping may add, never delay.
func (c *ShapedConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	written := 0
	for {
		chunk := min(len(p), shapedCellPayload)
		c.mu.Lock()
		err := c.writeFrame(shapedKindData, p[:chunk])
		c.mu.Unlock()
		if err != nil {
			return written, err
		}
		written += chunk
		p = p[chunk:]
		if len(p) == 0 {
			return written, nil
		}
	}
}

// writeFrame builds one frame and hands it over in a single Write, which is
// what makes it one message. The caller holds c.mu.
func (c *ShapedConn) writeFrame(kind byte, payload []byte) error {
	body := shapedHeaderLen + len(payload)
	padding := (shapedBucket - body%shapedBucket) % shapedBucket
	total := body + padding
	if cap(c.out) < total {
		c.out = make([]byte, total)
	}
	frame := c.out[:total]

	frame[0] = kind
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	binary.BigEndian.PutUint16(frame[3:5], uint16(padding))
	copy(frame[shapedHeaderLen:], payload)
	// Zeroes, not random bytes. The frame travels inside the outer TLS session,
	// so the padding is ciphertext by the time anyone sees it and its contents
	// carry nothing. The QUIC road cannot do this - its padding rides outside
	// the encryption, where zeroes would expose the keystream - and the
	// difference is worth stating rather than copying the expensive answer to a
	// problem this side does not have. Clearing matters only because the buffer
	// is reused, and stale bytes of an earlier frame have no business going out
	// again.
	clear(frame[body:])

	_, err := c.Conn.Write(frame)
	return err
}

// Read hands back payload bytes, dropping padding and chaff on the way.
func (c *ShapedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(c.pending) == 0 {
		if err := c.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *ShapedConn) readFrame() error {
	var header [shapedHeaderLen]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		return err
	}
	payload := int(binary.BigEndian.Uint16(header[1:3]))
	padding := int(binary.BigEndian.Uint16(header[3:5]))
	if payload > shapedMaxPayload || padding > shapedMaxPayload {
		return errors.New("relayproto: shaped frame length out of range")
	}
	switch header[0] {
	case shapedKindData, shapedKindChaff:
	default:
		// Both ends agree on the frame format before a byte is sent - it is
		// what the negotiated subprotocol names - so an unknown kind is a
		// mismatch, not something to skip past quietly.
		return errors.New("relayproto: unknown shaped frame kind")
	}

	size := payload + padding
	if cap(c.in) < size {
		c.in = make([]byte, size)
	}
	body := c.in[:size]
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return err
	}
	if header[0] == shapedKindChaff {
		return nil
	}
	c.pending = body[:payload]
	return nil
}

// SendChaff keeps the tunnel talking when nobody is, until ctx ends. One per
// connection, on each side.
//
// A chaff frame is a whole cell of the grid and carries nothing, so on the wire
// it is the same number of bytes as a frame of speech. What it hides is the
// silence: without it, the gaps between talk spurts are as legible as the
// spurts.
//
// It does not make the rate uniform, and saying otherwise would be a lie: while
// somebody speaks the tunnel still carries more frames per second than while
// nobody does. Erasing that needs a frame on every tick whether or not there is
// anything to send, which is a different and much more expensive design. This
// removes the size signature outright and blurs the rate.
func (c *ShapedConn) SendChaff(ctx context.Context) {
	timer := time.NewTimer(chaffGap())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// TryLock, never Lock: chaff must never be the reason a real write
		// waits. A tick that finds the stream busy is a tick worth dropping -
		// the traffic it would have added is already going out.
		if c.mu.TryLock() {
			err := c.writeFrame(shapedKindChaff, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
		timer.Reset(chaffGap())
	}
}
