package screenbridge

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

var assetPath = regexp.MustCompile(`^/assets/[A-Za-z0-9_.-]+$`)

func (b *Bridge) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=()")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; media-src blob:; connect-src 'self' wss: ws://127.0.0.1:*; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	// Exact authority prevents DNS rebinding into this loopback-only service.
	if r.Host != b.host || r.URL.RawQuery != "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		b.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/screen.html" && !assetPath.MatchString(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	info, err := fs.Stat(b.assets, name)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.FileServerFS(b.assets).ServeHTTP(w, r)
}

func (b *Bridge) api(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Path == "/api/screen/state"
	if (!state && r.Method != http.MethodPost) || (state && r.Method != http.MethodGet) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != b.origin && (!state || origin != "") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer || len(token) != 64 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/api/screen/open" {
		b.exchange(w, token)
		return
	}
	b.mu.Lock()
	session := b.active
	valid := session != nil && sameSecret(token, session.token)
	b.mu.Unlock()
	if !valid {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !matches(b.provider.Status(), session.epoch, session.channel) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	switch r.URL.Path {
	case "/api/screen/state":
		writeJSON(w, map[string]any{"epoch": session.epoch, "channelId": session.channel})
	case "/api/screen/grant":
		if authorizer, ok := b.provider.(interface{ AllowScreenOrigin(uint64, string) error }); ok {
			if authorizer.AllowScreenOrigin(session.epoch, b.origin) != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		grant, err := b.provider.ScreenGrant(r.Context(), session.epoch, session.channel)
		b.mu.Lock()
		current := b.active == session
		b.mu.Unlock()
		if !current || !matches(b.provider.Status(), session.epoch, session.channel) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(w, grant)
	case "/api/screen/close":
		b.mu.Lock()
		if b.active == session {
			b.active = nil
		}
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (b *Bridge) exchange(w http.ResponseWriter, code string) {
	token, err := secret()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	b.mu.Lock()
	session := b.active
	if session == nil || !sameSecret(code, session.code) || !time.Now().Before(session.expires) {
		b.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !matches(b.provider.Status(), session.epoch, session.channel) {
		b.mu.Unlock()
		w.WriteHeader(http.StatusConflict)
		return
	}
	// Never keep a reusable capability in browser history or a cookie.
	session.code = ""
	session.token = token
	b.mu.Unlock()
	writeJSON(w, map[string]any{"token": token, "epoch": session.epoch, "channelId": session.channel, "serverOrigin": session.server})
}

func sameSecret(a, b string) bool {
	return len(a) == 64 && len(b) == 64 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
