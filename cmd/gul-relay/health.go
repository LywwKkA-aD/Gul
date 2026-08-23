package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// certificateDegradedMarker reports a failing certificate reload without
// failing the health check. The relay keeps serving the last valid pair, and
// HealthOnFailure=kill together with StartLimitBurst would turn a broken
// certificate file into a unit that refuses to start at all.
const certificateDegradedMarker = "certificate-reload: failing"

type healthOptions struct {
	address      string
	expectedHost string
	certFile     string
	// fallbackRoots verifies the served certificate when the pinned file
	// cannot. nil selects the host trust store.
	fallbackRoots *x509.CertPool
	warn          io.Writer
}

func healthHandler(expectedHost string, certificateStatus func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The endpoint is hidden from the public listener by source address
		// alone. That holds because the relay shares Murmur's rootless pasta
		// namespace, where inbound public connections arrive from the
		// namespace gateway address and only the container's own health
		// command reaches it over loopback. A deployment that does not
		// preserve source addresses that way must block /healthz at the
		// firewall instead.
		if !isLoopbackRemote(r.RemoteAddr) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !equalHost(r.Host, expectedHost) {
			http.Error(w, http.StatusText(http.StatusMisdirectedRequest), http.StatusMisdirectedRequest)
			return
		}
		body := "ok\n"
		if certificateStatus != nil && certificateStatus() != nil {
			body += certificateDegradedMarker + "\n"
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func isLoopbackRemote(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func equalHost(got, want string) bool {
	host, _, err := net.SplitHostPort(got)
	if err == nil {
		got = host
	}
	return bytes.EqualFold([]byte(got), []byte(want))
}

// healthcheck probes the local endpoint. It pins the deployed leaf chain
// first, then falls back to the host trust store: the certificate loader
// picks a renewal up to one check interval after the ACME client writes it,
// so pinning the file alone would report a healthy relay as dead for that
// window and get the unit killed.
func healthcheck(opts healthOptions) error {
	pinned, poolErr := certificatePool(opts.certFile)
	if poolErr == nil {
		body, err := probeHealth(opts.address, opts.expectedHost, pinned)
		if err == nil {
			reportHealthBody(opts.warn, body)
			return nil
		}
		if !isCertificateVerificationFailure(err) {
			return err
		}
	}
	body, err := probeHealth(opts.address, opts.expectedHost, opts.fallbackRoots)
	if err != nil {
		if poolErr != nil {
			return errors.Join(fmt.Errorf("relay healthcheck certificate: %w", poolErr), err)
		}
		return err
	}
	reportHealthBody(opts.warn, body)
	return nil
}

func reportHealthBody(warn io.Writer, body string) {
	if warn == nil || !strings.Contains(body, certificateDegradedMarker) {
		return
	}
	fmt.Fprintln(warn, "relay healthcheck: certificate reload is failing, the served pair is stale")
}

func certificatePool(certFile string) (*x509.CertPool, error) {
	certificatePEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		return nil, errors.New("relay healthcheck certificate is invalid")
	}
	return roots, nil
}

func probeHealth(address, expectedHost string, roots *x509.CertPool) (string, error) {
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: expectedHost,
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://"+address+"/healthz", nil)
	if err != nil {
		return "", fmt.Errorf("relay healthcheck request: %w", err)
	}
	request.Host = expectedHost
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("relay healthcheck request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relay healthcheck status: %s", response.Status)
	}
	return string(body), nil
}

func isCertificateVerificationFailure(err error) bool {
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var hostname x509.HostnameError
	return errors.As(err, &hostname)
}
