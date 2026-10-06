package livekitlab

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

// A dedicated real TCP listener leaves the interactive broker on port 8787
// untouched. The canonical Host is explicit because the test listener uses an
// ephemeral port. Unlike recorder tests, RemoteAddr comes from the real socket.
func TestGulRealHTTPAuthenticationAndOriginOwnership(t *testing.T) {
	server := httptest.NewServer(testServer(t))
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	do := func(method, path, host, origin, token, body string) (int, []byte, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("real local HTTP request failed")
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 32768))
		if err != nil {
			t.Fatal("real local HTTP response failed")
		}
		return res.StatusCode, data, res.Header
	}
	status, data, headers := do("POST", "/api/gul/login", "127.0.0.1:8787", "http://127.0.0.1:8787", "", `{"username":"real-http","password":""}`)
	if status != 200 || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("login HTTP status = %d", status)
	}
	var login livekitapi.LoginResponse
	if err := json.Unmarshal(data, &login); err != nil || login.SessionToken == "" {
		t.Fatal("invalid real HTTP login")
	}
	if bytes.Contains(data, []byte(strings.Repeat("s", 32))) {
		t.Fatal("signing secret crossed HTTP boundary")
	}
	for _, tt := range []struct {
		name, host, origin, token string
		status                    int
	}{
		{"native", "127.0.0.1:8787", "", login.SessionToken, 200},
		{"own browser", "localhost:8787", "http://localhost:8787", login.SessionToken, 200},
		{"dev browser", "127.0.0.1:8787", "http://127.0.0.1:9245", login.SessionToken, 200},
		{"foreign origin", "127.0.0.1:8787", "https://example.invalid", login.SessionToken, 403},
		{"null origin", "127.0.0.1:8787", "null", login.SessionToken, 403},
		{"wrong host", "example.invalid:8787", "", login.SessionToken, 403},
		{"wrong port", "127.0.0.1:80", "", login.SessionToken, 403},
		{"missing bearer", "127.0.0.1:8787", "", "", 401},
		{"wrong bearer", "127.0.0.1:8787", "", strings.Repeat("a", 43), 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status, body, headers := do("GET", "/api/gul/state", tt.host, tt.origin, tt.token, "")
			if status != tt.status {
				t.Fatalf("HTTP status = %d, want %d", status, tt.status)
			}
			if bytes.Contains(body, []byte(login.SessionToken)) || bytes.Contains(body, []byte(login.Grant.Token)) {
				t.Fatal("state/error response exposed session credentials")
			}
			if status == 200 && tt.origin != "" && headers.Get("Access-Control-Allow-Origin") != tt.origin {
				t.Fatal("allowed CORS origin missing")
			}
		})
	}
	status, _, _ = do("POST", "/api/gul/logout", "127.0.0.1:8787", "", login.SessionToken, "")
	if status != 204 {
		t.Fatalf("logout HTTP status = %d", status)
	}
	status, _, _ = do("GET", "/api/gul/state", "127.0.0.1:8787", "", login.SessionToken, "")
	if status != 401 {
		t.Fatal("logged-out bearer accepted over HTTP")
	}
}
