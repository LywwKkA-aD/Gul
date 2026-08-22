package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriveCredentialCommandFromStandardInput(t *testing.T) {
	var output bytes.Buffer
	if err := deriveCredentialCommand(nil, strings.NewReader("secret\n"), &output); err != nil {
		t.Fatalf("derive credential: %v", err)
	}
	const want = "ecXjMdtgB9bAbJ4xSNptLwta9ET3_MHCKlC72qd_3Ik\n"
	if got := output.String(); got != want {
		t.Fatalf("credential = %q, want known vector", got)
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatal("output leaked the raw Mumble password")
	}
}

func TestDeriveCredentialCommandFromAbsoluteSecretFile(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "mumble-password")
	if err := os.WriteFile(secretFile, []byte("secret\r\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	var output bytes.Buffer
	if err := deriveCredentialCommand([]string{"--secret-file", secretFile}, strings.NewReader("ignored"), &output); err != nil {
		t.Fatalf("derive credential from file: %v", err)
	}
	if got := output.String(); got != "ecXjMdtgB9bAbJ4xSNptLwta9ET3_MHCKlC72qd_3Ik\n" {
		t.Fatalf("credential = %q, want known vector", got)
	}
}

func TestDeriveCredentialCommandRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "empty", input: "\n"},
		{name: "oversized", input: strings.Repeat("x", maxCredentialInputBytes+1)},
		{name: "relative file", args: []string{"--secret-file", "relative-secret"}, input: "unused"},
		{name: "positional secret", args: []string{"raw-password"}, input: "unused"},
		{name: "unknown flag", args: []string{"--raw-secret", "raw-password"}, input: "unused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := deriveCredentialCommand(tc.args, strings.NewReader(tc.input), &output); err == nil {
				t.Fatal("expected error")
			}
			if output.Len() != 0 {
				t.Fatalf("output on failure = %q, want empty", output.String())
			}
		})
	}
}

func TestPreAuthorizationConnectionLimitLeavesBoundedHandshakeHeadroom(t *testing.T) {
	if maxPreAuthConnections != 256 {
		t.Fatalf("pre-authorization connection limit = %d, want 256", maxPreAuthConnections)
	}
	if maxPreAuthConnections <= maxRelayConnections {
		t.Fatalf("pre-authorization limit %d must exceed active relay limit %d", maxPreAuthConnections, maxRelayConnections)
	}
}

func TestHealthHandlerRequiresExpectedHost(t *testing.T) {
	handler := healthHandler("murmur.example.test")

	good := httptest.NewRequest(http.MethodGet, "https://murmur.example.test/healthz", nil)
	good.Host = "murmur.example.test"
	good.RemoteAddr = "127.0.0.1:12345"
	goodResponse := httptest.NewRecorder()
	handler.ServeHTTP(goodResponse, good)
	if goodResponse.Code != http.StatusOK || goodResponse.Body.String() != "ok\n" {
		t.Fatalf("good response = %d %q", goodResponse.Code, goodResponse.Body.String())
	}

	bad := httptest.NewRequest(http.MethodGet, "https://other.example.test/healthz", nil)
	bad.Host = "other.example.test"
	bad.RemoteAddr = "127.0.0.1:12345"
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusMisdirectedRequest {
		t.Fatalf("bad host status = %d, want 421", badResponse.Code)
	}
}

func TestHealthHandlerIsLoopbackOnly(t *testing.T) {
	handler := healthHandler("murmur.example.test")
	request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test/healthz", nil)
	request.Host = "murmur.example.test"
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("public health status = %d, want 404", response.Code)
	}
}

func TestRejectRequestBodiesWrapsHealthAndUnknownRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler("murmur.example.test"))
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

func TestHealthcheckVerifiesTLSHostAndHTTP(t *testing.T) {
	server := httptest.NewTLSServer(healthHandler("example.com"))
	t.Cleanup(server.Close)
	certFile := filepath.Join(t.TempDir(), "relay.crt")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certFile, certificate, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	address := strings.TrimPrefix(server.URL, "https://")

	if err := healthcheck(address, "example.com", certFile); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}
	if err := healthcheck(address, "wrong.example.com", certFile); err == nil {
		t.Fatal("healthcheck accepted the wrong TLS hostname")
	}
}
