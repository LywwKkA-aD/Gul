package relayproto

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

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
	// salamanderLengthLen carries the real payload length, so the rest of the
	// datagram can be anything at all.
	salamanderLengthLen = 2
	// salamanderHeaderLen is what scrambling costs before any padding.
	salamanderHeaderLen = salamanderSaltLen + salamanderLengthLen
)

// Scrambling hides what the traffic IS. It does nothing about what the traffic
// LOOKS LIKE, and voice has a very particular look: a hundred packets a second,
// all within a few bytes of each other, starting when somebody speaks and
// stopping when they stop. A classifier that cannot read a single byte can
// still recognise that, and act on it.
//
// So every datagram is padded to a size drawn at random, and the tunnel keeps
// talking when nobody is: real packets stop being identifiable by size, and the
// gaps between talk and silence stop being visible at all.
//
// None of it delays anything. Padding adds bytes to a packet already on its
// way out, chaff is extra packets rather than held ones - the rule throughout
// is that we may add, never wait. Bandwidth is what pays: about 250 kbit/s
// while speaking against 118 before, which on any ordinary connection is
// nothing and on a metered mobile one is a setting.
const (
	// paddingMax is how much a datagram may grow. Small packets - voice is
	// around 150 bytes - end up anywhere in a wide band, which is what removes
	// the size signature; a large one is padded by whatever still fits.
	paddingMax = 256
	// datagramMax bounds the padded datagram, so padding never pushes a packet
	// past what the path will carry.
	datagramMax = 1200
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

// Overhead is what scrambling adds to a datagram before any padding.
func (o *Obfuscator) Overhead() int { return salamanderHeaderLen }

// Obfuscate writes the scrambled, padded form of in into out and returns its
// length. out must have room for len(in) + Overhead(); a shorter one yields
// zero, which the caller must treat as "do not send this".
//
// An empty payload is chaff: a datagram that exists only to be seen, which the
// other end discards.
func (o *Obfuscator) Obfuscate(in, out []byte) int {
	body := salamanderLengthLen + len(in)
	if len(in) > 0xffff || len(out) < body+salamanderSaltLen {
		return 0
	}
	salt := out[:salamanderSaltLen]
	if _, err := rand.Read(salt); err != nil {
		// crypto/rand does not fail on any platform this runs on; a caller
		// that somehow sees it gets a dropped packet rather than a
		// predictable one.
		return 0
	}
	// The salt is already random and already public, so the padding length is
	// drawn from it rather than from a second call.
	padding := o.padding(body, binary.BigEndian.Uint16(salt[:2]))
	if room := len(out) - salamanderSaltLen - body; padding > room {
		padding = room
	}

	plain := out[salamanderSaltLen : salamanderSaltLen+body]
	binary.BigEndian.PutUint16(plain, uint16(len(in)))
	copy(plain[salamanderLengthLen:], in)
	key := o.keyFor(salt)
	for i := range plain {
		plain[i] ^= key[i%salamanderKeyLen]
	}

	// The padding is random rather than scrambled zeroes. Scrambling zeroes
	// would write the keystream itself, and the keystream repeats every 32
	// bytes - the tail of every packet would carry a visible period, which is
	// a worse signature than the one the padding is here to remove.
	if padding > 0 {
		tail := out[salamanderSaltLen+body : salamanderSaltLen+body+padding]
		if _, err := rand.Read(tail); err != nil {
			return salamanderSaltLen + body
		}
	}
	return salamanderSaltLen + body + padding
}

// Deobfuscate reverses it, dropping the padding and the chaff. A datagram too
// short to carry a header yields zero - noise from somebody without the
// password looks exactly like this, and so does a stray packet, and neither is
// worth an answer.
func (o *Obfuscator) Deobfuscate(in, out []byte) int {
	if len(in) < salamanderHeaderLen {
		return 0
	}
	key := o.keyFor(in[:salamanderSaltLen])
	body := in[salamanderSaltLen:]
	var header [salamanderLengthLen]byte
	for i := range header {
		header[i] = body[i] ^ key[i%salamanderKeyLen]
	}
	size := int(binary.BigEndian.Uint16(header[:]))
	if size == 0 || size > len(body)-salamanderLengthLen || len(out) < size {
		// Zero is chaff. Anything longer than the datagram is a packet from
		// somebody using another key, which unscrambles into nonsense.
		return 0
	}
	payload := body[salamanderLengthLen : salamanderLengthLen+size]
	for i := range payload {
		out[i] = payload[i] ^ key[(salamanderLengthLen+i)%salamanderKeyLen]
	}
	return size
}

// padding is how much to add to a datagram whose real content is body bytes.
// The result is spread over what is left, so a small packet varies widely and
// a full one is left alone rather than pushed past what the path carries.
func (o *Obfuscator) padding(body int, pick uint16) int {
	room := min(datagramMax-salamanderSaltLen-body, paddingMax)
	if room <= 0 {
		return 0
	}
	return int(pick) % (room + 1)
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

// QUICPacketSize is the packet size QUIC is held to when the datagram is
// scrambled, and it is the floor rather than a choice: quic-go clamps anything
// below 1200 up to it (protocol.MinInitialPacketSize), so a smaller number
// here would simply be ignored.
//
// The honest consequence: a full packet leaves as a 1210-byte datagram, ten
// bytes over what QUIC believes it sent, because the salt and the length ride
// on top and there is no way to ask QUIC for less. Every path that carries
// QUIC has room - the 1200 floor exists because that is the assumed safe
// minimum, and real paths are 1500 - but a path of exactly 1200 would drop it,
// and with path MTU discovery off there would be no probing to notice.
//
// Discovery stays off for the same reason it was off before: it would find the
// real limit and every packet after that would be over it by ten bytes.
// Padding never contributes here, because a full packet has no room left for
// any (see padding).
const QUICPacketSize = datagramMax

// Chaff keeps the tunnel talking when nobody is.
//
// Voice has a shape even through a scrambled tunnel: a hundred packets a
// second while somebody speaks, nothing at all while they do not. The talk
// spurts alone are recognisable, and so is the rate. Extra datagrams carrying
// nothing break both - the rate stops being a metronome, and silence stops
// looking like silence.
//
// Never by delaying anything: chaff is packets added alongside the real ones,
// which are sent the moment they exist, exactly as before.
const (
	// chaffMinGap and chaffMaxGap bound the wait between chaff datagrams. The
	// spread matters more than the rate: a fixed interval would be its own
	// metronome, on top of the one it is hiding.
	chaffMinGap = 20 * time.Millisecond
	chaffMaxGap = 90 * time.Millisecond
)

// SendChaff emits chaff to addr until ctx ends. One per tunnel, on the side
// that has a single peer to talk to.
func (c *ObfuscatedPacketConn) SendChaff(ctx context.Context, addr net.Addr) {
	timer := time.NewTimer(chaffGap())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// An empty payload is the chaff marker: the other end reads a length
		// of zero and drops the datagram without waking anything above it.
		if _, err := c.WriteTo(nil, addr); err != nil {
			return
		}
		timer.Reset(chaffGap())
	}
}

func chaffGap() time.Duration {
	var pick [2]byte
	if _, err := rand.Read(pick[:]); err != nil {
		return chaffMaxGap
	}
	// Scale the draw across the spread, do NOT take it modulo the spread: a
	// uint16 is at most 65535 nanoseconds, far below the 70ms spread, so a
	// modulo would be an identity and every gap would be chaffMinGap - a fixed
	// interval, the exact metronome this is meant to avoid.
	spread := chaffMaxGap - chaffMinGap
	offset := time.Duration(uint64(binary.BigEndian.Uint16(pick[:])) * uint64(spread) / (1 << 16))
	return chaffMinGap + offset
}
