package mumble

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumbleutil"

	"github.com/LywwKkA-aD/Gul/internal/identity"
)

// dialTimeout bounds getting the connection open: the transport dial, the TLS
// handshake and Hysteria TCP stream setup. All of that is small and fixed in
// size, so a fixed budget fits it.
const dialTimeout = 10 * time.Second

// syncSilence bounds what happens next, and it is measured differently on
// purpose.
//
// gumble returns from a dial only once the server has finished sending its
// state, and how much that is depends on the room, not on us: the channel and
// user tree, and - because the server starts relaying to a client the moment it
// has authenticated - everyone else's voice while they talk. On a thin link
// that is easily more than ten seconds of data.
//
// Bounding it by total time is therefore the wrong rule, and it was the bug: a
// user on a slow connection was cut off at exactly ten seconds, every attempt,
// forever, while the server's log showed the login succeeding every time. What
// says a connection is dead is silence, not slowness, so this is the longest
// the server may deliver nothing at all before we give up on it.
const syncSilence = 10 * time.Second

// DialConfig carries everything needed to establish one Mumble session.
type DialConfig struct {
	// Context cancels connection setup and initial Mumble synchronization.
	// Canceling it after Dial succeeds does not close the live session.
	Context  context.Context
	Address  string // hysteria2://host[:port] (bare hosts default to UDP 443)
	Username string
	Password string
	// Certificate is used only by callers without a derived IdentitySeed.
	Certificate *tls.Certificate
	// IdentitySeed is the master secret this user is known by
	// (internal/identity). When empty, Certificate supplies a legacy identity.
	IdentitySeed []byte
	// OuterRoots overrides who signs the Hysteria server certificate. Nil means
	// the system trust store, which is what production uses; a live test
	// supplies a CA for its local Hysteria server.
	OuterRoots *tls.Config
	// Transport identifies the embedded Hysteria transport in diagnostics.
	Transport Transport
}

// sessionHooks receive gumble events for one session. Every hook runs on a
// gumble goroutine - in practice the single read loop that also handles ping,
// user and channel updates - so a hook must return quickly and must never
// block: a stalled hook stalls the whole protocol and the connection dies on
// the 20s read deadline.
type sessionHooks struct {
	connect          func(*gumble.ConnectEvent)
	disconnect       func(*gumble.DisconnectEvent)
	channelChange    func(*gumble.ChannelChangeEvent)
	userChange       func(*gumble.UserChangeEvent)
	textMessage      func(*gumble.TextMessageEvent)
	permissionDenied func(*gumble.PermissionDeniedEvent)
	// audio receives raw Opus streams. It is attached before Dial like every
	// other listener, and unlike the hooks above it owns its own goroutines:
	// gumble delivers stream packets over an unbuffered channel written from
	// the read loop.
	audio gumble.AudioListener
}

// Session wraps one live gumble connection. A gumble Client is dead once
// disconnected, so a Session is single-use: reconnecting means dialing a new
// one.
type Session struct {
	client *gumble.Client
	log    *slog.Logger
	addr   string
	host   string
	// packets owns the tunnel and records why it ended, including stalled uplink.
	packets *packetConn
	// closeOnce releases the tunnel once when Disconnect and reconnect race.
	closeOnce sync.Once
}

// stalledUplink reports whether this session died because our own traffic
// stopped getting through while the server's kept arriving (packetconn.go).
func (s *Session) stalledUplink() bool {
	return s != nil && s.packets != nil && s.packets.StalledUplink()
}

// vitals reads the instrument panel of this session's connection (vitals.go).
// The second return value is false when no transport has been attached.
func (s *Session) vitals() (Vitals, bool) {
	if s == nil || s.packets == nil {
		return Vitals{}, false
	}
	return s.packets.Vitals(), true
}

// transportError is what the connection itself reported, which is usually the
// only account of why a session ended: gumble's own reason is empty unless the
// server sent one.
func (s *Session) transportError() error {
	if s == nil || s.packets == nil {
		return nil
	}
	return s.packets.TransportError()
}

// Dial opens a session with plain logging hooks. It is the M0 entry point kept
// for the dev stand and the live smoke test; the Manager uses dial directly so
// it can route events into snapshots and callbacks.
func Dial(cfg DialConfig, tofu *TOFUStore, log *slog.Logger) (*Session, error) {
	return dial(cfg, tofu, loggingHooks(log, cfg.Address), log)
}

func dial(cfg DialConfig, tofu *TOFUStore, hooks sessionHooks, log *slog.Logger) (*Session, error) {
	ep, err := parseEndpoint(cfg.Address)
	if err != nil {
		return nil, err
	}
	// The logger carries no server attribute: log records travel in shareable
	// diagnostics archives and must not name the address the user connected to.
	s := &Session{log: log, addr: ep.address, host: ep.host}

	// The stub codec must be in gumble's registry before Dial: the Authenticate
	// packet advertises Opus only when codec id 4 is registered, and without
	// that flag the server refuses to route our voice.
	registerVoiceCodec()

	// Config must be fully populated before Dial: Client and Config are
	// thread-unsafe once the read loop is running.
	gc := gumble.NewConfig()
	gc.Username = cfg.Username
	gc.Password = cfg.Password
	// Voice runs in passthrough: gumble hands us raw Opus frames instead of
	// decoding them, and one packet carries one 10ms frame.
	gc.OpusPassthrough = true
	gc.AudioInterval = gumble.AudioDefaultInterval
	gc.Attach(gumbleutil.Listener{
		Connect:          hooks.connect,
		Disconnect:       hooks.disconnect,
		ChannelChange:    hooks.channelChange,
		UserChange:       hooks.userChange,
		TextMessage:      hooks.textMessage,
		PermissionDenied: hooks.permissionDenied,
	})
	if hooks.audio != nil {
		gc.AttachAudio(hooks.audio)
	}

	// Two budgets, because the two phases fail differently. Opening the
	// connection is bounded by the clock; the sync that follows is bounded by
	// silence (syncSilence).
	parent := cfg.Context
	if parent == nil {
		parent = context.Background()
	}
	dialCtx, cancelDial := context.WithTimeout(parent, dialTimeout)
	conn, dialErr := dialHysteria(dialCtx, cfg, ep, tofu)
	// A completed tunnel no longer depends on the setup context.
	cancelDial()
	if dialErr != nil {
		return nil, fmt.Errorf("dial %s: %w", ep.address, dialErr)
	}
	// Frame application packets above TLS so protocol keepalives and QUIC
	// handshakes never count as Mumble activity for the silence watchdog.
	packets := newPacketConn(conn)
	s.packets = packets
	syncCtx, cancelSync := syncingContextWithParent(parent, packets, time.Now())
	defer cancelSync()
	client, err := gumble.DialWithConn(syncCtx, packets, gc)
	if err != nil {
		_ = packets.Close()
		return nil, fmt.Errorf("dial %s: %w", ep.address, err)
	}
	if err := checkIdentity(client, ep.host, cfg.IdentitySeed, log); err != nil {
		_ = packets.Close()
		return nil, err
	}
	s.client = client
	return s, nil
}

// checkIdentity verifies that Murmur reports the identity presented by the
// client's end-to-end TLS session. A server may allow anonymous users.
func checkIdentity(client *gumble.Client, host string, master []byte, log *slog.Logger) error {
	if len(master) == 0 || client == nil || client.Self == nil {
		return nil
	}
	expected, err := identity.ForHost(master, host)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	switch got := client.Self.Hash; got {
	case expected.Fingerprint:
	case "":
		log.Warn("the server did not ask who we are; this session is anonymous")
	default:
		// The server reported a certificate that is not ours. Whoever
		// we are logged in as, it is not who this client believes it is.
		return fmt.Errorf(
			"the server knows this session as somebody else: it reports %s where this client is %s",
			got, expected.Fingerprint)
	}
	return nil
}

// syncingContextSince bounds setup by silence since the supplied start time.
// A slow sync that keeps delivering Mumble bytes remains alive.
func syncingContextSince(packets *packetConn, started time.Time) (context.Context, context.CancelFunc) {
	return syncingContextWithParent(context.Background(), packets, started)
}

func syncingContextWithParent(parent context.Context, packets *packetConn, started time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		// A quarter of the budget: often enough to notice promptly, rarely
		// enough that a slow sync pays nothing for being watched.
		ticker := time.NewTicker(syncSilence / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if packets.SilentFor(started) >= syncSilence {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

// Disconnect closes the connection exactly once; later calls are no-ops.
// Closing the owned stream also releases Hysteria after a remote EOF.
func (s *Session) Disconnect() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		// gumble marks a read failure disconnected without closing its Conn.
		// Always release the owned stream and Hysteria client, including after
		// a remote EOF. Closing the socket also avoids Client.Disconnect's
		// unsynchronized write to the read loop's disconnect event.
		if s.packets != nil {
			err = s.packets.Close()
		} else if s.client != nil && s.client.Conn != nil {
			err = s.client.Conn.Close()
		}
	})
	return err
}

func (s *Session) State() string {
	if s == nil || s.client == nil {
		return "disconnected"
	}
	switch s.client.State() {
	case gumble.StateConnected:
		return "connected"
	case gumble.StateSynced:
		return "synced"
	default:
		return "disconnected"
	}
}

// loggingHooks keep the M0 behaviour: lifecycle visible in the log, no tree
// dump - the tree now travels as a snapshot through the Manager callbacks.
//
// address is only ever used to keep itself out of the records: a disconnect
// reason is often a network error carrying host:port or a resolved IP, and
// gul.log travels in shareable diagnostics archives (PLAN.md §10.7).
func loggingHooks(log *slog.Logger, address string) sessionHooks {
	return sessionHooks{
		connect: func(e *gumble.ConnectEvent) {
			welcome := ""
			if e.WelcomeMessage != nil {
				welcome = *e.WelcomeMessage
			}
			log.Info("connected", "welcome", welcome, "users", len(e.Client.Users))
		},
		disconnect: func(e *gumble.DisconnectEvent) {
			log.Info("disconnected", "type", int(e.Type), "reason", RedactServer(e.String, address))
		},
		permissionDenied: func(e *gumble.PermissionDeniedEvent) {
			log.Warn("permission denied", "type", int(e.Type), "reason", e.String)
		},
	}
}
