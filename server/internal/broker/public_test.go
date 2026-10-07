package broker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
)

const publicTestPassword = "test-only-password-123456789"

func publicTestConfig() PublicConfig {
	hash := sha256.Sum256([]byte(publicTestPassword))
	return PublicConfig{
		ListenAddress: "127.0.0.1:8787", PublicOrigin: "https://voice.example.test",
		LiveKitURL: "wss://voice.example.test", LiveKitInternalURL: "http://127.0.0.1:7880",
		APIKey: "test-key", APISecret: strings.Repeat("s", 32), JoinPasswordSHA256: hex.EncodeToString(hash[:]),
	}
}

type removal struct{ room, identity string }
type fakeParticipantRemover struct {
	mu    sync.Mutex
	calls []removal
	fail  bool
}

func (f *fakeParticipantRemover) RemoveParticipant(_ context.Context, room, identity string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, removal{room, identity})
	if f.fail {
		return errors.New("private upstream diagnostic must not escape")
	}
	return nil
}

func publicRequest(h http.Handler, method, path, token, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://voice.example.test"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func publicLogin(t *testing.T, h http.Handler) api.LoginResponse {
	t.Helper()
	body, _ := json.Marshal(api.LoginRequest{Username: "Friend", Password: publicTestPassword})
	return gulResponse[api.LoginResponse](t, publicRequest(h, "POST", "/api/gul/login", "", string(body), nil))
}

func TestPublicBrokerAuthenticationAndBoundary(t *testing.T) {
	h, err := NewPublicHandler(publicTestConfig(), &fakeParticipantRemover{})
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"", "wrong", publicTestPassword + " ", strings.Repeat("x", 257)} {
		body, _ := json.Marshal(api.LoginRequest{Username: "Friend", Password: password})
		w := publicRequest(h, "POST", "/api/gul/login", "", string(body), nil)
		if w.Code != 401 || strings.Contains(w.Body.String(), password) && password != "" {
			t.Fatal("invalid public password handling")
		}
	}
	login := publicLogin(t, h)
	if login.Grant.URL != "wss://voice.example.test" {
		t.Fatal("public grant used a local URL")
	}
	claims := publicClaims(t, login.Grant.Token)
	if claims["exp"].(float64)-claims["iat"].(float64) != 90 {
		t.Fatal("public initial grant lifetime must be 90 seconds")
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.5:1234" },
		func(r *http.Request) { r.Host = "evil.example" },
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		func(r *http.Request) { r.Header.Set("Origin", "null") },
		func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "http") },
		func(r *http.Request) { r.Header.Del("X-Forwarded-For") },
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.1, 192.0.2.2") },
		func(r *http.Request) { r.Header.Add("X-Forwarded-For", "192.0.2.2") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		if w := publicRequest(h, "GET", "/healthz", "", "", mutate); w.Code != 403 {
			t.Fatalf("unsafe proxy boundary accepted: %d", w.Code)
		}
	}
	for _, path := range []string{"/api/livekit/token", "/", "/api/gul/unknown"} {
		if w := publicRequest(h, "GET", path, "", "", nil); w.Code != 404 {
			t.Fatal("public handler exposed lab/static route")
		}
	}
	if w := publicRequest(h, "GET", "/api/gul/state", "", "", nil); w.Code != 401 {
		t.Fatal("unauthenticated roster exposed")
	}
	w := publicRequest(h, "GET", "/healthz", "", "", func(r *http.Request) { r.Header.Set("Origin", "https://voice.example.test") })
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://voice.example.test" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("public health/CORS headers invalid")
	}
}

func publicClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid token shape")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal("invalid token claims")
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		t.Fatal("invalid token JSON")
	}
	return claims
}

func TestPublicChannelLogoutAndFailedRemoval(t *testing.T) {
	remover := &fakeParticipantRemover{}
	h, err := NewPublicHandler(publicTestConfig(), remover)
	if err != nil {
		t.Fatal(err)
	}
	login := publicLogin(t, h)
	if w := publicRequest(h, "POST", "/api/gul/channel", login.SessionToken, `{"channelId":1}`, nil); w.Code != 200 || len(remover.calls) != 0 {
		t.Fatal("idempotent channel removed participants")
	}
	moved := gulResponse[api.LoginResponse](t, publicRequest(h, "POST", "/api/gul/channel", login.SessionToken, `{"channelId":2}`, nil))
	if moved.ChannelID != 2 || moved.Revision != 2 || len(remover.calls) != 2 {
		t.Fatal("channel transition did not clean old room")
	}
	if remover.calls[0] != (removal{"gul-channel-1", login.Identity}) || remover.calls[1] != (removal{"gul-channel-1", strings.Replace(login.Identity, "voice.", "screen.", 1)}) {
		t.Fatal("removed wrong participant or room")
	}
	remover.fail = true
	w := publicRequest(h, "POST", "/api/gul/channel", login.SessionToken, `{"channelId":3}`, nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("failed removal was ignored or leaked diagnostic")
	}
	state := gulResponse[api.State](t, publicRequest(h, "GET", "/api/gul/state", login.SessionToken, "", nil))
	if state.SelfChannel != 2 || state.Revision != 2 {
		t.Fatal("failed transition changed committed channel")
	}
	if w := publicRequest(h, "POST", "/api/gul/logout", login.SessionToken, "", nil); w.Code != 503 {
		t.Fatal("failed logout cleanup not reported")
	}
	if w := publicRequest(h, "GET", "/api/gul/state", login.SessionToken, "", nil); w.Code != 401 {
		t.Fatal("logout failed to revoke broker bearer")
	}
	remover.fail = false
	h.cleanupExpired(context.Background())
	if len(h.broker.sessions) != 0 {
		t.Fatal("revoked session not cleaned after retry")
	}
}

func TestPublicRateLimitAndLeaseCleanup(t *testing.T) {
	remover := &fakeParticipantRemover{}
	h, err := NewPublicHandler(publicTestConfig(), remover)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	h.broker.now = func() time.Time { return now }
	h.limits.now = h.broker.now
	login := publicLogin(t, h)
	for i := 1; i < 20; i++ {
		if w := publicRequest(h, "POST", "/api/gul/login", "", `{}`, nil); w.Code != 401 {
			t.Fatal("unexpected pre-limit login status")
		}
	}
	if w := publicRequest(h, "POST", "/api/gul/login", "", `{}`, nil); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("login rate limit missing")
	}
	now = now.Add(61 * time.Second)
	h.cleanupExpired(context.Background())
	if len(remover.calls) != 2 || len(h.broker.sessions) != 0 {
		t.Fatal("expired media/session was not removed")
	}
	if w := publicRequest(h, "GET", "/api/gul/state", login.SessionToken, "", nil); w.Code != 401 {
		t.Fatal("expired bearer accepted")
	}
	_ = publicLogin(t, h)
}
