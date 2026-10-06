// Package screenbridge serves an authenticated browser screen companion on
// loopback for desktop webviews that cannot provide WebRTC or display capture.
package screenbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

var (
	ErrUnavailable = errors.New("не удалось открыть демонстрации в браузере")
	ErrStale       = errors.New("канал изменился; откройте демонстрации заново")
)

type Provider interface {
	Status() domain.ConnectionStatus
	ScreenGrant(context.Context, uint64, uint32) (domain.ScreenGrant, error)
}

type browserSession struct {
	code, token string
	epoch       uint64
	channel     uint32
	server      string
	expires     time.Time
}

type Bridge struct {
	provider     Provider
	assets       fs.FS
	openURL      func(string) error
	mu           sync.Mutex
	server       *http.Server
	origin, host string
	active       *browserSession
	closed       bool
}

// New is lazy: no listening socket or browser exists until an explicit click.
func New(provider Provider, assets fs.FS, openURL func(string) error) *Bridge {
	return &Bridge{provider: provider, assets: assets, openURL: openURL}
}

func (b *Bridge) Open(ctx context.Context, epoch uint64, channel uint32) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := b.provider.Status()
	if !matches(status, epoch, channel) {
		return ErrStale
	}
	code, err := secret()
	if err != nil {
		return ErrUnavailable
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrUnavailable
	}
	if err := b.startLocked(); err != nil {
		b.mu.Unlock()
		return ErrUnavailable
	}
	session := &browserSession{code: code, epoch: epoch, channel: channel, server: status.Server, expires: time.Now().Add(time.Minute)}
	b.active = session
	address := b.origin + "/screen.html#" + code
	b.mu.Unlock()
	if b.openURL == nil || b.openURL(address) != nil {
		b.mu.Lock()
		if b.active == session {
			b.active = nil
		}
		b.mu.Unlock()
		return ErrUnavailable
	}
	return nil
}

func (b *Bridge) startLocked() error {
	if b.server != nil {
		return nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	b.host = listener.Addr().String()
	b.origin = "http://" + b.host
	b.server = &http.Server{Handler: http.HandlerFunc(b.serve), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 20 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	server := b.server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (b *Bridge) Close() error {
	b.mu.Lock()
	b.closed = true
	b.active = nil
	server := b.server
	b.server = nil
	b.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}

func matches(status domain.ConnectionStatus, epoch uint64, channel uint32) bool {
	return status.State == domain.StateConnected && epoch > 0 && status.Epoch == epoch && status.SelfChannel == channel
}

func secret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
