package relayproto

import (
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"

	"golang.org/x/crypto/blake2b"
)

// A QUIC packet announces itself before anything is decrypted.
//
// The Initial packet is protected with keys derived from the connection ID and
// a published version salt, so its header, the version number, the SNI and the
// protocol name are readable by anyone on the path. Everything the previous
// step did to make the tunnel unremarkable stops at that packet: "there is
// HTTP/3 here" is exactly the kind of statement a network can act on.
//
// So the datagram is scrambled before it leaves and unscrambled on arrival,
// the way Hysteria's Salamander does it. What crosses the network is bytes
// with no structure to recognise. Two consequences follow, and the second is
// the more useful one:
//
//   - there is nothing left to fingerprint - no version, no ALPN, not even the
//     fact that this is QUIC;
//   - a prober that does not know the password sends noise, which unscrambles
//     into noise, which QUIC discards without a word. The port answers nobody.
//
// This is not encryption and is not asked to be. QUIC's own encryption is what
// keeps the contents secret; this only removes the shape. Anyone with the
// password can undo it, and anyone with the password is a user.
const (
	// salamanderSaltLen is the per-packet salt, so identical datagrams do not
	// scramble to identical bytes.
	salamanderSaltLen = 8
	// salamanderKeyLen is the BLAKE2b output the keystream cycles through.
	salamanderKeyLen = 32
)

// Obfuscator scrambles and unscrambles datagrams under one shared key.
// Safe for concurrent use: the only mutable state is a pool of scratch keys.
type Obfuscator struct {
	psk  []byte
	keys sync.Pool
}

// NewObfuscator keys the scrambler from the credential, which is already a
// stretched secret, so both ends arrive at the same key from the password
// alone and neither has to be told anything extra.
func NewObfuscator(c Credential) *Obfuscator {
	sum := blake2b.Sum256([]byte(c))
	psk := make([]byte, len(sum))
	copy(psk, sum[:])
	return &Obfuscator{
		psk: psk,
		keys: sync.Pool{New: func() any {
			buffer := make([]byte, len(psk)+salamanderSaltLen)
			return &buffer
		}},
	}
}

// Overhead is what scrambling adds to every datagram.
func (o *Obfuscator) Overhead() int { return salamanderSaltLen }

// Obfuscate writes the scrambled form of in into out and returns its length.
// out must have room for len(in) + Overhead(); a shorter one yields zero,
// which the caller must treat as "do not send this".
func (o *Obfuscator) Obfuscate(in, out []byte) int {
	if len(out) < len(in)+salamanderSaltLen {
		return 0
	}
	salt := out[:salamanderSaltLen]
	if _, err := rand.Read(salt); err != nil {
		// crypto/rand does not fail on any platform this runs on; a caller
		// that somehow sees it gets a dropped packet rather than a
		// predictable one.
		return 0
	}
	key := o.keyFor(salt)
	for i := range in {
		out[salamanderSaltLen+i] = in[i] ^ key[i%salamanderKeyLen]
	}
	return len(in) + salamanderSaltLen
}

// Deobfuscate reverses it. A datagram too short to carry a salt yields zero -
// noise from somebody who does not have the password looks exactly like this,
// and so does a stray packet, and neither is worth an answer.
func (o *Obfuscator) Deobfuscate(in, out []byte) int {
	if len(in) <= salamanderSaltLen || len(out) < len(in)-salamanderSaltLen {
		return 0
	}
	key := o.keyFor(in[:salamanderSaltLen])
	body := in[salamanderSaltLen:]
	for i := range body {
		out[i] = body[i] ^ key[i%salamanderKeyLen]
	}
	return len(body)
}

// keyFor derives the keystream for one salt. The result is only read, so the
// scratch buffer it was mixed in can go straight back to the pool.
func (o *Obfuscator) keyFor(salt []byte) [salamanderKeyLen]byte {
	scratch := o.keys.Get().(*[]byte)
	buffer := (*scratch)[:0]
	buffer = append(buffer, o.psk...)
	buffer = append(buffer, salt...)
	key := blake2b.Sum256(buffer)
	*scratch = buffer[:0]
	o.keys.Put(scratch)
	return key
}

// ObfuscatedPacketConn wraps a UDP socket so every datagram is scrambled on
// the way out and unscrambled on the way in. It is what QUIC is given instead
// of the socket itself, on both sides.
type ObfuscatedPacketConn struct {
	net.PacketConn
	obfuscator *Obfuscator
	buffers    sync.Pool
}

// ObfuscatePacketConn wraps conn. Both ends must wrap, and with the same key,
// or neither hears the other.
func ObfuscatePacketConn(conn net.PacketConn, o *Obfuscator) *ObfuscatedPacketConn {
	return &ObfuscatedPacketConn{
		PacketConn: conn,
		obfuscator: o,
		buffers: sync.Pool{New: func() any {
			buffer := make([]byte, obfuscatedBufferBytes)
			return &buffer
		}},
	}
}

// obfuscatedBufferBytes covers any datagram a path can carry, with room for
// the salt on top.
const obfuscatedBufferBytes = 2048

// ReadFrom returns the next datagram that unscrambles. Anything that does not
// is dropped without a reply and without a log line: a public UDP port is
// offered scans, strays and noise all day, and answering any of them - even
// with an error - is an answer.
func (c *ObfuscatedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	scratch := c.buffers.Get().(*[]byte)
	defer c.buffers.Put(scratch)
	buffer := *scratch
	for {
		n, addr, err := c.PacketConn.ReadFrom(buffer)
		if err != nil {
			return 0, addr, err
		}
		if size := c.obfuscator.Deobfuscate(buffer[:n], p); size > 0 {
			return size, addr, nil
		}
	}
}

// WriteTo scrambles and sends. A datagram that does not fit the scratch buffer
// is refused rather than truncated: a half-written packet would be worse than
// a dropped one.
func (c *ObfuscatedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	scratch := c.buffers.Get().(*[]byte)
	defer c.buffers.Put(scratch)
	buffer := *scratch
	n := c.obfuscator.Obfuscate(p, buffer)
	if n == 0 {
		return 0, io.ErrShortBuffer
	}
	if _, err := c.PacketConn.WriteTo(buffer[:n], addr); err != nil {
		return 0, err
	}
	// The caller is told what it handed over, not what went on the wire: the
	// salt is ours, not part of its packet.
	return len(p), nil
}

// SetReadBuffer and SetWriteBuffer let QUIC size the socket, which it warns
// about loudly when it cannot. Only the buffers pass through: the wrapper hides
// the socket from everything else on purpose (see the note below).
func (c *ObfuscatedPacketConn) SetReadBuffer(bytes int) error {
	if conn, ok := c.PacketConn.(interface{ SetReadBuffer(int) error }); ok {
		return conn.SetReadBuffer(bytes)
	}
	return nil
}

func (c *ObfuscatedPacketConn) SetWriteBuffer(bytes int) error {
	if conn, ok := c.PacketConn.(interface{ SetWriteBuffer(int) error }); ok {
		return conn.SetWriteBuffer(bytes)
	}
	return nil
}

// SyscallConn is exposed for one reason: QUIC uses it to set the don't-fragment
// bit, which is what keeps a packet from being split on the way.
//
// DO NOT add ReadMsgUDP or WriteMsgUDP here, however tempting the batching and
// the ECN bits look. Together with the two methods above and this one they
// satisfy quic.OOBCapablePacketConn, and QUIC then reads and writes through
// THOSE instead of ReadFrom and WriteTo - which is to say, straight past the
// scrambling, in the clear, with nothing failing to show it. A test asserts
// that this type does not satisfy that interface, and it is there to stop
// exactly this.
func (c *ObfuscatedPacketConn) SyscallConn() (syscall.RawConn, error) {
	conn, ok := c.PacketConn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, errors.New("relayproto: underlying connection has no syscall handle")
	}
	return conn.SyscallConn()
}

// QUICPacketSize is the packet size QUIC must be held to when the datagram is
// scrambled.
//
// Scrambling adds a salt to every datagram, so a QUIC packet sized to the path
// exactly would leave as one that is eight bytes too long. Path MTU discovery
// has to be off for the same reason: it would probe up to the real limit and
// every packet after that would be over it. QUIC's floor is 1200 bytes and
// every path that carries QUIC at all carries that, so the cost is a slightly
// smaller packet, not a broken one.
const QUICPacketSize = 1200 - salamanderSaltLen
