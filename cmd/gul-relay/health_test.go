package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthHandlerRequiresExpectedHost(t *testing.T) {
	handler := healthHandler("murmur.example.test", nil)

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
	handler := healthHandler("murmur.example.test", nil)
	request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test/healthz", nil)
	request.Host = "murmur.example.test"
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("public health status = %d, want 404", response.Code)
	}
}

// TestHealthHandlerReportsStaleCertificateWithoutFailing covers the deployment
// trap: the relay still serves on the last valid pair, so failing the check
// would let HealthOnFailure=kill and StartLimitBurst brick a working unit.
func TestHealthHandlerReportsStaleCertificateWithoutFailing(t *testing.T) {
	handler := healthHandler("murmur.example.test", func() error { return errors.New("load: bad key") })
	request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test/healthz", nil)
	request.Host = "murmur.example.test"
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("degraded status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), certificateDegradedMarker) {
		t.Fatalf("degraded body = %q, want the certificate marker", response.Body.String())
	}
}

func TestHealthcheckVerifiesTLSHostAndHTTP(t *testing.T) {
	server := httptest.NewTLSServer(healthHandler("example.com", nil))
	t.Cleanup(server.Close)
	certFile := writeCertificateFile(t, server.Certificate())
	address := strings.TrimPrefix(server.URL, "https://")

	if err := healthcheck(healthOptions{address: address, expectedHost: "example.com", certFile: certFile, fallbackRoots: x509.NewCertPool()}); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}
	if err := healthcheck(healthOptions{address: address, expectedHost: "wrong.example.com", certFile: certFile, fallbackRoots: x509.NewCertPool()}); err == nil {
		t.Fatal("healthcheck accepted the wrong TLS hostname")
	}
}

// TestHealthcheckFallsBackToTheTrustStore covers the renewal window: the
// certificate loader picks a new pair up to one check interval after the ACME
// client writes it, so the file on disk and the certificate being served can
// legitimately disagree.
func TestHealthcheckFallsBackToTheTrustStore(t *testing.T) {
	server := httptest.NewTLSServer(healthHandler("example.com", nil))
	t.Cleanup(server.Close)
	// The pinned file holds an unrelated certificate, which is what a file
	// written by the ACME client before the loader picked it up looks like to
	// the health command.
	stale := t.TempDir()
	staleFile := filepath.Join(stale, "relay.crt")
	writeDaemonCertificate(t, staleFile, filepath.Join(stale, "relay.key"))
	address := strings.TrimPrefix(server.URL, "https://")

	trusted := x509.NewCertPool()
	trusted.AddCert(server.Certificate())
	if err := healthcheck(healthOptions{address: address, expectedHost: "example.com", certFile: staleFile, fallbackRoots: trusted}); err != nil {
		t.Fatalf("healthcheck did not fall back: %v", err)
	}

	// Without a trust store that accepts the served certificate the check must
	// still fail: the fallback widens verification, it does not skip it.
	if err := healthcheck(healthOptions{address: address, expectedHost: "example.com", certFile: staleFile, fallbackRoots: x509.NewCertPool()}); err == nil {
		t.Fatal("healthcheck accepted an unverifiable certificate")
	}

	// A missing pinned file behaves the same way.
	missing := filepath.Join(t.TempDir(), "absent.crt")
	if err := healthcheck(healthOptions{address: address, expectedHost: "example.com", certFile: missing, fallbackRoots: trusted}); err != nil {
		t.Fatalf("healthcheck with a missing pinned file: %v", err)
	}
}

func TestHealthcheckWarnsAboutAStaleCertificate(t *testing.T) {
	server := httptest.NewTLSServer(healthHandler("example.com", func() error { return errors.New("load: bad key") }))
	t.Cleanup(server.Close)
	certFile := writeCertificateFile(t, server.Certificate())
	var warn bytes.Buffer

	err := healthcheck(healthOptions{
		address:       strings.TrimPrefix(server.URL, "https://"),
		expectedHost:  "example.com",
		certFile:      certFile,
		fallbackRoots: x509.NewCertPool(),
		warn:          &warn,
	})
	if err != nil {
		t.Fatalf("healthcheck: %v", err)
	}
	if !strings.Contains(warn.String(), "certificate reload is failing") {
		t.Fatalf("warning = %q, want the stale certificate notice", warn.String())
	}
}

func writeCertificateFile(t *testing.T, certificate *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.crt")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path
}
