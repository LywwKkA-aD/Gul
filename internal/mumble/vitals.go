package mumble

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// Vitals is one reading of what a live connection has actually carried, taken
// from the outside while it is still running.
//
// It exists because of a failure the logs could not describe. A user connected,
// authenticated, and was dropped a few seconds later, every time, on every road
// and every version; the relay counted the same number of bytes from him in
// every session, and the window said "connection lost". Nothing in either
// journal separated the three ways that can happen: a write of ours that never
// returns, a write that returns while nothing crosses the network, or no write
// attempted at all. These fields separate them.
//
// Sent counts the Mumble bytes this client handed to the socket, so it can be
// held against the relay's own count of what arrived. Out and In name the
// packets by type, which is the reading that says how far the exchange got -
// whether serversync ever arrived, whether ping is still going out.
type Vitals struct {
	// Sent and Received are plaintext Mumble bytes, before TLS and framing.
	Sent, Received int64
	// Out and In tally packets by type, non-empty entries only.
	Out, In string
	// Blocked is how long the write happening right now has been waiting.
	// Zero when no write is in flight - which, when Out has stopped growing,
	// is the difference between a stuck uplink and a silent one.
	Blocked time.Duration
	// Silent is how long since anything arrived, measured from the moment the
	// connection was made when nothing ever has.
	Silent time.Duration
	// Slowest is the longest single write this connection has completed.
	Slowest time.Duration
	// Stalled latches the one-directional failure (ErrUplinkStalled).
	Stalled bool
	// Err is the first error the socket reported, which is usually the only
	// account of why a session ended.
	Err error
}

// redact returns the reading with the transport error rewritten so it cannot
// name the server.
//
// Log records travel in diagnostics archives that users hand to other people,
// and a socket error is exactly where an address turns up: "read tcp
// 1.2.3.4:5->6.7.8.9:443". Everything else on this panel is counters and
// packet types, which name nothing.
func (v Vitals) redact(address string) Vitals {
	if v.Err != nil {
		v.Err = errors.New(RedactServer(v.Err.Error(), address))
	}
	return v
}

// LogValue renders the reading as one group, so a log line carries it under a
// single key and every field keeps its name.
func (v Vitals) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.Int64("sent", v.Sent),
		slog.Int64("received", v.Received),
		slog.String("out", v.Out),
		slog.String("in", v.In),
		slog.Duration("blocked", v.Blocked.Round(time.Millisecond)),
		slog.Duration("silent", v.Silent.Round(time.Millisecond)),
		slog.Duration("slowest", v.Slowest.Round(time.Millisecond)),
	}
	if v.Stalled {
		attrs = append(attrs, slog.Bool("stalled", true))
	}
	if v.Err != nil {
		attrs = append(attrs, slog.String("error", v.Err.Error()))
	}
	return slog.GroupValue(attrs...)
}

// packetKinds names the Mumble TCP packet types by their wire number. The order
// is the protocol's own and gumble dispatches on the same index (handlers.go);
// a number past the end is a type this build does not know, which is worth
// counting under its number rather than dropping.
var packetKinds = [...]string{
	"version", "udptunnel", "authenticate", "ping", "reject", "serversync",
	"channelremove", "channelstate", "userremove", "userstate", "banlist",
	"textmessage", "permissiondenied", "acl", "queryusers", "cryptsetup",
	"contextactionmodify", "contextaction", "userlist", "voicetarget",
	"permissionquery", "codecversion", "userstats", "requestblob",
	"serverconfig", "suggestconfig",
}

// tally counts packets by type in one direction.
//
// Counting is atomic because the reading is taken from the session goroutine
// while the read loop and the send path keep writing to it.
type tally struct {
	known   [len(packetKinds)]atomic.Int64
	unknown atomic.Int64
	total   atomic.Int64
}

func (t *tally) add(kind uint16) {
	t.total.Add(1)
	if int(kind) < len(t.known) {
		t.known[kind].Add(1)
		return
	}
	t.unknown.Add(1)
}

// String renders the non-zero entries, which keeps the line short: a session
// touches perhaps eight of the twenty-six types.
func (t *tally) String() string {
	var b strings.Builder
	for i := range t.known {
		count := t.known[i].Load()
		if count == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(packetKinds[i])
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(count, 10))
	}
	if count := t.unknown.Load(); count > 0 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("unknown=")
		b.WriteString(strconv.FormatInt(count, 10))
	}
	return b.String()
}

// inboundFramer finds whole Mumble packets in a stream that arrives in whatever
// sized pieces the socket delivers, so the inbound tally counts packets and not
// reads.
//
// Only the read loop touches it - gumble reads the connection from one
// goroutine and nothing else does - so it holds no lock; the tally it feeds
// carries the crossing to other goroutines.
type inboundFramer struct {
	header [packetHeaderBytes]byte
	have   int
	remain int
}

// consume walks b, counting every packet header it completes and skipping the
// payloads between them.
func (f *inboundFramer) consume(b []byte, into *tally) {
	for len(b) > 0 {
		if f.remain > 0 {
			skip := min(f.remain, len(b))
			f.remain -= skip
			b = b[skip:]
			continue
		}
		n := copy(f.header[f.have:], b)
		f.have += n
		b = b[n:]
		if f.have < packetHeaderBytes {
			return
		}
		kind := uint16(f.header[0])<<8 | uint16(f.header[1])
		length := uint32(f.header[2])<<24 | uint32(f.header[3])<<16 |
			uint32(f.header[4])<<8 | uint32(f.header[5])
		// A length past what the relay would carry means the stream is not
		// where this thinks it is. Counting is a diagnostic and must never be
		// the thing that ends a session, so it stops looking rather than
		// failing: the tally goes quiet, and the framing error surfaces
		// wherever it really belongs.
		if length > relayMaxPacketPayload {
			f.remain, f.have = 0, 0
			f.stop()
			return
		}
		f.remain = int(length)
		f.have = 0
		into.add(kind)
	}
}

// stop parks the framer past the end of any real stream, so it counts nothing
// more without needing a flag of its own.
func (f *inboundFramer) stop() { f.remain = maxFramerSkip }

const (
	// maxFramerSkip is large enough that no session outlives it and small
	// enough to stay far from overflow.
	maxFramerSkip = 1 << 62
	// relayMaxPacketPayload is the largest payload a packet may declare, the
	// same bound packetBounds applies to what we send.
	relayMaxPacketPayload = uint32(relayproto.MaxMessageBytes - packetHeaderBytes)
)
