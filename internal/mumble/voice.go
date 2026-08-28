package mumble

import (
	"errors"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
)

const (
	// voiceCodecOpus is the Mumble codec id for Opus. gumble only advertises
	// Opus support in the Authenticate packet when a codec sits under this id.
	voiceCodecOpus = 4

	// voiceRXBuffer holds incoming frames between gumble's read loop and the
	// RX pipeline: 256 frames is 2.56s on the 10ms grid, enough to ride out a
	// TCP burst and small enough that a stalled consumer loses audio instead of
	// growing an unbounded backlog.
	voiceRXBuffer = 256

	// voiceTXBuffer holds outgoing frames between the DSP goroutine and the
	// sender: 8 frames is 80ms, so a hiccup on the socket costs frames rather
	// than latency.
	voiceTXBuffer = 8

	// voiceSequenceWrap matches gumble's own wrap point for the wire sequence.
	voiceSequenceWrap = math.MaxInt32

	// voiceDrainGrace bounds how long a retiring stream pump keeps discarding
	// packets. It covers the gap between a session being torn down and its
	// socket actually closing, during which gumble's read loop can still hand
	// over a packet and park on it forever if nobody takes it.
	voiceDrainGrace = 250 * time.Millisecond
)

// ErrEmptyVoiceFrame is returned by SendVoice for a frame that carries neither
// audio nor a terminator: nothing to put on the wire.
var ErrEmptyVoiceFrame = errors.New("mumble: empty voice frame")

// ErrInvalidVoiceTarget is returned by SetVoiceTarget for an id outside the
// 5-bit wire field.
var ErrInvalidVoiceTarget = errors.New("mumble: voice target out of range")

// Voice targets on the wire: 0 routes to the current channel, 31 makes the
// server return our own audio (latency diagnostics). 1-30 are whisper targets
// and would need registration first, which this layer does not do yet.
const (
	VoiceTargetNormal   byte = 0
	VoiceTargetLoopback byte = 31
)

// VoicePacket is one incoming raw Opus packet (passthrough mode).
type VoicePacket struct {
	Session uint32 // sender session id
	// Key is what this client files the sender's audio settings under: the
	// certificate hash when there is one, something weaker when there is not
	// (peerkey.go). Two anonymous peers must not become one entry.
	Key      string
	Sequence int64 // wire frame number (10 ms units)
	Opus     []byte
	Final    bool
}

// VoiceStats are the voice transport counters. Every field is monotonic for
// the life of the Manager and survives reconnects.
type VoiceStats struct {
	RXDrops   uint64 // incoming frames dropped because the RX consumer fell behind
	TXDrops   uint64 // outgoing frames dropped because the sender fell behind
	TXOffline uint64 // outgoing frames dropped because no session was connected
	TXErrors  uint64 // WriteAudio failures
}

// VoicePackets returns the stream of incoming Opus frames. The channel is
// created with the Manager and stays valid across reconnects, so the audio
// pipeline can hold it for its whole life. The receiver must keep draining it:
// a stalled consumer loses the oldest frames (see VoiceDrops).
func (m *Manager) VoicePackets() <-chan VoicePacket {
	return m.voice.rx.out()
}

// VoiceDrops reports how many incoming frames were discarded because the
// receiver fell behind.
func (m *Manager) VoiceDrops() uint64 {
	return m.voice.rx.dropped()
}

// VoiceStats returns all voice transport counters at once.
func (m *Manager) VoiceStats() VoiceStats {
	return m.voice.stats()
}

// SendVoice queues one encoded Opus frame for the wire, with final marking the
// last frame of a transmission.
//
// It never blocks and never waits on the network: when the sender is behind,
// the oldest queued frame is dropped, because in a live conversation a stale
// frame is worse than a gap. With no session connected the frame is dropped
// silently and counted - going offline is not a caller error.
//
// The layer takes ownership of opus; the caller must not reuse or modify the
// slice afterwards.
func (m *Manager) SendVoice(opus []byte, final bool) error {
	if len(opus) == 0 && !final {
		return ErrEmptyVoiceFrame
	}
	m.voice.tx.push(voiceFrame{opus: opus, final: final})
	return nil
}

// SetVoiceTarget selects the wire target for outgoing frames, taking effect
// from the next frame sent. The default is VoiceTargetNormal.
func (m *Manager) SetVoiceTarget(target byte) error {
	if target > VoiceTargetLoopback {
		return ErrInvalidVoiceTarget
	}
	m.voice.target.Store(uint32(target))
	return nil
}

// --- stub codec ------------------------------------------------------------

// errStubCodec is returned by every stub codec operation. In passthrough mode
// gumble never encodes or decodes, so reaching this error means audio took a
// path it must not take.
var errStubCodec = errors.New("mumble: opus codec stub called in passthrough mode")

// voiceStubCodec satisfies gumble's codec registry without doing any work.
//
// gumble decides the Opus flag of the Authenticate packet by looking up codec
// id 4 in a process-wide registry: with nothing registered the server is told
// Opus: false and refuses to route our voice. Passthrough means raw Opus goes
// out through Conn.WriteAudio and comes back in AudioPacket.OpusData, so the
// registered codec only has to exist - and fails loudly if anything calls it.
type voiceStubCodec struct{}

func (voiceStubCodec) ID() int                         { return voiceCodecOpus }
func (voiceStubCodec) NewEncoder() gumble.AudioEncoder { return voiceStubEncoder{} }
func (voiceStubCodec) NewDecoder() gumble.AudioDecoder { return voiceStubDecoder{} }

type voiceStubEncoder struct{}

func (voiceStubEncoder) ID() int { return voiceCodecOpus }
func (voiceStubEncoder) Encode([]int16, int, int) ([]byte, error) {
	return nil, errStubCodec
}
func (voiceStubEncoder) Reset() {}

type voiceStubDecoder struct{}

func (voiceStubDecoder) ID() int { return voiceCodecOpus }
func (voiceStubDecoder) Decode([]byte, int) ([]int16, error) {
	return nil, errStubCodec
}
func (voiceStubDecoder) Reset() {}

// codecRegistrar installs the stub codec at most once. gumble's codec table is
// process-wide and unsynchronized against live clients, so registering twice
// would at best be pointless and at worst race a running read loop.
type codecRegistrar struct {
	once sync.Once
	// register is a seam for tests; nil means gumble's real registry.
	register func(int, gumble.AudioCodec)
}

func (r *codecRegistrar) ensure() {
	r.once.Do(func() {
		register := r.register
		if register == nil {
			register = gumble.RegisterAudioCodec
		}
		register(voiceCodecOpus, voiceStubCodec{})
	})
}

var voiceCodecRegistry codecRegistrar

// registerVoiceCodec installs the stub codec. It must run before the first
// Dial: gumble reads the registry while building the Authenticate packet.
func registerVoiceCodec() { voiceCodecRegistry.ensure() }

// --- drop-oldest buffer ----------------------------------------------------

// dropAttempts bounds the evict-and-retry loop in push. One eviction is enough
// for a single producer; the extra rounds only matter when several stream
// pumps push at once, and the bound keeps push O(1) instead of live-locking
// against them.
const dropAttempts = 4

// dropBuffer is a bounded FIFO that never blocks its producer: once full, the
// oldest queued item is discarded to make room and counted as a drop. Both
// voice directions need exactly this - the producer is either gumble's read
// loop (blocking it kills the connection on the 20s deadline) or the DSP
// goroutine (blocking it wrecks the 10ms grid).
type dropBuffer[T any] struct {
	ch    chan T
	drops atomic.Uint64
}

func newDropBuffer[T any](capacity int) *dropBuffer[T] {
	return &dropBuffer[T]{ch: make(chan T, capacity)}
}

// push adds v, evicting older items if that is what it takes. It never blocks.
func (b *dropBuffer[T]) push(v T) {
	for range dropAttempts {
		select {
		case b.ch <- v:
			return
		default:
		}
		select {
		case <-b.ch:
			b.drops.Add(1)
		default:
			// Someone else drained it between the two selects; retry the send.
		}
	}
	// Still no room after evicting: competing producers are refilling it faster
	// than the consumer drains. Drop the newest item instead of spinning.
	b.drops.Add(1)
}

// out is the consumer side. It is created once and never replaced, so callers
// may hold it for the life of the buffer.
func (b *dropBuffer[T]) out() <-chan T { return b.ch }

func (b *dropBuffer[T]) dropped() uint64 { return b.drops.Load() }

// --- receive ---------------------------------------------------------------

// voiceListener receives audio streams for one session and forwards packets
// into the manager-wide RX buffer.
//
// gumble hands each stream over an unbuffered channel written from the read
// loop, so OnAudioStream must never do work inline and the goroutine it starts
// must never stall: a blocked read loop stops sending pings and the server
// drops us on its 20s deadline.
type voiceListener struct {
	out *dropBuffer[VoicePacket]

	mu      sync.Mutex
	stopped bool
	done    chan struct{}
	wg      sync.WaitGroup
}

func newVoiceListener(out *dropBuffer[VoicePacket]) *voiceListener {
	return &voiceListener{out: out, done: make(chan struct{})}
}

// OnAudioStream starts one pump per talking user. It runs on the read loop and
// does nothing but read the sender identity and spawn.
//
// Session and the peer key are computed here, not inside the pump: this is the
// read loop itself, the one goroutine where gumble's User fields are stable.
// Both values are fixed for the lifetime of a stream (gumble keys streams by
// *User), so a single read is enough and the pump never dereferences a *User.
func (l *voiceListener) OnAudioStream(e *gumble.AudioStreamEvent) {
	if e == nil || e.C == nil {
		return
	}
	var (
		session uint32
		key     string
	)
	if e.User != nil {
		session, key = e.User.Session, peerKey(e.User)
	}

	l.mu.Lock()
	tracked := !l.stopped
	if tracked {
		l.wg.Add(1)
	}
	l.mu.Unlock()

	go l.pump(e.C, session, key, tracked)
}

// pump moves packets from one stream into the shared buffer until gumble closes
// the stream (it does so on the sender's disconnect) or the session ends.
func (l *voiceListener) pump(c <-chan *gumble.AudioPacket, session uint32, key string, tracked bool) {
	if tracked {
		defer l.wg.Done()
	}
	for {
		select {
		case p, ok := <-c:
			if !ok {
				return
			}
			if p == nil {
				continue
			}
			// OpusData is freshly allocated per packet by the fork, so it can be
			// handed on without copying. Nothing else from the packet is kept:
			// Client, Sender and Target are live pointers into gumble state.
			l.out.push(VoicePacket{
				Session:  session,
				Key:      key,
				Sequence: p.Sequence,
				Opus:     p.OpusData,
				Final:    p.Final,
			})
		case <-l.done:
			l.drain(c)
			return
		}
	}
}

// drain discards what the stream still produces so a read loop parked on the
// unbuffered handoff is released instead of stranded. gumble closes stream
// channels when the *sender* leaves but never when we do, which is why the pump
// needs an exit of its own at all - and why leaving through it must not strand
// a read loop that is only now noticing the socket is gone.
func (l *voiceListener) drain(c <-chan *gumble.AudioPacket) {
	timer := time.NewTimer(voiceDrainGrace)
	defer timer.Stop()

	for {
		select {
		case _, ok := <-c:
			if !ok {
				return
			}
		case <-timer.C:
			return
		}
	}
}

// stop ends every pump of this listener and waits for them. Pumps never block,
// so the wait is bounded.
func (l *voiceListener) stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	l.mu.Unlock()

	close(l.done)
	l.wg.Wait()
}

// --- send ------------------------------------------------------------------

// voiceFrame is one encoded 10ms frame on its way to the wire.
type voiceFrame struct {
	opus  []byte
	final bool
}

// voiceSequence tracks the wire frame counter of an outgoing transmission.
//
// Mumble counts frames, not packets: one 10ms frame per packet means +1 per
// packet. The terminator ends the transmission, so the next one starts a fresh
// count - this mirrors gumble's OpusOutgoing, where a new channel means a new
// goroutine with seq back at zero.
type voiceSequence struct {
	next int64
}

// step returns the sequence for the frame about to be written and advances the
// counter.
func (s *voiceSequence) step(final bool) int64 {
	seq := s.next
	if final {
		s.next = 0
		return seq
	}
	s.next = (s.next + 1) % voiceSequenceWrap
	return seq
}

func (s *voiceSequence) reset() { s.next = 0 }

// voiceIO owns the voice transport of a Manager: one RX buffer and one sender
// goroutine, both outliving individual sessions. Sessions come and go through
// bind/unbind, while the channels the audio pipeline holds never change.
type voiceIO struct {
	log *slog.Logger

	rx *dropBuffer[VoicePacket]
	tx *dropBuffer[voiceFrame]

	mu       sync.Mutex
	client   *gumble.Client
	proto    bool
	server   string
	listener *voiceListener

	// target is the wire voice target of outgoing frames (see SetVoiceTarget).
	// Stored as uint32 only because atomics have no byte flavor.
	target atomic.Uint32

	txOffline atomic.Uint64
	txErrors  atomic.Uint64

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func newVoiceIO(log *slog.Logger) *voiceIO {
	v := &voiceIO{
		log:  log,
		rx:   newDropBuffer[VoicePacket](voiceRXBuffer),
		tx:   newDropBuffer[voiceFrame](voiceTXBuffer),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go v.sendLoop()
	return v
}

// sendLoop is the only writer of outgoing audio. It lives as long as the
// Manager: reconnects swap the client underneath it instead of restarting it.
func (v *voiceIO) sendLoop() {
	defer close(v.done)

	var (
		current *gumble.Client
		seq     voiceSequence
	)
	for {
		select {
		case <-v.stop:
			return
		case frame := <-v.tx.out():
			client, proto, server := v.binding()
			if client == nil {
				v.txOffline.Add(1)
				continue
			}
			if client != current {
				// A new session is a new transmission as far as the server is
				// concerned; the old count means nothing to it.
				current = client
				seq.reset()
			}
			// Conn.WriteAudio serializes on the Conn mutex, so writing from
			// here is safe next to the read loop and to Client.Do.
			err := client.Conn.WriteAudio(
				voiceCodecOpus, byte(v.target.Load()),
				seq.step(frame.final), frame.final,
				frame.opus, nil, nil, nil, proto,
			)
			if err != nil {
				v.txErrors.Add(1)
				// Socket errors carry host:port; log records must not.
				v.log.Warn("voice frame write failed", "error", RedactServer(err.Error(), server))
			}
		}
	}
}

// bind points the sender at a freshly synced session.
//
// The protobuf framing flag is resolved here, once, rather than per frame:
// Client.Version is written during the handshake and dial only returns after
// the sync completed, so this read cannot race the read loop.
func (v *voiceIO) bind(client *gumble.Client, server string) {
	proto := voiceProtoMode(client)

	v.mu.Lock()
	v.client, v.proto, v.server = client, proto, server
	v.mu.Unlock()
}

// unbind detaches a dead session, so frames queued from now on are dropped as
// offline rather than written to a dying socket.
//
// The session's stream pumps are deliberately left alone here: this runs while
// the socket may still be closing, and a pump that quit now could strand
// gumble's read loop on an unbuffered handoff. They are retired by newListener
// before the next dial, and by close.
func (v *voiceIO) unbind() {
	v.mu.Lock()
	v.client = nil
	v.mu.Unlock()
}

// newListener retires the previous session's listener - by now its read loop is
// gone - and returns one for the session about to be dialed.
func (v *voiceIO) newListener() *voiceListener {
	l := newVoiceListener(v.rx)

	v.mu.Lock()
	previous := v.listener
	v.listener = l
	v.mu.Unlock()

	previous.stop()
	return l
}

func (v *voiceIO) binding() (client *gumble.Client, proto bool, server string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.client, v.proto, v.server
}

func (v *voiceIO) stats() VoiceStats {
	return VoiceStats{
		RXDrops:   v.rx.dropped(),
		TXDrops:   v.tx.dropped(),
		TXOffline: v.txOffline.Load(),
		TXErrors:  v.txErrors.Load(),
	}
}

// close stops the sender and releases the current session's pumps. The RX
// channel is deliberately left open: closing it would turn a late receive in
// the audio pipeline into a flood of zero values.
func (v *voiceIO) close() {
	v.stopOnce.Do(func() { close(v.stop) })
	<-v.done

	v.mu.Lock()
	v.client = nil
	listener := v.listener
	v.listener = nil
	v.mu.Unlock()

	listener.stop()
}

// voiceProtoMode reports whether the server speaks the 1.5 protobuf audio
// framing. Mirrors gumble's own check in writeOpusAudio.
func voiceProtoMode(client *gumble.Client) bool {
	if client == nil || client.Version == nil {
		return false
	}
	return client.Version.Version >= uint64(1<<48|5<<32)
}
