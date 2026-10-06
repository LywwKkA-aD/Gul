// Package livekitlab provides an explicitly local screen-sharing experiment.
// It is not an authentication service suitable for public deployment.
package livekitlab

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"
)

const (
	ListenAddress = "127.0.0.1:8787"
	ServerURL     = "ws://127.0.0.1:7880"
	RoomName      = "gul-local"
	tokenLifetime = 5 * time.Minute
)

// TokenResponse contains only participant credentials, never the signing key.
type TokenResponse struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Identity string `json:"identity"`
	Room     string `json:"room"`
}

// NewHandler serves the local token endpoint and an existing frontend build.
func NewHandler(cfg Config, assets fs.FS) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","service":"gul-livekit-lab"}`)
	})
	mux.HandleFunc("/api/livekit/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST, OPTIONS")
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		var input struct {
			Identity string `json:"identity"`
			Room     string `json:"room"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if input.Room == "" {
			input.Room = RoomName
		}
		if !validIdentity(input.Identity) || input.Room != RoomName {
			http.Error(w, "valid identity and gul-local room required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenResponse{
			URL:      ServerURL,
			Token:    signedToken(cfg, input.Identity, time.Now()),
			Identity: input.Identity,
			Room:     RoomName,
		})
	})
	newGulBroker(cfg, time.Now).register(mux)
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return localOnly(mux), nil
}

func validIdentity(identity string) bool {
	if len(identity) == 0 || len(identity) > 64 {
		return false
	}
	for _, r := range identity {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_.", r) {
			return false
		}
	}
	return true
}

func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		remote := net.ParseIP(host)
		if err != nil || remote == nil || !remote.IsLoopback() || !allowedHost(r.Host) {
			http.Error(w, "local access required", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !allowedOrigin(origin) {
			http.Error(w, "local origin required", http.StatusForbidden)
			return
		}
		// A local native client has no Origin. A foreign website must not get
		// that exemption even if its browser omits Origin on a navigation.
		if origin == "" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "local origin required", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

func allowedHost(host string) bool {
	return host == "127.0.0.1:8787" || host == "localhost:8787"
}

func allowedOrigin(origin string) bool {
	return origin == "http://127.0.0.1:8787" || origin == "http://localhost:8787" || origin == "http://127.0.0.1:9245"
}

func signedToken(cfg Config, identity string, now time.Time) string {
	// The primitive values below cannot produce json.Marshal errors.
	claims, _ := json.Marshal(map[string]any{
		"iss": cfg.APIKey, "sub": identity,
		"iat": now.Unix(), "nbf": now.Add(-10 * time.Second).Unix(), "exp": now.Add(tokenLifetime).Unix(),
		"video": map[string]any{
			"roomJoin": true, "room": RoomName,
			"canPublish": true, "canSubscribe": true, "canPublishData": false,
			"canPublishSources": []string{"screen_share", "screen_share_audio"},
		},
	})
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(cfg.APISecret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
