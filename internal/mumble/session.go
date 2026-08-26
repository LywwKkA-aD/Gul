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

// dialTimeout bounds one attempt end to end: gumble.DialWithDialer only returns
// after the server has finished syncing its state, and uses the dialer timeout
// as the deadline for that sync too.
const dialTimeout = 10 * time.Second

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
	if ep.kind == endpointWSS {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		conn, dialErr := dialRelay(ctx, ep, relayCredential(cfg), tofu, cfg.Certificate, log)
		if dialErr != nil {
			return nil, fmt.Errorf("dial %s: %w", ep.address, dialErr)
		}
		// One Mumble packet per WebSocket message (see newPacketConn); the
		// wrapper belongs on the connection gumble writes packets into.
		packets := newPacketConn(conn)
		s.packets = packets
		client, err = gumble.DialWithConn(ctx, packets, gc)
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

// dialRelay reaches the relay by whichever road works.
//
// WebSocket over TCP first, because it is the one every deployed relay speaks
// and the one that has carried every session so far; QUIC over UDP when that
// fails to establish. A network that drops one does not necessarily drop the
// other, and the user should not have to know which.
//
// This is failure-to-connect fallback, not yet a choice: a road that connects
// and then carries nothing still has to be found by the round-trip gate in the
// manager, and picking between roads on that evidence is the next step.
func dialRelay(
	ctx context.Context,
	ep endpoint,
	credential relayproto.Credential,
	tofu *TOFUStore,
	certificate *tls.Certificate,
	log *slog.Logger,
) (net.Conn, error) {
	conn, wssErr := dialWSSMumbleTLS(ctx, ep, credential, tofu, certificate, nil)
	if wssErr == nil {
		return conn, nil
	}
	if isTerminalRelayError(wssErr) {
		// A refused credential or a rate limit says nothing about the road, so
		// trying the other one would only spend the user's time twice.
		return nil, wssErr
	}
	log.Info("relay unreachable over websocket, trying quic",
		"error", RedactServer(wssErr.Error(), ep.address))
	conn, quicErr := dialQUICMumbleTLS(ctx, ep, credential, tofu, certificate, nil)
	if quicErr == nil {
		log.Info("relay reached over quic")
		return conn, nil
	}
	// The WebSocket failure is the one the user needs: it is the road that is
	// supposed to work, and the QUIC attempt was the long shot.
	return nil, wssErr
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
