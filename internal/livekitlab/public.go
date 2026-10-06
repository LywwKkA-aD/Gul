package livekitlab

import (
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// PublicHandler exposes the authenticated Gul API behind a trusted loopback
// TLS reverse proxy. It deliberately has no lab tokens or static file routes.
type PublicHandler struct {
	handler http.Handler
	broker  *gulBroker
	limits  *publicLoginLimits
}

func NewPublicHandler(cfg PublicConfig, remover ParticipantRemover) (*PublicHandler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if remover == nil {
		remover = newLiveKitRemover(cfg)
	}
	b := newGulBroker(Config{APIKey: cfg.APIKey, APISecret: cfg.APISecret}, time.Now)
	b.serverURL, b.grantLifetime, b.maxSessions = cfg.LiveKitURL, 90*time.Second, 32
	var hash [32]byte
	decoded, _ := hex.DecodeString(cfg.JoinPasswordSHA256)
	copy(hash[:], decoded)
	b.passwordHash, b.remover = &hash, remover
	h := &PublicHandler{broker: b, limits: newPublicLoginLimits()}
	mux := http.NewServeMux()
	b.register(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !gulMethod(w, r, http.MethodGet) {
			return
		}
		gulWrite(w, http.StatusOK, map[string]string{"status": "ok", "service": "gul-livekit-server"})
	})
	origin, _ := url.Parse(cfg.PublicOrigin)
	h.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		clientIP, allowed := trustedProxyRequest(r, origin.Host, cfg.PublicOrigin)
		if !allowed {
			http.Error(w, "trusted HTTPS proxy required", http.StatusForbidden)
			return
		}
		if r.Header.Get("Origin") != "" {
			w.Header().Set("Access-Control-Allow-Origin", cfg.PublicOrigin)
			w.Header().Set("Vary", "Origin")
		}
		if r.URL.Path == "/api/gul/login" && r.Method == http.MethodPost && !h.limits.allow(clientIP) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "login rate limit reached", http.StatusTooManyRequests)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return h, nil
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

func trustedProxyRequest(r *http.Request, host, origin string) (netip.Addr, bool) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	peer, parseErr := netip.ParseAddr(ip)
	if err != nil || parseErr != nil || !peer.IsLoopback() || r.Host != host {
		return netip.Addr{}, false
	}
	if len(r.Header.Values("X-Forwarded-Proto")) != 1 || r.Header.Get("X-Forwarded-Proto") != "https" || len(r.Header.Values("X-Forwarded-For")) != 1 {
		return netip.Addr{}, false
	}
	client, err := netip.ParseAddr(r.Header.Get("X-Forwarded-For"))
	if err != nil || client.Zone() != "" || len(r.Header.Values("Origin")) > 1 || len(r.Header.Values("Authorization")) > 1 {
		return netip.Addr{}, false
	}
	if got := r.Header.Get("Origin"); got != "" && got != origin {
		return netip.Addr{}, false
	}
	if r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return netip.Addr{}, false
	}
	return client.Unmap(), true
}
