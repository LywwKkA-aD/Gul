package mumble

import (
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/stieneee/gumble/gumble"
	"github.com/stieneee/gumble/gumbleutil"
)

// DialConfig carries everything needed to establish one Mumble session.
type DialConfig struct {
	Address  string // host:port; port defaults to 64738 when missing
	Username string
	Password string
}

// Session wraps one live gumble connection. M0 scope: connect, log the channel
// tree and server events; voice comes in M2.
type Session struct {
	client *gumble.Client
	log    *slog.Logger
}

func Dial(cfg DialConfig, tofu *TOFUStore, log *slog.Logger) (*Session, error) {
	addr := cfg.Address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "64738")
	}
	host, _, _ := net.SplitHostPort(addr)

	s := &Session{log: log.With("server", addr)}

	gc := gumble.NewConfig()
	gc.Username = cfg.Username
	gc.Password = cfg.Password
	gc.Attach(gumbleutil.Listener{
		Connect:    s.onConnect,
		Disconnect: s.onDisconnect,
		UserChange: s.onUserChange,
		ChannelChange: func(e *gumble.ChannelChangeEvent) {
			s.log.Debug("channel change", "channel", e.Channel.Name, "type", int(e.Type))
		},
		TextMessage: func(e *gumble.TextMessageEvent) {
			sender := ""
			if e.Sender != nil {
				sender = e.Sender.Name
			}
			s.log.Info("text message", "from", sender, "message", e.Message)
		},
		PermissionDenied: func(e *gumble.PermissionDeniedEvent) {
			s.log.Warn("permission denied", "type", int(e.Type))
		},
	})

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	client, err := gumble.DialWithDialer(dialer, addr, gc, tofu.TLSConfig(host))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	s.client = client
	return s, nil
}

func (s *Session) Disconnect() error {
	if s.client == nil {
		return nil
	}
	return s.client.Disconnect()
}

func (s *Session) State() string {
	if s.client == nil {
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

func (s *Session) onConnect(e *gumble.ConnectEvent) {
	welcome := ""
	if e.WelcomeMessage != nil {
		welcome = *e.WelcomeMessage
	}
	s.log.Info("connected", "welcome", welcome, "users", len(e.Client.Users))
	for _, line := range channelTreeLines(e.Client.Channels[0], 0) {
		s.log.Info("channel tree " + line)
	}
}

func (s *Session) onDisconnect(e *gumble.DisconnectEvent) {
	s.log.Info("disconnected", "type", int(e.Type), "reason", e.String)
}

func (s *Session) onUserChange(e *gumble.UserChangeEvent) {
	channel := ""
	if e.User.Channel != nil {
		channel = e.User.Channel.Name
	}
	s.log.Info("user change", "user", e.User.Name, "channel", channel, "type", int(e.Type))
}

// channelTreeLines renders the channel hierarchy depth-first with users.
func channelTreeLines(ch *gumble.Channel, depth int) []string {
	if ch == nil {
		return nil
	}
	indent := strings.Repeat("  ", depth)
	line := fmt.Sprintf("%s- %s", indent, ch.Name)
	if len(ch.Users) > 0 {
		names := make([]string, 0, len(ch.Users))
		for _, u := range ch.Users {
			names = append(names, u.Name)
		}
		sort.Strings(names)
		line += " [" + strings.Join(names, ", ") + "]"
	}
	lines := []string{line}

	children := make([]*gumble.Channel, 0, len(ch.Children))
	for _, c := range ch.Children {
		children = append(children, c)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Position < children[j].Position })
	for _, c := range children {
		lines = append(lines, channelTreeLines(c, depth+1)...)
	}
	return lines
}
