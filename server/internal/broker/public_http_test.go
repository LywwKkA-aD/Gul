package broker

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
)

func TestPublicBrokerRealHTTPAuthenticationAndOrigin(t *testing.T) {
	h, err := NewPublicHandler(publicTestConfig(), &fakeParticipantRemover{})
	if err != nil {
		t.Fatal("cannot configure public broker")
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	do := func(method, path, origin, token, body string) (int, []byte, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal("cannot create broker request")
		}
		req.Host = "voice.example.test"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "192.0.2.4")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("public broker request failed")
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(res.Body, 32768))
		if err != nil {
			t.Fatal("public broker response failed")
		}
		return res.StatusCode, data, res.Header
	}
	body, _ := json.Marshal(api.LoginRequest{Username: "desktop", Password: publicTestPassword})
	status, data, headers := do("POST", "/api/gul/login", "https://voice.example.test", "", string(body))
	var login api.LoginResponse
	if status != 200 || json.Unmarshal(data, &login) != nil || login.SessionToken == "" || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("public HTTP login failed")
	}
	if bytes.Contains(data, []byte(strings.Repeat("s", 32))) || bytes.Contains(data, []byte(publicTestPassword)) {
		t.Fatal("private signing material crossed HTTP boundary")
	}
	for _, tt := range []struct {
		origin, token string
		status        int
	}{
		{"", login.SessionToken, 200},
		{"https://voice.example.test", login.SessionToken, 200},
		{"gul://app", login.SessionToken, 403},
		{"https://foreign.invalid", login.SessionToken, 403},
		{"", "", 401},
		{"", strings.Repeat("a", 43), 401},
	} {
		status, data, _ := do("GET", "/api/gul/state", tt.origin, tt.token, "")
		if status != tt.status {
			t.Fatalf("state status = %d, want %d", status, tt.status)
		}
		if bytes.Contains(data, []byte(login.SessionToken)) || bytes.Contains(data, []byte(login.Grant.Token)) {
			t.Fatal("state exposed bearer credentials")
		}
	}
	status, _, _ = do("POST", "/api/gul/logout", "", login.SessionToken, "")
	if status != 204 {
		t.Fatal("public HTTP logout failed")
	}
	status, _, _ = do("GET", "/api/gul/state", "", login.SessionToken, "")
	if status != 401 {
		t.Fatal("logged-out bearer remained valid")
	}
}
