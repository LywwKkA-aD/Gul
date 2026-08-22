// Package relay implements the authenticated, fixed-target WebSocket relay
// used by Gul. It carries an opaque Mumble TLS byte stream and never parses
// Mumble credentials, messages, or audio.
package relay

import (
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const (
	// Path is deliberately fixed so the service cannot become a
	// client-selected TCP proxy.
	Path = relayproto.Path
	// Subprotocol versions the byte-stream contract independently of the app.
	Subprotocol = relayproto.Subprotocol

	defaultAuthFailuresBeforeBan = 5
	defaultAuthFailureWindow     = time.Minute
	defaultAuthBanDuration       = 5 * time.Minute
	defaultMaxAuthTrackedSources = 4096
)

// Config contains the security and capacity limits for a Handler.
type Config struct {
	ExpectedHost string
	Upstream     string
	// BearerCredential is the unpadded base64url HMAC credential derived by
	// `gul-relay derive-credential`. It must not be the raw Mumble password.
	BearerCredential        []byte
	MaxConnections          int
	MaxConnectionsPerIP     int
	MaxWebSocketMessageSize int64
	DialTimeout             time.Duration
	AuthFailuresBeforeBan   int
	AuthFailureWindow       time.Duration
	AuthBanDuration         time.Duration
	MaxAuthTrackedSources   int
	// Now is optional and exists so limiter behavior can be tested without
	// wall-clock sleeps. Production callers should leave it nil.
	Now func() time.Time
}

// Handler upgrades authenticated requests and proxies them to one loopback
// upstream. It is safe for concurrent use.
type Handler struct {
	expectedHost string
	upstream     string
	authHash     [sha256.Size]byte
	sourceKey    [sha256.Size]byte
	dialer       net.Dialer
	messageSize  int64
	global       chan struct{}
	perIPMax     int
	authFailures *authFailureLimiter
	ctx          context.Context
	cancel       context.CancelFunc

	mu           sync.Mutex
	perIP        map[string]int
	active       map[*websocket.Conn]struct{}
	closing      bool
	sessions     sync.WaitGroup
	shutdownOnce sync.Once
}

type authFailureLimiter struct {
	mu                sync.Mutex
	entries           map[string]*authFailureEntry
	order             *list.List
	failuresBeforeBan int
	failureWindow     time.Duration
	banDuration       time.Duration
	maxEntries        int
	now               func() time.Time
}

type authFailureEntry struct {
	source        string
	failures      int
	windowStarted time.Time
	bannedUntil   time.Time
	element       *list.Element
}

// NewHandler validates cfg and creates a fixed-target relay.
func NewHandler(cfg Config) (*Handler, error) {
	if strings.TrimSpace(cfg.ExpectedHost) == "" || strings.ContainsAny(cfg.ExpectedHost, "/?#") {
		return nil, errors.New("relay expected host is required")
	}
	if err := validateLoopbackAddress(cfg.Upstream); err != nil {
		return nil, fmt.Errorf("relay upstream: %w", err)
	}
	authHash, err := bearerCredentialHash(cfg.BearerCredential)
	if err != nil {
		return nil, fmt.Errorf("relay bearer credential: %w", err)
	}
	if cfg.MaxConnections <= 0 {
		return nil, errors.New("relay max connections must be positive")
	}
	if cfg.MaxConnectionsPerIP <= 0 || cfg.MaxConnectionsPerIP > cfg.MaxConnections {
		return nil, errors.New("relay per-IP limit must be positive and no larger than the global limit")
	}
	if cfg.MaxWebSocketMessageSize <= 0 {
		return nil, errors.New("relay message size limit must be positive")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.AuthFailuresBeforeBan < 0 {
		return nil, errors.New("relay authorization failure limit cannot be negative")
	}
	if cfg.AuthFailureWindow < 0 {
		return nil, errors.New("relay authorization failure window cannot be negative")
	}
	if cfg.AuthBanDuration < 0 {
		return nil, errors.New("relay authorization ban duration cannot be negative")
	}
	if cfg.MaxAuthTrackedSources < 0 {
		return nil, errors.New("relay authorization source limit cannot be negative")
	}
	if cfg.AuthFailuresBeforeBan == 0 {
		cfg.AuthFailuresBeforeBan = defaultAuthFailuresBeforeBan
	}
	if cfg.AuthFailureWindow == 0 {
		cfg.AuthFailureWindow = defaultAuthFailureWindow
	}
	if cfg.AuthBanDuration == 0 {
		cfg.AuthBanDuration = defaultAuthBanDuration
	}
	if cfg.MaxAuthTrackedSources == 0 {
		cfg.MaxAuthTrackedSources = defaultMaxAuthTrackedSources
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{
		expectedHost: strings.ToLower(cfg.ExpectedHost),
		upstream:     cfg.Upstream,
		authHash:     authHash,
		sourceKey:    sourceAddressKey(cfg.BearerCredential),
		dialer:       net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second},
		messageSize:  cfg.MaxWebSocketMessageSize,
		global:       make(chan struct{}, cfg.MaxConnections),
		perIPMax:     cfg.MaxConnectionsPerIP,
		authFailures: newAuthFailureLimiter(cfg),
		perIP:        make(map[string]int),
		active:       make(map[*websocket.Conn]struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path {
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not accepted", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(requestHost(r.Host), h.expectedHost) {
		http.Error(w, http.StatusText(http.StatusMisdirectedRequest), http.StatusMisdirectedRequest)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		// A WebSocket handshake never needs an HTTP request body. Refuse it
		// without reading so slow/chunked bodies cannot occupy the server before
		// authentication. HTTP/1.1 is enforced by the serving binary.
		w.Header().Set("Connection", "close")
		r.Close = true
		http.Error(w, "request body is not accepted", http.StatusBadRequest)
		return
	}
	if !sameOriginOrNative(r.Header.Get("Origin"), h.expectedHost) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	sourceIP := remoteIP(r.RemoteAddr)
	if retryAfter, banned := h.authFailures.banRemaining(sourceIP); banned {
		writeRateLimited(w, r, retryAfter)
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		retryAfter, limited := h.authFailures.recordFailure(sourceIP)
		w.Header().Set("Cache-Control", "no-store")
		if limited {
			writeRateLimited(w, r, retryAfter)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="gul-relay"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	if retryAfter, banned := h.authFailures.clearIfAllowed(sourceIP); banned {
		// A concurrent failed request may have activated the ban while this
		// request was validating its credential. Recheck under the limiter lock
		// before accepting it.
		writeRateLimited(w, r, retryAfter)
		return
	}
	if !offersSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"), Subprotocol) {
		http.Error(w, "required WebSocket subprotocol is missing", http.StatusBadRequest)
		return
	}

	release, ok := h.acquire(sourceIP)
	if !ok {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	defer release()

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{Subprotocol},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	if !h.registerWebSocket(ws) {
		_ = ws.CloseNow()
		return
	}
	defer h.unregisterWebSocket(ws)
	stream := websocket.NetConn(h.ctx, ws, websocket.MessageBinary)
	ws.SetReadLimit(h.messageSize)
	defer func() { _ = stream.Close() }()

	dialer := h.dialer
	// Linux routes the full 127/8 block locally. Binding a stable alias gives
	// Murmur a distinct autoban bucket per outer source IP. Darwin only assigns
	// 127.0.0.1 by default, so local cross-platform tests use the OS default.
	if runtime.GOOS == "linux" {
		dialer.LocalAddr = &net.TCPAddr{IP: pseudonymousLoopback(h.sourceKey, sourceIP)}
	}
	upstream, err := dialer.DialContext(h.ctx, "tcp", h.upstream)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "Murmur is unavailable")
		return
	}
	defer func() { _ = upstream.Close() }()
	if tcp, ok := upstream.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	proxy(stream, upstream)
}

func (h *Handler) authorized(header string) bool {
	scheme, credential, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validBearerCredential([]byte(credential)) {
		return false
	}
	provided := sha256.Sum256([]byte(credential))
	return subtle.ConstantTimeCompare(provided[:], h.authHash[:]) == 1
}

func bearerCredentialHash(credential []byte) ([sha256.Size]byte, error) {
	if !validBearerCredential(credential) {
		return [sha256.Size]byte{}, errors.New("must be an unpadded base64url-encoded 32-byte value")
	}
	return sha256.Sum256(credential), nil
}

func validBearerCredential(credential []byte) bool {
	if len(credential) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
		return false
	}
	var decoded [sha256.Size]byte
	n, err := base64.RawURLEncoding.Strict().Decode(decoded[:], credential)
	return err == nil && n == sha256.Size
}

func newAuthFailureLimiter(cfg Config) *authFailureLimiter {
	return &authFailureLimiter{
		entries:           make(map[string]*authFailureEntry),
		order:             list.New(),
		failuresBeforeBan: cfg.AuthFailuresBeforeBan,
		failureWindow:     cfg.AuthFailureWindow,
		banDuration:       cfg.AuthBanDuration,
		maxEntries:        cfg.MaxAuthTrackedSources,
		now:               cfg.Now,
	}
}

// recordFailure returns a positive ban duration after a source consumes its
// configured allowance of 401 responses. The triggering request and all later
// failed requests receive 429 until the fixed ban expires.
func (l *authFailureLimiter) recordFailure(source string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[source]
	if !ok {
		entry = l.insert(source, now)
	} else {
		l.order.MoveToBack(entry.element)
	}
	if now.Before(entry.bannedUntil) {
		return entry.bannedUntil.Sub(now), true
	}
	if !entry.bannedUntil.IsZero() || !now.Before(entry.windowStarted.Add(l.failureWindow)) {
		entry.failures = 0
		entry.windowStarted = now
		entry.bannedUntil = time.Time{}
	}

	entry.failures++
	if entry.failures > l.failuresBeforeBan {
		entry.bannedUntil = now.Add(l.banDuration)
		return l.banDuration, true
	}
	return 0, false
}

func (l *authFailureLimiter) banRemaining(source string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[source]
	if !ok {
		return 0, false
	}
	if now.Before(entry.bannedUntil) {
		l.order.MoveToBack(entry.element)
		return entry.bannedUntil.Sub(now), true
	}
	if !entry.bannedUntil.IsZero() {
		delete(l.entries, source)
		l.order.Remove(entry.element)
	}
	return 0, false
}

func (l *authFailureLimiter) clearIfAllowed(source string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[source]
	if !ok {
		return 0, false
	}
	if now.Before(entry.bannedUntil) {
		l.order.MoveToBack(entry.element)
		return entry.bannedUntil.Sub(now), true
	}
	delete(l.entries, source)
	l.order.Remove(entry.element)
	return 0, false
}

func (l *authFailureLimiter) insert(source string, now time.Time) *authFailureEntry {
	if len(l.entries) >= l.maxEntries {
		oldestElement := l.order.Front()
		oldest := oldestElement.Value.(*authFailureEntry)
		delete(l.entries, oldest.source)
		l.order.Remove(oldestElement)
	}
	entry := &authFailureEntry{source: source, windowStarted: now}
	entry.element = l.order.PushBack(entry)
	l.entries[source] = entry
	return entry
}

func retryAfterSeconds(duration time.Duration) string {
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(int64(seconds), 10)
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	r.Close = true
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
}

func (h *Handler) acquire(ip string) (func(), bool) {
	select {
	case h.global <- struct{}{}:
	default:
		return nil, false
	}

	h.mu.Lock()
	if h.closing || h.perIP[ip] >= h.perIPMax {
		h.mu.Unlock()
		<-h.global
		return nil, false
	}
	h.perIP[ip]++
	h.sessions.Add(1)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.perIP[ip]--
			if h.perIP[ip] == 0 {
				delete(h.perIP, ip)
			}
			h.mu.Unlock()
			<-h.global
			h.sessions.Done()
		})
	}, true
}

// Shutdown stops accepting sessions, cancels every active WebSocket stream,
// and waits for both proxy directions to return.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.shutdownOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		active := make([]*websocket.Conn, 0, len(h.active))
		for conn := range h.active {
			active = append(active, conn)
		}
		h.mu.Unlock()
		h.cancel()
		for _, conn := range active {
			_ = conn.CloseNow()
		}
	})
	done := make(chan struct{})
	go func() {
		h.sessions.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) registerWebSocket(conn *websocket.Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	h.active[conn] = struct{}{}
	return true
}

func (h *Handler) unregisterWebSocket(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.active, conn)
	h.mu.Unlock()
}

func proxy(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyOneWay := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go copyOneWay(left, right)
	go copyOneWay(right, left)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("must be a host:port address")
	}
	if port == "" {
		return errors.New("port is required")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("host must be loopback")
	}
	return nil
}

func offersSubprotocol(values []string, want string) bool {
	for _, value := range values {
		for _, offered := range strings.Split(value, ",") {
			if strings.TrimSpace(offered) == want {
				return true
			}
		}
	}
	return false
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func requestHost(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(value)
}

func sameOriginOrNative(origin, expectedHost string) bool {
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), expectedHost)
}

func sourceAddressKey(token []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write([]byte("gul-relay-v1 source-address"))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}

func pseudonymousLoopback(key [sha256.Size]byte, sourceIP string) net.IP {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(sourceIP))
	sum := mac.Sum(nil)
	result := net.IPv4(127, sum[0], sum[1], sum[2])
	if result.Equal(net.IPv4(127, 0, 0, 1)) {
		result = net.IPv4(127, 0, 0, 2)
	}
	return result
}
