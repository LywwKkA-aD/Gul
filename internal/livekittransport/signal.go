package livekittransport

import (
	"context"
	"crypto/sha256"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func (g *Gateway) authorize(r *http.Request) (string, string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.Method != http.MethodGet || len(r.URL.RawQuery) > 32768 || r.URL.RawPath != "" ||
		len(query["access_token"]) > 1 || len(r.Header.Values("Authorization")) > 1 || len(r.Header.Values("Origin")) > 1 {
		return "", "", false
	}
	token := query.Get("access_token")
	if auth := r.Header.Get("Authorization"); auth != "" {
		value, found := strings.CutPrefix(auth, "Bearer ")
		if !found || (token != "" && token != value) {
			return "", "", false
		}
		token = value
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.cap == "" || r.Host != g.web.Addr().String() || !g.allowedOrigin(r.Header.Get("Origin")) {
		return "", "", false
	}
	path, found := strings.CutPrefix(r.URL.Path, "/"+g.cap)
	if !found || (path != "/rtc" && path != "/rtc/validate" && path != "/rtc/v1" && path != "/rtc/v1/validate") {
		return "", "", false
	}
	if _, found := g.tokens[sha256.Sum256([]byte(token))]; !found {
		return "", "", false
	}
	return path, token, true
}

// work starts under the same lock as Close, so no handler can add work after
// shutdown starts waiting for all hijacked sockets and TURN streams.
func (g *Gateway) work() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

func (g *Gateway) serveSignal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path, token, ok := g.authorize(r)
	if !ok || !g.work() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	defer g.wg.Done()
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	g.mu.Lock()
	epoch, mediaCtx := g.epoch, g.mediaCtx
	current := r.URL.Path == "/"+g.cap+path
	g.mu.Unlock()
	if !current || mediaCtx == nil {
		http.Error(w, "stale session", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(mediaCtx, cancel)
	defer stop()
	r = r.WithContext(ctx)
	query := r.URL.Query()
	query.Del("access_token")
	upstream := g.profile.Origin + path + "?" + query.Encode()
	header := http.Header{"Authorization": {"Bearer " + token}}
	if strings.HasSuffix(path, "/validate") {
		g.validate(w, r, upstream, header)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "upgrade required", http.StatusBadRequest)
		return
	}
	dialer := &websocket.Dialer{NetDialContext: rejectPlainDial, NetDialTLSContext: g.dialTLS, HandshakeTimeout: 15 * time.Second}
	remote, response, err := dialer.DialContext(r.Context(), "wss"+strings.TrimPrefix(upstream, "https"), header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		http.Error(w, "transport unavailable", http.StatusBadGateway)
		return
	}
	defer remote.Close()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, Error: func(w http.ResponseWriter, _ *http.Request, status int, _ error) {
		http.Error(w, "upgrade rejected", status)
	}}
	local, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	tracked := g.track(local.UnderlyingConn(), nil)
	if tracked == nil {
		_ = local.Close()
		return
	}
	defer tracked.Close()
	upstreamConn, ok := remote.UnderlyingConn().(*trackedConn)
	if !ok || !g.bindEpoch(epoch, tracked, upstreamConn) {
		return
	}
	local.SetReadLimit(1 << 20)
	remote.SetReadLimit(1 << 20)
	_ = local.UnderlyingConn().SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go pipeSignal(remote, local, nil, done)
	go pipeSignal(local, remote, func(kind int, data []byte) ([]byte, error) {
		return g.rewriteSignal(epoch, kind, data, &token)
	}, done)
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}

func pipeSignal(dst, src *websocket.Conn, rewrite func(int, []byte) ([]byte, error), done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	for {
		kind, data, err := src.ReadMessage()
		if err != nil {
			return
		}
		if rewrite != nil {
			data, err = rewrite(kind, data)
			if err != nil {
				return
			}
		}
		_ = dst.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if dst.WriteMessage(kind, data) != nil {
			return
		}
	}
}

func (g *Gateway) validate(w http.ResponseWriter, r *http.Request, upstream string, header http.Header) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		http.Error(w, "transport unavailable", http.StatusBadGateway)
		return
	}
	req.Header = header
	// RoundTrip cannot follow redirects or send the bearer to another host.
	res, err := g.client.RoundTrip(req)
	if err != nil {
		http.Error(w, "transport unavailable", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		http.Error(w, "validation failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 4096))
}

func (g *Gateway) upstreamAddress() string {
	u, _ := url.Parse(g.profile.Origin)
	return net.JoinHostPort(u.Hostname(), "443")
}
