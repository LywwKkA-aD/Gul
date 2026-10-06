package screensharelab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJoin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/livekit/token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"url":"ws://127.0.0.1:7880","token":"local-test","identity":"viewer","room":"gul-local"}`))
	}))
	defer server.Close()
	client := NewClient()
	client.endpoint = server.URL + "/api/livekit/token"
	grant, err := client.Join(context.Background(), "viewer")
	if err != nil || grant.Identity != "viewer" || grant.Token != "local-test" {
		t.Fatalf("grant=%+v err=%v", grant, err)
	}
}

func TestJoinRejectsUnsafeResponses(t *testing.T) {
	for _, body := range []string{
		`{"url":"ws://external.example:7880","token":"sensitive","identity":"viewer","room":"gul-local"}`,
		`{"url":"ws://127.0.0.1:7880","token":"","identity":"viewer","room":"gul-local"}`,
		`{"url":"ws://127.0.0.1:7880","token":"sensitive","identity":"other","room":"gul-local"}`,
		`{"url":"ws://127.0.0.1:7880","token":"sensitive","identity":"viewer","room":"other"}`,
		`not json sensitive`,
		strings.Repeat("sensitive", 2000),
	} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			client := NewClient()
			client.endpoint = server.URL
			_, err := client.Join(context.Background(), "viewer")
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestJoinUnavailableAndCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sensitive", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewClient()
	client.endpoint = server.URL
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		_, err := client.Join(ctx, "viewer")
		cancel()
		if err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestJoinDoesNotFollowRedirects(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewClient()
	client.endpoint = server.URL
	_, err := client.Join(context.Background(), "viewer")
	if err == nil || hit {
		t.Fatal("redirect was accepted")
	}
}
