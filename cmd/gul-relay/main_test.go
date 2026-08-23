package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestPreAuthorizationConnectionLimitsLeaveBoundedHandshakeHeadroom(t *testing.T) {
	if maxPreAuthConnections != 256 {
		t.Fatalf("pre-authorization connection limit = %d, want 256", maxPreAuthConnections)
	}
	if maxPreAuthConnections <= maxRelayConnections {
		t.Fatalf("pre-authorization limit %d must exceed active relay limit %d", maxPreAuthConnections, maxRelayConnections)
	}
	// One source must not be able to reach the global bound on its own: that
	// is what took the endpoint offline.
	if maxPreAuthConnections/maxPreAuthPerSource < 8 {
		t.Fatalf("one source may hold 1/%d of the global limit, want at most an eighth", maxPreAuthConnections/maxPreAuthPerSource)
	}
	// An established session holds its accepted connection, so a source at its
	// session quota must still have room to open a handshake.
	if maxPreAuthPerSource <= maxSessionsPerIP {
		t.Fatalf("per-source connection limit %d leaves no handshake headroom above %d sessions", maxPreAuthPerSource, maxSessionsPerIP)
	}

	// The headroom is only bounded if an admitted connection cannot sit on its
	// slot: a source at maxPreAuthPerSource idle connections must lose them
	// within seconds. Pinned on the wired server, not on the constants, so a
	// longer timeout cannot return through the constructor.
	server := relayServer("127.0.0.1:0", http.NewServeMux(), nil, slog.New(slog.DiscardHandler))
	if server.IdleTimeout != 5*time.Second {
		t.Fatalf("idle timeout = %s, want 5s", server.IdleTimeout)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("read header timeout = %s, want 5s", server.ReadHeaderTimeout)
	}
	if server.MaxHeaderBytes != 16<<10 {
		t.Fatalf("max header bytes = %d, want %d", server.MaxHeaderBytes, 16<<10)
	}
}

func TestRelayConfigUsesTheSharedMessageBound(t *testing.T) {
	credentials := []relayproto.Credential{relayproto.Credential(currentVector)}
	cfg := relayConfig(options{expectedHost: "murmur.example.test", acceptLegacyBearer: true}, credentials, slog.New(slog.DiscardHandler))

	if cfg.MaxWebSocketMessageSize != relayproto.MaxMessageBytes {
		t.Fatalf("message bound = %d, want %d", cfg.MaxWebSocketMessageSize, relayproto.MaxMessageBytes)
	}
	if !cfg.AcceptLegacyBearer {
		t.Fatal("legacy bearer acceptance was not passed through")
	}
	if cfg.AuthBanDuration != time.Minute {
		t.Fatalf("ban duration = %s, want 1m", cfg.AuthBanDuration)
	}
	if cfg.Logger == nil {
		t.Fatal("handler was built without a logger")
	}
}

// TestListenRelayAcceptsIPv6 pins the dual-stack listener: "tcp4" excluded the
// IPv6-only networks the relay exists to serve.
func TestListenRelayAcceptsIPv6(t *testing.T) {
	if !strings.HasPrefix(defaultListenAddress, ":") {
		t.Fatalf("default listen address %q names an address family", defaultListenAddress)
	}
	listener, err := listenRelay("[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial IPv6 loopback: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("IPv6 connection was not accepted")
	}
}

func TestParseLogLevel(t *testing.T) {
	for value, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo,
		"WARN":  slog.LevelWarn,
		"error": slog.LevelError,
	} {
		got, err := parseLogLevel(value)
		if err != nil || got != want {
			t.Fatalf("parseLogLevel(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := parseLogLevel("trace"); err == nil {
		t.Fatal("unknown log level was accepted")
	}
}

// TestServeBoundsPreAuthenticationConnectionsPerSource is the daemon-level form
// of the outage: unauthenticated connections from one source are capped, the
// surplus is dropped, and the listener keeps serving instead of suspending
// Accept at the cap.
//
// Only that half is provable here. Every connection a test can make to a
// loopback listener shares one /32, so the concurrent-service half - a flood
// from one source does not starve another - is covered at unit level by
// TestSourceLimitedListenerBoundsOneSourceWithoutStarvingOthers, which offers
// connections from distinct sources.
func TestServeBoundsPreAuthenticationConnectionsPerSource(t *testing.T) {
	opts, roots := testDaemonOptions(t)
	listener, err := listenRelay("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, opts, slog.New(slog.DiscardHandler), listener) }()

	if err := probeHealthz(address, opts.expectedHost, roots); err != nil {
		t.Fatalf("health probe: %v", err)
	}

	// The surplus is what has to be rejected. The health probe above may not
	// have given its slot back yet, so the assertions are about the surplus and
	// the remaining headroom rather than about one exact connection.
	const surplus = 4
	flood := make([]net.Conn, 0, maxPreAuthPerSource+surplus)
	for range cap(flood) {
		conn, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		flood = append(flood, conn)
	}

	// A rejected connection is closed at accept time, before TLS, so it ends
	// within milliseconds. The deadline only bounds the test: accepting any
	// error would also accept the timeout of an unlimited idle TLS connection,
	// which is the state this test exists to rule out.
	const readBudget = 3 * time.Second
	closed, open, slowest := classifyPreAuthConnections(t, flood, readBudget)
	if closed < surplus {
		t.Fatalf("connections ended past the per-source limit = %d, want at least %d", closed, surplus)
	}
	if open < maxSessionsPerIP {
		t.Fatalf("connections still open = %d, want room for the %d sessions one source may run", open, maxSessionsPerIP)
	}
	if slowest >= readBudget/3 {
		t.Fatalf("slowest rejection took %s, want well under the %s deadline", slowest, readBudget)
	}

	// Freeing the source's slots restores service, which proves the listener
	// kept accepting while the source sat at its cap. The slot belongs to the
	// accepted connection, so it comes back once the server side notices the
	// close rather than the instant the client hangs up.
	for _, conn := range flood {
		_ = conn.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := probeHealthz(address, opts.expectedHost, roots)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("health probe after the flood: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return after the context was cancelled")
	}
}

// classifyPreAuthConnections reads one byte from every connection in parallel
// and reports how many the server ended, how many outlasted the budget, and how
// long the slowest ending took. Only a deadline counts as still open: any other
// read outcome means the connection is gone, and data would mean the relay
// spoke to an unauthenticated peer.
func classifyPreAuthConnections(t *testing.T, conns []net.Conn, budget time.Duration) (closed, open int, slowest time.Duration) {
	t.Helper()
	type outcome struct {
		elapsed time.Duration
		closed  bool
		err     error
	}
	outcomes := make([]outcome, len(conns))
	deadline := time.Now().Add(budget)
	var pending sync.WaitGroup
	for i, conn := range conns {
		pending.Add(1)
		go func() {
			defer pending.Done()
			if err := conn.SetReadDeadline(deadline); err != nil {
				outcomes[i] = outcome{err: err}
				return
			}
			started := time.Now()
			_, err := conn.Read(make([]byte, 1))
			elapsed := time.Since(started)
			switch {
			case err == nil:
				outcomes[i] = outcome{err: errors.New("relay sent data to an unauthenticated connection")}
			case errors.Is(err, os.ErrDeadlineExceeded):
				outcomes[i] = outcome{elapsed: elapsed}
			default:
				outcomes[i] = outcome{elapsed: elapsed, closed: true}
			}
		}()
	}
	pending.Wait()

	for i, result := range outcomes {
		if result.err != nil {
			t.Fatalf("connection %d: %v", i, result.err)
		}
		if !result.closed {
			open++
			continue
		}
		closed++
		if result.elapsed > slowest {
			slowest = result.elapsed
		}
	}
	return closed, open, slowest
}

func TestServeRejectsUnusableCredentialFile(t *testing.T) {
	opts, _ := testDaemonOptions(t)
	if err := os.WriteFile(opts.credentialFile, []byte(legacyVector+"\n"), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	listener, err := listenRelay("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// A file holding only the superseded credential fails closed rather than
	// running a relay no current client can reach.
	if err := serve(context.Background(), opts, slog.New(slog.DiscardHandler), listener); err == nil {
		t.Fatal("expected a credential error")
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), 500*time.Millisecond); err == nil {
		t.Fatal("listener stayed open after a startup failure")
	}
}

func TestRejectRequestBodiesWrapsHealthAndUnknownRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler("murmur.example.test", nil))
	handler := rejectRequestBodies(mux)

	for _, tc := range []struct {
		name             string
		path             string
		contentLength    int64
		transferEncoding []string
	}{
		{name: "health content length", path: "/healthz", contentLength: 1},
		{name: "unknown chunked", path: "/unknown", contentLength: -1, transferEncoding: []string{"chunked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test"+tc.path, nil)
			request.Host = "murmur.example.test"
			request.RemoteAddr = "127.0.0.1:12345"
			request.ContentLength = tc.contentLength
			request.TransferEncoding = tc.transferEncoding
			request.Body = io.NopCloser(mainPanicReader{})
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if got := response.Header().Get("Connection"); !strings.EqualFold(got, "close") {
				t.Fatalf("Connection = %q, want close", got)
			}
			if !request.Close {
				t.Fatal("request connection was not marked for closure")
			}
		})
	}
}

func TestRejectRequestBodiesClosesIncompleteChunkedRequestPromptly(t *testing.T) {
	server := httptest.NewUnstartedServer(rejectRequestBodies(http.NotFoundHandler()))
	server.EnableHTTP2 = false
	server.StartTLS()
	t.Cleanup(server.Close)

	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(server.Certificate())
	dialer := &net.Dialer{Timeout: time.Second}
	connection, err := tls.DialWithDialer(dialer, "tcp", strings.TrimPrefix(server.URL, "https://"), &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
		ServerName: "example.com",
		NextProtos: []string{"http/1.1"},
	})
	if err != nil {
		t.Fatalf("dial TLS server: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	if _, err := io.WriteString(connection, "GET /unknown HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\n\r\n"); err != nil {
		t.Fatalf("write incomplete chunked request: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(750 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read rejection response: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	if !response.Close {
		t.Fatal("response did not signal connection closure")
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read rejection body: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close rejection body: %v", err)
	}

	started := time.Now()
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("connection did not close promptly after rejection: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("connection closure took %s, want under 500ms", elapsed)
	}
}

type mainPanicReader struct{}

func (mainPanicReader) Read([]byte) (int, error) {
	panic("request body must not be read")
}

func testDaemonOptions(t *testing.T) (options, *x509.CertPool) {
	t.Helper()
	dir := t.TempDir()
	certFile := filepath.Join(dir, "relay.crt")
	keyFile := filepath.Join(dir, "relay.key")
	credentialFile := filepath.Join(dir, "GUL_RELAY_BEARER")
	roots := writeDaemonCertificate(t, certFile, keyFile)
	if err := os.WriteFile(credentialFile, []byte(currentVector+"\n"+legacyVector+"\n"), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	return options{
		listen:             "127.0.0.1:0",
		expectedHost:       "murmur.example.test",
		certFile:           certFile,
		keyFile:            keyFile,
		credentialFile:     credentialFile,
		acceptLegacyBearer: true,
	}, roots
}

func writeDaemonCertificate(t *testing.T, certFile, keyFile string) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "murmur.example.test"},
		DNSNames:     []string{"murmur.example.test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	roots := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots.AddCert(leaf)
	return roots
}

func probeHealthz(address, expectedHost string, roots *x509.CertPool) error {
	transport := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: expectedHost,
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://"+address+"/healthz", nil)
	if err != nil {
		return err
	}
	request.Host = expectedHost
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK || string(body) != "ok\n" {
		return errors.New("unexpected health response: " + response.Status + " " + string(body))
	}
	return nil
}

// TestServerErrorLogSeparatesHandshakeNoiseFromRealFailures keeps the noise a
// public port attracts out of the journal without hiding a handler panic.
func TestServerErrorLogSeparatesHandshakeNoiseFromRealFailures(t *testing.T) {
	recorder := &levelRecorder{}
	errorLog := serverErrorLog(slog.New(recorder))

	errorLog.Printf("http: TLS handshake error from 192.0.2.10:1234: EOF")
	errorLog.Printf("http: panic serving 192.0.2.10:1234: boom")

	if len(recorder.levels) != 2 {
		t.Fatalf("records = %d, want 2", len(recorder.levels))
	}
	if recorder.levels[0] != slog.LevelDebug {
		t.Fatalf("handshake error level = %v, want debug", recorder.levels[0])
	}
	if recorder.levels[1] != slog.LevelError {
		t.Fatalf("panic level = %v, want error", recorder.levels[1])
	}
	if !strings.Contains(recorder.messages[1], "panic serving") {
		t.Fatalf("panic message = %q", recorder.messages[1])
	}
}

type levelRecorder struct {
	levels   []slog.Level
	messages []string
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, record slog.Record) error {
	r.levels = append(r.levels, record.Level)
	message := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		message += " " + attr.Value.String()
		return true
	})
	r.messages = append(r.messages, message)
	return nil
}

func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *levelRecorder) WithGroup(string) slog.Handler { return r }
