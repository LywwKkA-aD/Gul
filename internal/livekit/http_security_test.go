package livekit

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokerRejectsUntrustedTLSWithoutSendingCredentials(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	b := newBroker(server.URL)
	defer b.close()
	if _, err := b.login(context.Background(), "alice", "fixture-password"); err != ErrBroker || reached.Load() {
		t.Fatal("untrusted certificate allowed credentials to cross TLS boundary")
	}
}

func TestSDKMediaRejectsUntrustedTLSWithoutSendingCredentials(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	grant := fixtureLogin(1).Grant
	grant.URL = "wss" + server.URL[len("https"):]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	media, err := dialMedia(ctx, grant, mediaHooks{})
	if media != nil {
		media.close()
	}
	if err != ErrMedia || reached.Load() {
		t.Fatal("untrusted certificate allowed SDK credentials to cross TLS boundary")
	}
}

func TestSDKRedirectGuardPreservesUnrelatedClientBehavior(t *testing.T) {
	originalError := errors.New("fixture redirect policy")
	called := false
	original := &http.Client{Timeout: 5 * time.Second, Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
		called = true
		return originalError
	}}
	guarded := guardSDKHTTPClient(original)
	if guarded.Timeout != original.Timeout || guarded.Transport != original.Transport {
		t.Fatal("default HTTP client settings changed")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/unrelated", nil)
	if err := guarded.CheckRedirect(request, []*http.Request{request}); !errors.Is(err, originalError) || !called {
		t.Fatal("unrelated redirect policy was replaced")
	}
	plain := guardSDKHTTPClient(&http.Client{})
	ten := make([]*http.Request, 10)
	for i := range ten {
		ten[i] = request
	}
	if plain.CheckRedirect(request, []*http.Request{request}) != nil || plain.CheckRedirect(request, ten) == nil {
		t.Fatal("standard redirect limit changed")
	}
}

func TestSDKValidateDoesNotFollowAuthenticatedRedirect(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	request, err := http.NewRequest(http.MethodGet, source.URL+"/rtc/validate?fixture=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("SDK validation redirect guard failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || reached.Load() {
		t.Fatal("SDK validation followed a redirect carrying credentials")
	}
}
