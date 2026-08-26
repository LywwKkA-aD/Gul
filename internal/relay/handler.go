// Package relay implements the authenticated, fixed-target WebSocket relay
// used by Gul. It carries an opaque Mumble TLS byte stream and never parses
// Mumble credentials, messages, or audio.
package relay

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const (
	// LegacyPath and LegacySubprotocol are the fixed pair every build up to
	// v0.4.0-alpha.2 used. The current pair is derived per server from the
	// credential (relayproto.NamesFor); these stay only while the relay accepts
	// both. Neither is client-selected: the relay answers a fixed set it
	// computed at startup, so it can never become a proxy to somewhere else.
	LegacyPath = relayproto.LegacyPath
	// LegacySubprotocol versions the byte-stream contract independently of the app.
	LegacySubprotocol = relayproto.LegacySubprotocol

	defaultAuthFailuresBeforeBan = 5
	defaultAuthFailureWindow     = time.Minute
	// defaultAuthBanDuration is short on purpose: the ban keys on a source
	// block, so behind a NAT it is shared by everyone in that block.
	defaultAuthBanDuration       = time.Minute
	defaultMaxAuthTrackedSources = 4096
	defaultShutdownDrainTimeout  = 5 * time.Second
	capacityRetryAfter           = 5 * time.Second
)

// Config contains the security and capacity limits for a Handler.
type Config struct {
	ExpectedHost string
	Upstream     string
	// BearerCredentials are the expected credentials, precomputed by
	// `gul-relay derive-credential`. Deriving one costs tens of milliseconds,
	// so it must never happen while a request is being served. At least one
	// v2 credential is required; legacy credentials are honored only while
	// AcceptLegacyBearer is set. None of them is the raw Mumble password.
	BearerCredentials []relayproto.Credential
	// AcceptLegacyNames keeps the fixed /mumble path and gul-mumble-v1
	// subprotocol usable while the clients that predate the derived pair are
	// still out there. Every legacy match is logged at Warn.
	AcceptLegacyNames bool
	// AcceptLegacyBearer keeps the v0.3.0-alpha.2 credential usable for one
	// release. Every legacy match is logged at Warn.
	AcceptLegacyBearer bool
	MaxConnections     int
	// MaxConnectionsPerIP bounds the sessions one source may hold. "IP" is the
	// folded source key, not a single address: IPv4 by /32, IPv6 by /64. See
	// sourceKey.
	MaxConnectionsPerIP     int
	MaxWebSocketMessageSize int64
	DialTimeout             time.Duration
	// SessionIdleTimeout closes a session that transferred nothing in either
	// direction for this long; SessionWriteTimeout bounds a single blocked
	// write. Both default to production values when unset.
	SessionIdleTimeout   time.Duration
	SessionWriteTimeout  time.Duration
	ShutdownDrainTimeout time.Duration

	AuthFailuresBeforeBan int
	AuthFailureWindow     time.Duration
	AuthBanDuration       time.Duration
	MaxAuthTrackedSources int
	// ServerHeader, CoverIndex and CoverNotFound shape the ordinary website
	// the relay shows to everything that is not a tunnel request (cover.go).
	// Empty fields take the stock nginx pages.
	ServerHeader  string
	CoverIndex    string
	CoverNotFound string
	// Logger receives the session and rejection events. nil selects
	// slog.Default().
	Logger *slog.Logger
	// Now is optional and exists so limiter behavior can be tested without
	// wall-clock sleeps. Production callers should leave it nil.
	Now func() time.Time
}

// Handler upgrades authenticated requests and proxies them to one loopback
// upstream. It is safe for concurrent use.
type Handler struct {
	expectedHost string
	upstream     string
	credentials  []relayproto.Credential
	pseudonymKey [sha256.Size]byte
	dialer       net.Dialer
	messageSize  int64
	idleTimeout  time.Duration
	writeTimeout time.Duration
	cover        *coverSite
	// obfuscator scrambles the QUIC datagrams (relayproto.Salamander). Keyed
	// by the primary credential, so both ends reach it from the password.
	obfuscator *relayproto.Obfuscator
	// paths and subprotocols are the names this relay answers on, derived from
	// every configured credential (relayproto.NamesFor) plus, while the window
	// is open, the fixed legacy pair. Precomputed: a request must never spend
	// a key derivation.
	paths        map[string]bool
	subprotocols map[string]bool
	legacyNames  bool
	drainTimeout time.Duration
	global       chan struct{}
	perSourceMax int
	authFailures *authFailureLimiter
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	// upstreamLocalAddress resolves the local address of an upstream dial from
	// a folded source key, or returns nil where the platform cannot route the
	// alias. Holding it in a field keeps the platform decision in one place and
	// lets a test observe the key a session used without a Linux host.
	upstreamLocalAddress func(source string) net.IP

	mu        sync.Mutex
	perSource map[string]int
	active    map[*websocket.Conn]struct{}
	// streams holds the sessions that are not WebSockets - the QUIC ones -
	// which have no close frame to send and are simply closed.
	streams      map[io.Closer]struct{}
	closing      bool
	sessions     sync.WaitGroup
	shutdownOnce sync.Once
}

// NewHandler validates cfg and creates a fixed-target relay.
func NewHandler(cfg Config) (*Handler, error) {
	if strings.TrimSpace(cfg.ExpectedHost) == "" || strings.ContainsAny(cfg.ExpectedHost, "/?#") {
		return nil, errors.New("relay expected host is required")
	}
	if err := validateLoopbackAddress(cfg.Upstream); err != nil {
		return nil, fmt.Errorf("relay upstream: %w", err)
	}
	credentials, primary, err := prepareCredentials(cfg.BearerCredentials, cfg.AcceptLegacyBearer)
	if err != nil {
		return nil, fmt.Errorf("relay bearer credentials: %w", err)
	}
	if cfg.MaxConnections <= 0 {
		return nil, errors.New("relay max connections must be positive")
	}
	if cfg.MaxConnectionsPerIP <= 0 || cfg.MaxConnectionsPerIP > cfg.MaxConnections {
		return nil, errors.New("relay per-source limit must be positive and no larger than the global limit")
	}
	if cfg.MaxWebSocketMessageSize <= 0 {
		return nil, errors.New("relay message size limit must be positive")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.SessionIdleTimeout <= 0 {
		cfg.SessionIdleTimeout = defaultSessionIdleTimeout
	}
	if cfg.SessionWriteTimeout <= 0 {
		cfg.SessionWriteTimeout = defaultSessionWriteTimeout
	}
	if cfg.ShutdownDrainTimeout <= 0 {
		cfg.ShutdownDrainTimeout = defaultShutdownDrainTimeout
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
	h := &Handler{
		expectedHost: strings.ToLower(cfg.ExpectedHost),
		upstream:     cfg.Upstream,
		credentials:  credentials,
		pseudonymKey: sourceAddressKey([]byte(primary)),
		dialer:       net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second},
		messageSize:  cfg.MaxWebSocketMessageSize,
		idleTimeout:  cfg.SessionIdleTimeout,
		writeTimeout: cfg.SessionWriteTimeout,
		cover:        newCoverSite(cfg.ServerHeader, cfg.CoverIndex, cfg.CoverNotFound, time.Time{}),
		obfuscator:   relayproto.NewObfuscator(primary),
		paths:        tunnelPaths(credentials, cfg.AcceptLegacyNames),
		subprotocols: tunnelSubprotocols(credentials, cfg.AcceptLegacyNames),
		legacyNames:  cfg.AcceptLegacyNames,
		drainTimeout: cfg.ShutdownDrainTimeout,
		global:       make(chan struct{}, cfg.MaxConnections),
		perSourceMax: cfg.MaxConnectionsPerIP,
		authFailures: newAuthFailureLimiter(cfg),
		logger:       loggerOrDefault(cfg.Logger),
		perSource:    make(map[string]int),
		active:       make(map[*websocket.Conn]struct{}),
		streams:      make(map[io.Closer]struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}
	h.upstreamLocalAddress = h.pseudonymousUpstreamAddress
	return h, nil
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

// Cover returns the ordinary website this relay presents. The serving binary
// mounts it on every path the tunnel does not own, so one host has exactly one
// personality (cover.go).
func (h *Handler) Cover() http.Handler { return h.cover }

// ServeHTTP answers the tunnel path.
//
// Every refusal here writes the same page, whatever the reason: a method we do
// not take, a host that is not ours, a missing credential, a wrong one. The
// reason belongs in the log, not on the wire - answering each case differently
// hands a prober our decision tree, and one of those answers used to name the
// software outright (cover.go).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.paths[r.URL.Path] {
		h.cover.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		h.cover.NotFound(w, r)
		return
	}
	if !strings.EqualFold(requestHost(r.Host), h.expectedHost) {
		h.cover.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		h.cover.NotFound(w, r)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		// A WebSocket handshake never needs an HTTP request body. Refuse it
		// without reading so slow/chunked bodies cannot occupy the server before
		// authentication. HTTP/1.1 is enforced by the serving binary.
		w.Header().Set("Connection", "close")
		r.Close = true
		h.cover.NotFound(w, r)
		return
	}
	if !sameOriginOrNative(r.Header.Get("Origin"), h.expectedHost) {
		h.cover.NotFound(w, r)
		return
	}
	// sourceIP identifies one client and is only ever logged. Every defense
	// below counts against the block the subscriber controls, so rotating
	// addresses inside one IPv6 /64 cannot reset a ban, a session count, or a
	// Murmur autoban bucket.
	sourceIP := remoteIP(r.RemoteAddr)
	sourceBlock := sourceKey(sourceIP)
	if retryAfter, banned := h.authFailures.banRemaining(sourceBlock); banned {
		h.logger.Debug("relay request rate limited", "source", sourceIP, "retry_after", retryAfter)
		h.cover.decorate(w)
		writeRateLimited(w, r, retryAfter)
		return
	}
	result := h.authorize(r.Header.Get("Authorization"))
	if !result.authorized {
		ban := h.authFailures.recordFailure(sourceBlock)
		// The credential itself is never logged: only whether it was absent,
		// malformed, or a well-formed credential of either generation.
		h.logger.Warn("relay authorization rejected", "source", sourceIP, "credential", result.class)
		if ban.limited {
			if ban.activated {
				h.logger.Warn("relay authorization ban activated", "source", sourceIP, "retry_after", ban.retryAfter)
			} else {
				h.logger.Debug("relay request rate limited", "source", sourceIP, "retry_after", ban.retryAfter)
			}
			h.cover.decorate(w)
			writeRateLimited(w, r, ban.retryAfter)
			return
		}
		// No WWW-Authenticate: it announced the software by name to anyone who
		// asked for the path, and it told a prober the path exists at all.
		h.cover.NotFound(w, r)
		return
	}
	if result.legacy {
		h.logger.Warn("relay accepted legacy bearer credential", "source", sourceIP)
	}
	if retryAfter, banned := h.authFailures.clearIfAllowed(sourceBlock); banned {
		// A concurrent failed request may have activated the ban while this
		// request was validating its credential. Recheck under the limiter lock
		// before accepting it.
		h.cover.decorate(w)
		writeRateLimited(w, r, retryAfter)
		return
	}
	subprotocol, ok := h.matchSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"))
	if !ok {
		h.cover.NotFound(w, r)
		return
	}
	if h.legacyNames && (r.URL.Path == relayproto.LegacyPath || subprotocol == relayproto.LegacySubprotocol) {
		// The pair that names what the tunnel carries. Logged so the operator
		// can see who still has to update before the window is shut.
		h.logger.Warn("relay accepted the legacy tunnel names", "source", sourceIP)
	}

	release, scope, ok := h.acquire(sourceBlock)
	if !ok {
		h.logger.Warn("relay capacity rejected", "source", sourceIP, "scope", scope)
		h.cover.decorate(w)
		writeCapacityRejected(w, capacityRetryAfter)
		return
	}
	defer release()

	h.cover.decorate(w)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{subprotocol},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.logger.Warn("relay websocket upgrade failed", "source", sourceIP, "error", err)
		return
	}
	if !h.registerWebSocket(ws) {
		closeInBackground(ws)
		return
	}
	defer h.unregisterWebSocket(ws)
	stream := websocket.NetConn(h.ctx, ws, websocket.MessageBinary)
	// Order is load-bearing: websocket.NetConn disables the read limit, so the
	// bound has to be reinstated after it, not before. Without it a peer can
	// make the relay buffer a message of any size.
	ws.SetReadLimit(h.messageSize)
	// Closing a WebSocket runs a close handshake that a vanished peer never
	// answers, and the library bounds that wait at seconds. Keep it off the
	// session goroutine: the capacity slot is released when the bytes stop,
	// not when a courtesy close frame finishes timing out.
	defer func() { go func() { _ = stream.Close() }() }()

	h.pumpSession(stream, sourceIP, sourceBlock, "websocket", func() {
		go func() { _ = ws.Close(websocket.StatusInternalError, "Murmur is unavailable") }()
	})
}

// pumpSession is the half of a session that does not depend on how the client
// arrived: dial Murmur, move bytes, account for them. Both transports end
// here, so a WebSocket session and a QUIC one are the same session with a
// different front door.
//
// onUpstreamFailure tells the client that Murmur, not the relay, is the thing
// that is down, in whatever way its transport can say so.
func (h *Handler) pumpSession(
	stream net.Conn,
	sourceIP, sourceBlock, transport string,
	onUpstreamFailure func(),
) {
	dialer := h.dialer
	localAddress := h.upstreamLocalAddress(sourceBlock)
	if localAddress != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: localAddress}
	}
	upstream, err := dialer.DialContext(h.ctx, "tcp", h.upstream)
	if err != nil {
		h.logger.Error("relay upstream dial failed",
			"source", sourceIP, "transport", transport, "upstream", h.upstream, "error", err)
		if onUpstreamFailure != nil {
			onUpstreamFailure()
		}
		return
	}
	defer func() { _ = upstream.Close() }()
	if tcp, ok := upstream.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	opened := time.Now()
	openAttrs := []any{"source", sourceIP, "transport", transport}
	if localAddress != nil {
		openAttrs = append(openAttrs, "upstream_source", localAddress.String())
	}
	h.logger.Info("relay session opened", openAttrs...)

	stats := proxySession(stream, upstream, h.idleTimeout, h.writeTimeout)
	closeAttrs := []any{
		"source", sourceIP,
		"transport", transport,
		"duration", time.Since(opened).Round(time.Millisecond),
		"bytes_from_client", stats.fromClient,
		"bytes_to_client", stats.toClient,
		"reason", stats.reason,
	}
	if stats.err != nil {
		closeAttrs = append(closeAttrs, "error", stats.err)
	}
	h.logger.Info("relay session closed", closeAttrs...)
}

// acquire reserves one session slot for a folded source key. It reports which
// limit refused the request so the rejection can be logged without guessing.
func (h *Handler) acquire(source string) (func(), string, bool) {
	select {
	case h.global <- struct{}{}:
	default:
		return nil, "global", false
	}

	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		<-h.global
		return nil, "shutdown", false
	}
	if h.perSource[source] >= h.perSourceMax {
		h.mu.Unlock()
		<-h.global
		return nil, "source", false
	}
	h.perSource[source]++
	h.sessions.Add(1)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.perSource[source]--
			if h.perSource[source] == 0 {
				delete(h.perSource, source)
			}
			h.mu.Unlock()
			<-h.global
			h.sessions.Done()
		})
	}, "", true
}

// closeInBackground runs a WebSocket close off the calling goroutine. The
// close handshake waits for a reply the peer may never send, and the library
// gives no way to cut that short, so no request-serving path may block on it.
func closeInBackground(conn *websocket.Conn) {
	go func() { _ = conn.CloseNow() }()
}

// Shutdown stops accepting sessions, asks every active session to close, and
// waits up to the drain window before cutting the remainder off. Sending a
// close frame first lets a client distinguish a planned restart from a
// network failure and reconnect without a reconnect backoff penalty.
//
// A session whose peer ignores the close frame ends when the drain window
// expires and the shared stream context is cancelled, which is what releases
// its capacity slot. The close handshake itself may keep running in the
// background for the few seconds the library allows it.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.shutdownOnce.Do(func() {
		for _, conn := range h.stopAccepting() {
			// Close waits for the peer's close frame, so each session gets its
			// own goroutine and none delays the others.
			go func(conn *websocket.Conn) {
				_ = conn.Close(websocket.StatusGoingAway, "relay is shutting down")
			}(conn)
		}
	})

	done := make(chan struct{})
	go func() {
		h.sessions.Wait()
		close(done)
	}()

	drain := time.NewTimer(h.drainTimeout)
	defer drain.Stop()
	select {
	case <-done:
		return nil
	case <-drain.C:
	case <-ctx.Done():
	}

	// Sessions that did not answer the close frame are cut off.
	h.cancel()
	h.mu.Lock()
	remaining := make([]*websocket.Conn, 0, len(h.active))
	for conn := range h.active {
		remaining = append(remaining, conn)
	}
	h.mu.Unlock()
	for _, conn := range remaining {
		// CloseNow waits for the connection's own goroutines, so one wedged
		// session must not delay the rest. Cancelling the shared context above
		// is what actually ends the sessions; this only hurries them along.
		go func(conn *websocket.Conn) { _ = conn.CloseNow() }(conn)
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) stopAccepting() []*websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closing = true
	active := make([]*websocket.Conn, 0, len(h.active))
	for conn := range h.active {
		active = append(active, conn)
	}
	// A QUIC stream has no close frame to send, so it is simply closed here
	// rather than being asked to leave first.
	for stream := range h.streams {
		go func(stream io.Closer) { _ = stream.Close() }(stream)
	}
	return active
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

// registerStream tracks a session that is not a WebSocket, so a shutdown can
// end it too. It reports false once the relay has stopped accepting.
func (h *Handler) registerStream(stream io.Closer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	h.streams[stream] = struct{}{}
	return true
}

func (h *Handler) unregisterStream(stream io.Closer) {
	h.mu.Lock()
	delete(h.streams, stream)
	h.mu.Unlock()
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

// tunnelPaths and tunnelSubprotocols precompute the names this relay answers
// on: one pair per configured credential, plus the legacy pair while the
// window is open. Derivation is one HMAC, but it belongs at startup - a
// request must never spend one.
func tunnelPaths(credentials []relayproto.Credential, legacy bool) map[string]bool {
	paths := make(map[string]bool, len(credentials)+1)
	for _, credential := range credentials {
		paths[relayproto.NamesFor(credential).Path] = true
	}
	if legacy {
		paths[relayproto.LegacyPath] = true
	}
	return paths
}

func tunnelSubprotocols(credentials []relayproto.Credential, legacy bool) map[string]bool {
	names := make(map[string]bool, len(credentials)+1)
	for _, credential := range credentials {
		names[relayproto.NamesFor(credential).Subprotocol] = true
	}
	if legacy {
		names[relayproto.LegacySubprotocol] = true
	}
	return names
}

// TunnelPaths reports the addresses this relay answers on, so the serving
// binary can mount the handler on each of them.
func (h *Handler) TunnelPaths() []string {
	paths := make([]string, 0, len(h.paths))
	for path := range h.paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// matchSubprotocol returns the offered subprotocol this relay accepts. The
// answer has to be echoed back to the client, so it is the matched name and
// not a fixed one.
func (h *Handler) matchSubprotocol(values []string) (string, bool) {
	for _, value := range values {
		for _, offered := range strings.Split(value, ",") {
			offered = strings.TrimSpace(offered)
			if h.subprotocols[offered] {
				return offered, true
			}
		}
	}
	return "", false
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
