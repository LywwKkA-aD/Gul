package livekitlab

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{APIKey: "test-key", APISecret: strings.Repeat("s", 32)}, fstest.MapFS{
		"index.html": {Data: []byte("<html>lab</html>")},
		"asset.js":   {Data: []byte("test")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(handler http.Handler, method, body string, configure func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8787/api/livekit/token", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if configure != nil {
		configure(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestTokenUsesScopedSignedClaims(t *testing.T) {
	w := request(testServer(t), http.MethodPost, `{"identity":"тест-1","room":"gul-local"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var response TokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.URL != "ws://127.0.0.1:7880" || response.Identity != "тест-1" || response.Room != "gul-local" {
		t.Fatalf("bad response metadata: %+v", response)
	}
	parts := strings.Split(response.Token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "test-key" || claims["sub"] != "тест-1" {
		t.Fatal("wrong issuer or identity")
	}
	expires := int64(claims["exp"].(float64))
	if expires < time.Now().Unix()+290 || expires > time.Now().Unix()+301 {
		t.Fatal("token must expire in five minutes")
	}
	video := claims["video"].(map[string]any)
	if video["room"] != "gul-local" || video["roomJoin"] != true || video["canSubscribe"] != true || video["canPublish"] != true || video["canPublishData"] != false {
		t.Fatalf("bad grants: %v", video)
	}
	sources := video["canPublishSources"].([]any)
	if len(sources) != 2 || sources[0] != "screen_share" || sources[1] != "screen_share_audio" {
		t.Fatalf("bad sources: %v", sources)
	}
	if _, ok := video["roomAdmin"]; ok {
		t.Fatal("unexpected room admin grant")
	}
	if strings.Contains(w.Body.String(), strings.Repeat("s", 32)) {
		t.Fatal("secret leaked")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response may be cached")
	}
}

func TestTokenRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name, method, body string
		configure          func(*http.Request)
		status             int
	}{
		{"get", "GET", "", nil, 405},
		{"bad origin", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"null origin", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"rebound host", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Host = "evil.example:8787" }, 403},
		{"wrong port", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Host = "127.0.0.1:8080" }, 403},
		{"nonlocal remote", "POST", `{"identity":"user"}`, func(r *http.Request) { r.RemoteAddr = "192.168.1.1:5000" }, 403},
		{"bad remote", "POST", `{"identity":"user"}`, func(r *http.Request) { r.RemoteAddr = "invalid" }, 403},
		{"cross site", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"form", "POST", `{"identity":"user"}`, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"invalid json", "POST", `{`, nil, 400},
		{"unknown field", "POST", `{"identity":"user","secret":"x"}`, nil, 400},
		{"multiple objects", "POST", `{"identity":"user"}{}`, nil, 400},
		{"empty identity", "POST", `{"identity":""}`, nil, 400},
		{"markup identity", "POST", `{"identity":"<script>"}`, nil, 400},
		{"long identity", "POST", `{"identity":"` + strings.Repeat("a", 65) + `"}`, nil, 400},
		{"wrong room", "POST", `{"identity":"user","room":"other"}`, nil, 400},
		{"oversized body", "POST", strings.Repeat(" ", 2049) + `{}`, nil, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := request(testServer(t), tt.method, tt.body, tt.configure)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
		})
	}
}

func TestAllowedOriginsAndDefaultRoom(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:8787", "http://localhost:8787", "http://127.0.0.1:9245"} {
		t.Run(origin, func(t *testing.T) {
			w := request(testServer(t), "POST", `{"identity":"test_user.1"}`, func(r *http.Request) { r.Header.Set("Origin", origin) })
			if w.Code != 200 {
				t.Fatalf("status = %d", w.Code)
			}
			if origin != "" && w.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("missing allowed origin")
			}
			var response TokenResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Room != "gul-local" {
				t.Fatal("wrong default room")
			}
		})
	}
}

func TestPreflight(t *testing.T) {
	w := request(testServer(t), "OPTIONS", "", func(r *http.Request) {
		r.Header.Set("Origin", "http://127.0.0.1:9245")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "content-type")
	})
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("bad preflight: %d %v", w.Code, w.Header())
	}
}

func TestHealthAndStaticFiles(t *testing.T) {
	for _, tt := range []struct {
		path   string
		status int
		body   string
	}{
		{"/healthz", 200, `{"status":"ok","service":"gul-livekit-lab"}`},
		{"/", 200, "<html>lab</html>"},
		{"/asset.js", 200, "test"},
		{"/missing", 404, "404 page not found"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			w := request(testServer(t), "GET", "", func(r *http.Request) { r.URL.Path = tt.path })
			if w.Code != tt.status || strings.TrimSpace(w.Body.String()) != tt.body {
				t.Fatalf("status %d, body %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, cfg := range []Config{{}, {APIKey: "key", APISecret: "short"}, {APIKey: "bad key", APISecret: strings.Repeat("x", 32)}} {
		if _, err := NewHandler(cfg, fstest.MapFS{}); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}
