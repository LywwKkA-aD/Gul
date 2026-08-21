package mumble

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/stieneee/gumble/gumble"
	"github.com/stieneee/gumble/gumbleutil"
)

// dialTimeout bounds one attempt end to end: gumble.DialWithDialer only returns
// after the server has finished syncing its state, and uses the dialer timeout
// as the deadline for that sync too.
const dialTimeout = 10 * time.Second

// DialConfig carries everything needed to establish one Mumble session.
type DialConfig struct {
	Address  string // host:port; port defaults to 64738 when missing
	Username string
	Password string
	// Certificate is the persistent client identity. When nil the server sees
	// an anonymous client and User.Hash stays empty.
	Certificate *tls.Certificate
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
}

// Session wraps one live gumble connection. A gumble Client is dead once
// disconnected, so a Session is single-use: reconnecting means dialing a new
// one.
type Session struct {
	client *gumble.Client
	log    *slog.Logger
	addr   string
	host   string
}

// Dial opens a session with plain logging hooks. It is the M0 entry point kept
// for the dev stand and the live smoke test; the Manager uses dial directly so
// it can route events into snapshots and callbacks.
func Dial(cfg DialConfig, tofu *TOFUStore, log *slog.Logger) (*Session, error) {
	return dial(cfg, tofu, loggingHooks(log), log)
}

func dial(cfg DialConfig, tofu *TOFUStore, hooks sessionHooks, log *slog.Logger) (*Session, error) {
	addr, host := normalizeAddress(cfg.Address)
	s := &Session{log: log.With("server", addr), addr: addr, host: host}

	// Config must be fully populated before Dial: Client and Config are
	// thread-unsafe once the read loop is running.
	gc := gumble.NewConfig()
	gc.Username = cfg.Username
	gc.Password = cfg.Password
	gc.Attach(gumbleutil.Listener{
		Connect:          hooks.connect,
		Disconnect:       hooks.disconnect,
		ChannelChange:    hooks.channelChange,
		UserChange:       hooks.userChange,
		TextMessage:      hooks.textMessage,
		PermissionDenied: hooks.permissionDenied,
	})

	tlsConfig := tofu.TLSConfig(host)
	if cfg.Certificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*cfg.Certificate}
	}

	dialer := &net.Dialer{Timeout: dialTimeout}
	client, err := gumble.DialWithDialer(dialer, addr, gc, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	s.client = client
	return s, nil
}

// Disconnect closes the connection. gumble reports "already disconnected" as an
// error, which is not interesting to the caller here.
func (s *Session) Disconnect() error {
	if s == nil || s.client == nil {
		return nil
	}
	if s.client.State() == gumble.StateDisconnected {
		return nil
	}
	return s.client.Disconnect()
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

// normalizeAddress appends the default Mumble port when the caller omitted it
// and returns the dial address plus the bare host used as the TOFU pin key.
func normalizeAddress(address string) (addr, host string) {
	addr = address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(gumble.DefaultPort))
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return addr, host
}

// loggingHooks keep the M0 behaviour: lifecycle visible in the log, no tree
// dump - the tree now travels as a snapshot through the Manager callbacks.
func loggingHooks(log *slog.Logger) sessionHooks {
	return sessionHooks{
		connect: func(e *gumble.ConnectEvent) {
			welcome := ""
			if e.WelcomeMessage != nil {
				welcome = *e.WelcomeMessage
			}
			log.Info("connected", "welcome", welcome, "users", len(e.Client.Users))
		},
		disconnect: func(e *gumble.DisconnectEvent) {
			log.Info("disconnected", "type", int(e.Type), "reason", e.String)
		},
		permissionDenied: func(e *gumble.PermissionDeniedEvent) {
			log.Warn("permission denied", "type", int(e.Type), "reason", e.String)
		},
	}
}
