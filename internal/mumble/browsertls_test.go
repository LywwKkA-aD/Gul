package mumble

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// isGREASE reports whether a value is one of the deliberately invalid ones
// browsers scatter through a ClientHello (RFC 8701). Their whole point is that
// nothing should mind seeing them, so a client that sends none has told you it
// is not a browser before it has sent a single byte of the request.
func isGREASE(value uint16) bool {
	return byte(value>>8) == byte(value) && value&0x0f == 0x0a
}

func anyGREASE(values []uint16) bool {
	for _, value := range values {
		if isGREASE(value) {
			return true
		}
	}
	return false
}

// captureHello starts a TLS server that records the ClientHello it is offered.
func captureHello(t *testing.T) (*httptest.Server, func() *tls.ClientHelloInfo) {
	t.Helper()
	var captured atomic.Pointer[tls.ClientHelloInfo]
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			clone := *hello
			captured.Store(&clone)
			return nil, nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, captured.Load
}

// The outer handshake has to be a browser's. Go's own is unmistakable - its
// cipher list, its extension order, and no GREASE at all - which makes one
// JA3 lookup enough to say "a Go program on 443".
func TestOuterHandshakeLooksLikeABrowser(t *testing.T) {
	t.Parallel()
	server, hello := captureHello(t)

	response, err := browserClient(server.Client(), false).Get(server.URL)
	if err != nil {
		t.Fatalf("get through the browser client: %v", err)
	}
	_ = response.Body.Close()

	got := hello()
	if got == nil {
		t.Fatal("the server saw no ClientHello")
	}
	if !anyGREASE(got.CipherSuites) {
		t.Errorf("no GREASE cipher suite in %v; this is not a browser handshake", got.CipherSuites)
	}
	if !anyGREASE(got.SupportedVersions) {
		t.Errorf("no GREASE version in %v", got.SupportedVersions)
	}
	if len(got.SupportedCurves) == 0 || !anyGREASE(uint16sOf(got.SupportedCurves)) {
		t.Errorf("no GREASE curve in %v", got.SupportedCurves)
	}
}

// The counterpart, so the check above is known to discriminate rather than to
// pass for everyone: the standard library sends no GREASE, which is exactly
// what this replaces.
func TestPlainGoHandshakeHasNoGREASE(t *testing.T) {
	t.Parallel()
	server, hello := captureHello(t)

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("get through the plain client: %v", err)
	}
	_ = response.Body.Close()

	got := hello()
	if got == nil {
		t.Fatal("the server saw no ClientHello")
	}
	if anyGREASE(got.CipherSuites) {
		t.Fatalf("crypto/tls sent GREASE %v; the discriminator above is worthless", got.CipherSuites)
	}
}

// A caller that installed its own roots or its own route keeps them: the
// handshake is replaced, not the connection.
func TestBrowserClientKeepsTheCallersTransport(t *testing.T) {
	t.Parallel()
	server, _ := captureHello(t)

	// server.Client() trusts only the test certificate, so reaching it at all
	// proves the roots survived.
	if response, err := browserClient(server.Client(), false).Get(server.URL); err != nil {
		t.Fatalf("the caller's roots were dropped: %v", err)
	} else {
		_ = response.Body.Close()
	}

	// And a client that trusts nothing of the sort must still fail.
	if response, err := browserClient(&http.Client{}, false).Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("verification was skipped")
	}
}

// What a middlebox that terminates TLS reads must not say "Go program".
func TestHandshakeRequestCarriesBrowserHeaders(t *testing.T) {
	t.Parallel()
	header := make(http.Header)
	applyBrowserHeaders(header, "https://murmur.example.test")

	if got := header.Get("User-Agent"); got == "" || got[:len("Mozilla/")] != "Mozilla/" {
		t.Errorf("User-Agent = %q, want a browser one", got)
	}
	for _, key := range []string{"Origin", "Accept-Language", "Cache-Control", "Pragma"} {
		if header.Get(key) == "" {
			t.Errorf("no %s; a browser sends one", key)
		}
	}
}

// The Origin has to match the authority the request is addressed to, port and
// all: the WebSocket library on the other side compares them and refuses a
// mismatch, exactly as it would for a browser.
func TestRelayOriginMatchesTheAuthority(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"wss://murmur.example.test":      "https://murmur.example.test",
		"wss://murmur.example.test:8443": "https://murmur.example.test:8443",
		"wss://[2001:db8::1]:8443":       "https://[2001:db8::1]:8443",
		"://not a url":                   "",
	}
	for address, want := range cases {
		if got := relayOrigin(address); got != want {
			t.Errorf("relayOrigin(%q) = %q, want %q", address, got, want)
		}
	}
}

func uint16sOf(curves []tls.CurveID) []uint16 {
	out := make([]uint16, len(curves))
	for i, curve := range curves {
		out[i] = uint16(curve)
	}
	return out
}
