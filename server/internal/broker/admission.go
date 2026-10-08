package broker

import (
	"bufio"
	"context"
	"crypto/subtle"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type signalFlow struct {
	session   *gulSession
	channelID uint32
	cancel    context.CancelFunc
	done      chan struct{}
}

// ReverseProxy cancellation closes its backend, but an upstream EOF can leave
// the frontend read half open while the proxy waits for the other copy loop.
// Closing the owned hijacked frontend makes cancellation terminate both loops.
type signalResponseWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w signalResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w signalResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffered, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		context.AfterFunc(w.ctx, func() { _ = conn.Close() })
	}
	return conn, buffered, err
}

type admissionClaims struct {
	jwt.RegisteredClaims
	Attributes map[string]string `json:"attributes"`
	Video      struct {
		Room     string `json:"room"`
		RoomJoin bool   `json:"roomJoin"`
	} `json:"video"`
}

func mediaToken(r *http.Request) (string, bool) {
	if len(r.URL.RawQuery) > 16384 {
		return "", false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false
	}
	values := query["access_token"]
	header := r.Header.Get("Authorization")
	if len(values) == 1 && header == "" && len(values[0]) <= 8192 && values[0] != "" {
		return values[0], true
	}
	if len(values) == 0 && strings.HasPrefix(header, "Bearer ") && len(header) <= 8200 {
		return strings.TrimPrefix(header, "Bearer "), true
	}
	return "", false
}
func decimalAttribute(value string, bits int) (uint64, bool) {
	n, err := strconv.ParseUint(value, 10, bits)
	return n, err == nil && strconv.FormatUint(n, 10) == value
}
func (b *gulBroker) admitLocked(token string) *gulSession {
	claims := &admissionClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (any, error) { return []byte(b.cfg.APISecret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(b.cfg.APIKey), jwt.WithExpirationRequired(), jwt.WithTimeFunc(b.now))
	if err != nil || !parsed.Valid || claims.Attributes == nil || !claims.Video.RoomJoin {
		return nil
	}
	attributes := claims.Attributes
	id, idOK := decimalAttribute(attributes["sessionId"], 31)
	channel, channelOK := decimalAttribute(attributes["channelId"], 31)
	revision, revisionOK := decimalAttribute(attributes["revision"], 53)
	authVersion, authOK := decimalAttribute(attributes["authVersion"], 53)
	role := attributes["role"]
	if !idOK || id == 0 || !channelOK || !revisionOK || !authOK || (role != "voice" && role != "screen") {
		return nil
	}
	if claims.Subject != role+"."+strconv.FormatUint(id, 10) || attributes["ownerIdentity"] != voiceIdentity(uint32(id)) || claims.Video.Room != "gul-channel-"+strconv.FormatUint(channel, 10) {
		return nil
	}
	s := b.store.Snapshot()
	if attributes["serverId"] != s.ServerID {
		return nil
	}
	for _, session := range b.sessions {
		if session.ID == uint32(id) && session.ChannelID == uint32(channel) && session.Revision == revision && session.AuthVersion == authVersion && session.MemberID == attributes["memberId"] && subtle.ConstantTimeCompare([]byte(session.Nonce), []byte(attributes["sessionNonce"])) == 1 && !session.Revoked && b.now().Before(session.ExpiresAt) && b.sessionAccessLocked(session) {
			return session
		}
	}
	return nil
}
func (b *gulBroker) registerAdmission(mux *http.ServeMux, cfg PublicConfig) {
	target, _ := url.Parse(cfg.LiveKitInternalURL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 8 * time.Second
	transport.MaxIdleConnsPerHost = 16
	var upstreamTransport http.RoundTripper = transport
	if b.signalTransport != nil {
		upstreamTransport = b.signalTransport
	}
	proxy := &httputil.ReverseProxy{Transport: upstreamTransport, ErrorLog: log.New(io.Discard, "", 0), Rewrite: func(req *httputil.ProxyRequest) {
		req.SetURL(target)
		req.Out.Host = target.Host
		req.Out.Header.Del("Cookie")
		req.Out.Header.Del("X-Forwarded-For")
		req.Out.Header.Del("X-Forwarded-Proto")
		req.Out.Header.Del("Forwarded")
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { gulCode(w, 503, "media_unavailable") }}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			gulCode(w, 405, "method_not_allowed")
			return
		}
		token, ok := mediaToken(r)
		if !ok {
			gulCode(w, 401, "media_token_required")
			return
		}
		b.mu.Lock()
		b.expireLocked(b.now())
		session := b.admitLocked(token)
		if session == nil {
			b.mu.Unlock()
			gulCode(w, 403, "access_denied")
			return
		}
		count := 0
		for flow := range b.flows {
			if flow.session == session {
				count++
			}
		}
		if count >= 8 || len(b.flows) >= 128 {
			b.mu.Unlock()
			gulCode(w, 429, "media_limit")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		flow := &signalFlow{session: session, channelID: session.ChannelID, cancel: cancel, done: make(chan struct{})}
		b.flows[flow] = struct{}{}
		b.mu.Unlock()
		defer func() { cancel(); b.mu.Lock(); delete(b.flows, flow); close(flow.done); b.mu.Unlock() }()
		// Track before the upstream request, including its pre-upgrade join.
		// Cancelling closes the backend WS; completion precedes SFU removal.
		proxy.ServeHTTP(signalResponseWriter{ResponseWriter: w, ctx: ctx}, r.WithContext(ctx))
	}
	for _, path := range []string{"/rtc", "/rtc/validate", "/rtc/v1", "/rtc/v1/validate"} {
		mux.HandleFunc(path, handler)
	}
}
func (b *gulBroker) cancelFlowsLocked(session *gulSession) {
	for flow := range b.flows {
		if flow.session == session {
			flow.cancel()
		}
	}
}
func (b *gulBroker) cleanSessionMedia(ctx context.Context, session *gulSession) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b.mu.Lock()
	var wait []<-chan struct{}
	for flow := range b.flows {
		if flow.session == session {
			flow.cancel()
			wait = append(wait, flow.done)
		}
	}
	id, channel := session.ID, session.ChannelID
	b.mu.Unlock()
	for _, done := range wait {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.removeMedia(ctx, id, channel)
}
