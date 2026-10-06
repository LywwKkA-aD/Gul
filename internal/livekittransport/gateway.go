// Package livekittransport tunnels LiveKit HTTPS, signaling and TURN through
// Gul's embedded REALITY client. It never exposes a general-purpose proxy.
package livekittransport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/reality"
	"golang.org/x/net/netutil"
)

var ErrGateway = errors.New("LiveKit REALITY transport unavailable")

type Options struct {
	// RootCAs permits a separately trusted local fixture CA. Nil uses system
	// roots. Certificate and hostname verification are never disabled.
	RootCAs *x509.CertPool
}

type Gateway struct {
	profile     reality.LiveKitProfile
	config      reality.Config
	roots       *x509.CertPool
	ctx         context.Context
	cancel      context.CancelFunc
	http        *http.Server
	web         net.Listener
	turn        net.Listener
	client      *http.Transport
	slots       chan struct{}
	mu          sync.Mutex
	closed      bool
	epoch       uint64
	cap         string
	tokens      map[[32]byte]struct{}
	tokenOrder  [][32]byte
	originOrder []string
	mediaCtx    context.Context
	cancelMedia context.CancelFunc
	origins     map[string]struct{}
	conns       map[*trackedConn]uint64
	wg          sync.WaitGroup
	once        sync.Once
	dial        func(context.Context, reality.Config) (net.Conn, error)
}

// New opens only loopback listeners. The owner must Close after its final
// broker logout; canceling media setup must not prevent that authenticated
// cleanup request from using the same protected transport.
func New(profile reality.LiveKitProfile, password string, options Options) (*Gateway, error) {
	parsed, err := reality.ParseLiveKitProfile(profile.Address)
	if err != nil || parsed.Origin != profile.Origin || password == "" {
		return nil, ErrGateway
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &Gateway{profile: parsed, config: parsed.Config, roots: options.RootCAs, ctx: ctx, cancel: cancel,
		slots: make(chan struct{}, 32), tokens: make(map[[32]byte]struct{}), origins: make(map[string]struct{}),
		conns: make(map[*trackedConn]uint64), dial: reality.DialLiveKit}
	g.config.Password = password
	if options.RootCAs != nil {
		g.roots = options.RootCAs.Clone()
	}
	g.web, err = net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		g.turn, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		g.Close()
		return nil, ErrGateway
	}
	g.client = &http.Transport{Proxy: nil, DialTLSContext: g.dialTLS, DialContext: rejectPlainDial,
		MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	g.http = &http.Server{Handler: http.HandlerFunc(g.serveSignal), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	g.wg.Go(func() { _ = g.http.Serve(netutil.LimitListener(g.web, 32)) })
	g.wg.Go(g.acceptTURN)
	return g, nil
}

func rejectPlainDial(context.Context, string, string) (net.Conn, error) { return nil, ErrGateway }

func (g *Gateway) Transport() *http.Transport { return g.client }

func (g *Gateway) BeginEpoch(epoch uint64) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return ErrGateway
	}
	g.mu.Lock()
	if g.closed || epoch == 0 || epoch <= g.epoch {
		g.mu.Unlock()
		return ErrGateway
	}
	oldCancel := g.cancelMedia
	stale := make([]*trackedConn, 0)
	for conn, mediaEpoch := range g.conns {
		if mediaEpoch != 0 {
			stale = append(stale, conn)
		}
	}
	g.epoch, g.cap = epoch, hex.EncodeToString(secret)
	g.tokens = make(map[[32]byte]struct{})
	g.tokenOrder = nil
	g.origins = make(map[string]struct{})
	g.originOrder = nil
	g.mediaCtx, g.cancelMedia = context.WithCancel(g.ctx)
	g.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	for _, conn := range stale {
		_ = conn.Close()
	}
	return nil
}

// bindEpoch closes late sockets instead of attaching them to a newer room.
func (g *Gateway) bindEpoch(epoch uint64, conns ...*trackedConn) bool {
	g.mu.Lock()
	valid := !g.closed && epoch != 0 && g.epoch == epoch
	if valid {
		for _, conn := range conns {
			if conn != nil {
				g.conns[conn] = epoch
			}
		}
	}
	g.mu.Unlock()
	if !valid {
		for _, conn := range conns {
			if conn != nil {
				_ = conn.Close()
			}
		}
	}
	return valid
}

func (g *Gateway) SignalURL(epoch uint64, token string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || epoch == 0 || epoch != g.epoch || g.cap == "" || token == "" || len(token) > 16384 {
		return "", ErrGateway
	}
	g.registerTokenLocked(sha256.Sum256([]byte(token)))
	return "ws://" + g.web.Addr().String() + "/" + g.cap, nil
}

func (g *Gateway) registerTokenLocked(hash [32]byte) {
	if _, exists := g.tokens[hash]; !exists {
		if len(g.tokenOrder) == 32 {
			delete(g.tokens, g.tokenOrder[1])
			g.tokenOrder = append(g.tokenOrder[:1], g.tokenOrder[2:]...)
		}
		g.tokenOrder = append(g.tokenOrder, hash)
		g.tokens[hash] = struct{}{}
	}
}

// A refreshed voice token replaces its pinned predecessor. Screen retries
// cannot evict native reconnect authorization or grow the registry forever.
func (g *Gateway) refreshToken(epoch uint64, previous, next string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || epoch == 0 || g.epoch != epoch || next == "" || len(next) > 16384 {
		return ErrGateway
	}
	oldHash, newHash := sha256.Sum256([]byte(previous)), sha256.Sum256([]byte(next))
	if oldHash == newHash {
		g.registerTokenLocked(newHash)
		return nil
	}
	pinned := len(g.tokenOrder) > 0 && g.tokenOrder[0] == oldHash
	g.registerTokenLocked(newHash)
	delete(g.tokens, oldHash)
	g.tokenOrder = slices.DeleteFunc(g.tokenOrder, func(value [32]byte) bool { return value == oldHash || (pinned && value == newHash) })
	if pinned {
		g.tokenOrder = append([][32]byte{newHash}, g.tokenOrder...)
	}
	return nil
}

func (g *Gateway) AllowOrigin(epoch uint64, origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.String() != origin {
		return ErrGateway
	}
	if port, err := strconv.ParseUint(u.Port(), 10, 16); err != nil || port == 0 {
		return ErrGateway
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || epoch == 0 || g.epoch != epoch {
		return ErrGateway
	}
	if _, exists := g.origins[origin]; !exists {
		if len(g.originOrder) == 8 {
			delete(g.origins, g.originOrder[0])
			g.originOrder = g.originOrder[1:]
		}
		g.originOrder = append(g.originOrder, origin)
		g.origins[origin] = struct{}{}
	}
	return nil
}

func (g *Gateway) dialTLS(ctx context.Context, network, address string) (net.Conn, error) {
	u, _ := url.Parse(g.profile.Origin)
	if network != "tcp" || address != net.JoinHostPort(u.Hostname(), "443") {
		return nil, ErrGateway
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(g.ctx, cancel)
	defer stop()
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrGateway
	}
	failed := true
	defer func() {
		if failed {
			<-g.slots
		}
	}()
	raw, err := g.dial(ctx, g.config)
	if err != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, ErrGateway
	}
	secured := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), RootCAs: g.roots, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, ErrGateway
	}
	conn := g.track(secured, func() { <-g.slots })
	if conn == nil {
		return nil, ErrGateway
	}
	failed = false
	return conn, nil
}

type trackedConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.done)
	return err
}

func (g *Gateway) track(conn net.Conn, release func()) *trackedConn {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		_ = conn.Close()
		return nil
	}
	wrapped := &trackedConn{Conn: conn}
	wrapped.done = func() {
		g.mu.Lock()
		delete(g.conns, wrapped)
		g.mu.Unlock()
		if release != nil {
			release()
		}
	}
	g.conns[wrapped] = 0
	return wrapped
}

func (g *Gateway) Close() {
	g.once.Do(func() {
		g.mu.Lock()
		g.closed = true
		connections := make([]*trackedConn, 0, len(g.conns))
		for conn := range g.conns {
			connections = append(connections, conn)
		}
		g.mu.Unlock()
		g.cancel()
		if g.http != nil {
			_ = g.http.Close()
		}
		for _, listener := range []net.Listener{g.web, g.turn} {
			if listener != nil {
				_ = listener.Close()
			}
		}
		for _, conn := range connections {
			_ = conn.Close()
		}
		if g.client != nil {
			g.client.CloseIdleConnections()
		}
		g.wg.Wait()
	})
}

func (g *Gateway) allowedOrigin(origin string) bool {
	if origin == "" || origin == "wails://localhost" || origin == "wails://wails" || origin == "http://wails.localhost" {
		return true
	}
	_, allowed := g.origins[strings.TrimSpace(origin)]
	return allowed && strings.TrimSpace(origin) == origin
}
