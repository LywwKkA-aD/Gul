package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumbleutil"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// dialTimeout bounds getting the connection open: the transport dial, the TLS
// handshake, and on the relay roads the tunnel handshake in front of them. All
// of that is small and fixed in size, so a fixed budget fits it.
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
	Address  string // host[:port] or wss://host[:port]/mumble
	Username string
	Password string
	// Certificate is the persistent client identity. When nil the server sees
	// an anonymous client and User.Hash stays empty.
	Certificate *tls.Certificate
	// RelayCredential is the bearer for the WSS relay. Deriving it costs
	// roughly 50 ms of PBKDF2, so the Manager derives it once per Connect and
	// every reconnect reuses it; an empty value here is derived on the spot.
	RelayCredential relayproto.Credential
	// Transport is the road to try (transport.go). Empty means the WebSocket
	// one, which is what every deployed relay speaks. Ignored for a direct
	// Mumble address, which has only one road.
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
	// packets is the framing wrapper on the relay path, kept so the reason a
	// session ended can name a stalled uplink instead of "connection lost".
	// Nil on the direct path, which has no wrapper of its own.
	packets *packetConn
	// closeOnce funnels every Disconnect into one actual client call:
	// gumble's Client.Disconnect writes its state without a lock, and both the
	// reconnect loop and stopRun legitimately try to close the same session.
	closeOnce sync.Once
}

// stalledUplink reports whether this session died because our own traffic
// stopped getting through while the server's kept arriving (packetconn.go).
func (s *Session) stalledUplink() bool {
	return s != nil && s.packets != nil && s.packets.StalledUplink()
}

// vitals reads the instrument panel of this session's connection (vitals.go).
// The direct road has no wrapper of its own and therefore no panel, which is
// what the second return value says.
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

	var client *gumble.Client
	if ep.kind == endpointRelay {
		// Two budgets, because the two phases fail differently. Opening the
		// connection is bounded by the clock; the sync that follows is bounded
		// by silence (syncSilence).
		dialCtx, cancelDial := context.WithTimeout(context.Background(), dialTimeout)
		conn, dialErr := dialRelay(dialCtx, ep, cfg.Transport, relayCredential(cfg), tofu, cfg.Certificate)
		// The road is open or it is not; neither dial keeps this context past
		// its own handshake (wss.go, quic.go), so it is released here rather
		// than being left to bound the sync as well.
		cancelDial()
		if dialErr != nil {
			return nil, fmt.Errorf("dial %s: %w", ep.address, dialErr)
		}
		// One Mumble packet per WebSocket message (see newPacketConn); the
		// wrapper belongs on the connection gumble writes packets into.
		packets := newPacketConn(conn)
		s.packets = packets
		syncCtx, cancelSync := syncingContext(packets)
		defer cancelSync()
		client, err = gumble.DialWithConn(syncCtx, packets, gc)
	} else {
		tlsConfig := tofu.TLSConfig(ep.host)
		if cfg.Certificate != nil {
			tlsConfig.Certificates = []tls.Certificate{*cfg.Certificate}
		}
		dialer := &net.Dialer{Timeout: dialTimeout}
		client, err = gumble.DialWithDialer(dialer, ep.address, gc, tlsConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", ep.address, err)
	}
	s.client = client
	return s, nil
}

// syncingContext ends when the connection has been silent for syncSilence.
//
// A sync that is merely slow keeps its context; one that has stopped loses it.
// The connection itself is the evidence - packetConn records every read - so
// this asks the same question the rest of the transport does: is anything still
// arriving?
func syncingContext(packets *packetConn) (context.Context, context.CancelFunc) {
	return syncingContextSince(packets, time.Now())
}

// syncingContextSince is syncingContext with the start of the wait supplied, so
// a test can reach the timeout without spending it.
func syncingContextSince(packets *packetConn, started time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
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

// dialRelay takes the road it is given.
//
// Which road that is comes from the Manager, which rotates through them on the
// evidence that matters - whether packets of ours come back - rather than
// falling back here on a failure to connect. A road that connects and then
// carries nothing is the failure this whole milestone exists for, and it is
// invisible from inside a dial (transport.go).
func dialRelay(
	ctx context.Context,
	ep endpoint,
	transport Transport,
	credential relayproto.Credential,
	tofu *TOFUStore,
	certificate *tls.Certificate,
) (net.Conn, error) {
	road, ok := relayRoads[transport]
	if !ok {
		// A road named but not built. This was an else branch until now, so
		// anything that was not QUIC quietly became WebSocket: a road added to
		// relayTransports without being added here would not have failed, it
		// would have lied - the session would run, and the chooser would
		// record its success under the wrong name and write that to disk.
		return nil, fmt.Errorf("mumble: no road named %q", transport)
	}
	return road(ctx, ep, credential, tofu, certificate)
}

// relayRoad opens one road to the relay and returns the connection gumble will
// speak Mumble over. Each road's own dial takes one more argument than this -
// a seam its tests reach in with - and the entries below supply it.
type relayRoad func(
	context.Context,
	endpoint,
	relayproto.Credential,
	*TOFUStore,
	*tls.Certificate,
) (net.Conn, error)

// relayRoads is every road that exists, by the name the chooser and the
// settings file use for it. Adding a road is one entry here and one in
// relayTransports, which decides the order they are tried in.
var relayRoads = map[Transport]relayRoad{
	TransportWSS: func(ctx context.Context, ep endpoint, credential relayproto.Credential,
		tofu *TOFUStore, certificate *tls.Certificate) (net.Conn, error) {
		return dialWSSMumbleTLS(ctx, ep, credential, tofu, certificate, nil)
	},
	TransportQUIC: func(ctx context.Context, ep endpoint, credential relayproto.Credential,
		tofu *TOFUStore, certificate *tls.Certificate) (net.Conn, error) {
		return dialQUICMumbleTLS(ctx, ep, credential, tofu, certificate, nil)
	},
}

// isTerminalRelayError reports whether a failure is about who is calling
// rather than about the road, in which case another road cannot help.
func isTerminalRelayError(err error) bool {
	return errors.Is(err, ErrRelayPasswordRequired) ||
		errors.Is(err, ErrRelayAuthentication) ||
		errors.Is(err, ErrRelayNotFound) ||
		errors.Is(err, ErrRelayRateLimited) ||
		errors.Is(err, ErrRelayFull)
}

// relayCredential returns the bearer for the relay, deriving it when the
// caller did not. An empty password yields an empty credential, which dialWSS
// rejects before it touches the network.
func relayCredential(cfg DialConfig) relayproto.Credential {
	if cfg.RelayCredential != "" || cfg.Password == "" {
		return cfg.RelayCredential
	}
	return relayproto.Derive([]byte(cfg.Password))
}

// Disconnect closes the connection exactly once; later calls are no-ops.
// gumble reports "already disconnected" as an error, which is not interesting
// to any caller here.
func (s *Session) Disconnect() error {
	if s == nil || s.client == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		if s.client.State() == gumble.StateDisconnected {
			return
		}
		err = s.client.Disconnect()
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
