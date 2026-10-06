package livekitlab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGulRequiredFieldsAndEncoding(t *testing.T) {
	h := gulHandler(time.Now)
	login := gulLogin(t, h, "valid")
	for _, tt := range []struct{ path, body string }{
		{"/api/gul/login", `null`},
		{"/api/gul/login", `[]`},
		{"/api/gul/login", `{"username":"` + string([]byte{0xff}) + `"}`},
		{"/api/gul/channel", `{}`},
		{"/api/gul/channel", `{"channelId":null}`},
		{"/api/gul/audio", `{}`},
		{"/api/gul/audio", `{"muted":false}`},
		{"/api/gul/screen", `{"channelId":1}`},
		{"/api/gul/screen", `{"revision":1}`},
		{"/api/gul/logout", `{"unrecognized":true}`},
	} {
		t.Run(tt.path+tt.body, func(t *testing.T) {
			if w := gulRequest(h, "POST", tt.path, login.SessionToken, tt.body); w.Code != 400 {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	if w := gulRequest(h, "GET", "/api/gul/state", strings.Repeat("a", 43), ""); w.Code != 401 {
		t.Fatal("unknown well-formed session token accepted")
	}
	if w := gulRequest(h, "POST", "/api/gul/logout", login.SessionToken, `{}`); w.Code != 204 {
		t.Fatalf("empty logout object status = %d", w.Code)
	}
}

func TestGulLoopbackMiddlewareAndPreflight(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*http.Request)
		code int
	}{
		{"host", func(r *http.Request) { r.Host = "attacker.invalid:8787" }, 403},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, 403},
		{"remote", func(r *http.Request) { r.RemoteAddr = "192.168.1.5:2000" }, 403},
		{"content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"malformed content type", func(r *http.Request) { r.Header.Set("Content-Type", "invalid;") }, 415},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/gul/login", strings.NewReader(`{"username":"test"}`))
			r.RemoteAddr = "127.0.0.1:2000"
			r.Header.Set("Content-Type", "application/json")
			tt.edit(r)
			w := httptest.NewRecorder()
			testServer(t).ServeHTTP(w, r)
			if w.Code != tt.code {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	w := gulRequest(gulHandler(time.Now), "OPTIONS", "/api/gul/state", "", "")
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Fatal("invalid authenticated endpoint preflight")
	}
}

func TestGulIDCollisionAndCapacity(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := newGulBroker(Config{APIKey: "test-key", APISecret: strings.Repeat("s", 32)}, func() time.Time { return now })
	ids := []uint32{0, 0xffffffff, 7, 7, 8}
	index := 0
	b.newID = func() uint32 { id := ids[index]; index++; return id }
	mux := http.NewServeMux()
	b.register(mux)
	h := localOnly(mux)
	first := gulLogin(t, h, "one")
	second := gulLogin(t, h, "two")
	if first.SessionID != 7 || second.SessionID != 8 {
		t.Fatal("invalid or duplicate session ID accepted")
	}
	for i := 0; i < gulMaxSessions; i++ {
		b.sessions[[32]byte{byte(i)}] = &gulSession{ID: uint32(i + 100), ExpiresAt: now.Add(gulSessionLease)}
	}
	if w := gulRequest(h, "POST", "/api/gul/login", "", `{"username":"full"}`); w.Code != 429 {
		t.Fatalf("capacity response = %d", w.Code)
	}
	now = now.Add(gulSessionLease)
	b.newID = func() uint32 { return 1 }
	if gulLogin(t, h, "recovered").SessionID != 1 {
		t.Fatal("expired sessions did not release capacity")
	}
}
