package screenbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

type fakeProvider struct {
	mu     sync.Mutex
	status domain.ConnectionStatus
	grants int
	hook   func()
}

func (p *fakeProvider) Status() domain.ConnectionStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}
func (p *fakeProvider) ScreenGrant(_ context.Context, epoch uint64, channel uint32) (domain.ScreenGrant, error) {
	p.mu.Lock()
	p.grants++
	hook := p.hook
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	return domain.ScreenGrant{URL: "wss://example.com", Token: "private-grant", Epoch: epoch, ChannelID: channel}, nil
}
func (p *fakeProvider) change() { p.mu.Lock(); p.status.Epoch++; p.mu.Unlock() }

func setup(t *testing.T) (*Bridge, *fakeProvider, *url.URL) {
	t.Helper()
	p := &fakeProvider{status: domain.ConnectionStatus{State: domain.StateConnected, Epoch: 7, SelfChannel: 2, Server: "https://example.com"}}
	var opened string
	b := New(p, fstest.MapFS{"screen.html": {Data: []byte("screen app")}, "assets/app.js": {Data: []byte("safe asset")}, "index.html": {Data: []byte("native app")}}, func(u string) error { opened = u; return nil })
	t.Cleanup(func() { _ = b.Close() })
	if err := b.Open(context.Background(), 7, 2); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(opened)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" || u.Path != "/screen.html" || len(u.Fragment) != 64 {
		t.Fatal("unsafe launch URL")
	}
	return b, p, u
}

func call(t *testing.T, u *url.URL, method, path, token, origin, host string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, u.Scheme+"://"+u.Host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if host != "" {
		req.Host = host
	}
	client := http.Client{Timeout: time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing private response cache policy")
	}
	return res.StatusCode, data
}

func exchange(t *testing.T, u *url.URL) string {
	t.Helper()
	status, data := call(t, u, "POST", "/api/screen/open", u.Fragment, u.Scheme+"://"+u.Host, "")
	var result struct {
		Token        string
		Epoch        uint64
		ChannelID    uint32
		ServerOrigin string
	}
	if status != 200 || json.Unmarshal(data, &result) != nil || len(result.Token) != 64 || result.Epoch != 7 || result.ChannelID != 2 || result.ServerOrigin != "https://example.com" {
		t.Fatal("invalid exchange")
	}
	return result.Token
}

func TestCompanionOneUseHandoffAndSessionFencing(t *testing.T) {
	_, p, u := setup(t)
	token := exchange(t, u)
	origin := u.Scheme + "://" + u.Host
	if code, _ := call(t, u, "POST", "/api/screen/open", u.Fragment, origin, ""); code != 401 {
		t.Fatal("handoff reused")
	}
	if code, _ := call(t, u, "POST", "/api/screen/grant", token, origin, ""); code != 200 {
		t.Fatal("grant denied")
	}
	if code, _ := call(t, u, "GET", "/api/screen/state", token, "", ""); code != 200 {
		t.Fatal("same-origin state denied")
	}
	p.change()
	if code, data := call(t, u, "POST", "/api/screen/grant", token, origin, ""); code != 409 || strings.Contains(string(data), "private-grant") {
		t.Fatal("stale grant leaked")
	}
	if p.grants != 1 {
		t.Fatal("stale session reached provider")
	}
}

func TestCompanionRejectsCrossOriginAndRebinding(t *testing.T) {
	_, _, u := setup(t)
	origin := u.Scheme + "://" + u.Host
	for _, tc := range []struct{ origin, host, token string }{{"https://evil.example", "", u.Fragment}, {"null", "", u.Fragment}, {"", "", u.Fragment}, {origin, "evil.example", u.Fragment}, {origin, "", strings.Repeat("a", 64)}} {
		if code, data := call(t, u, "POST", "/api/screen/open", tc.token, tc.origin, tc.host); code == 200 || strings.Contains(string(data), "private-grant") {
			t.Fatal("untrusted caller accepted")
		}
	}
	_ = exchange(t, u)
}

func TestCompanionRechecksAfterGrantAndRevokesOnClose(t *testing.T) {
	_, p, u := setup(t)
	token := exchange(t, u)
	origin := u.Scheme + "://" + u.Host
	p.mu.Lock()
	p.hook = p.change
	p.mu.Unlock()
	if code, data := call(t, u, "POST", "/api/screen/grant", token, origin, ""); code != 409 || strings.Contains(string(data), "private-grant") {
		t.Fatal("late grant escaped")
	}
	_, _, u2 := setup(t)
	token2 := exchange(t, u2)
	origin2 := u2.Scheme + "://" + u2.Host
	if code, _ := call(t, u2, "POST", "/api/screen/close", token2, origin2, ""); code != 204 {
		t.Fatal("close failed")
	}
	if code, _ := call(t, u2, "GET", "/api/screen/state", token2, "", ""); code != 401 {
		t.Fatal("closed token survived")
	}
}

func TestCompanionAssetsAndLaunchFailures(t *testing.T) {
	b, p, u := setup(t)
	for _, path := range []string{"/screen.html", "/assets/app.js"} {
		if code, _ := call(t, u, "GET", path, "", "", ""); code != 200 {
			t.Fatal("asset missing")
		}
	}
	for _, path := range []string{"/", "/index.html", "/assets/", "/unknown", "/api/screen/grant"} {
		if code, _ := call(t, u, "GET", path, "", "", ""); code == 200 {
			t.Fatal("unexpected resource exposed")
		}
	}
	p.change()
	if b.Open(context.Background(), 7, 2) == nil {
		t.Fatal("stale open accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.Open(ctx, 8, 2) == nil {
		t.Fatal("cancelled open accepted")
	}
	bad := New(p, fstest.MapFS{}, func(string) error { return errors.New("private OS details") })
	defer bad.Close()
	if err := bad.Open(context.Background(), 8, 2); err == nil || strings.Contains(err.Error(), "private OS") {
		t.Fatal("unsafe launch error")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Open(context.Background(), 8, 2) == nil {
		t.Fatal("closed bridge reopened")
	}
}
