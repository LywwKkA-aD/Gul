// gul-relay exposes one authenticated WSS endpoint and forwards its opaque
// binary stream to the local Murmur listener. It is intentionally not a
// general-purpose proxy.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relay"
	"github.com/LywwKkA-aD/Gul/internal/relayproto"
	"golang.org/x/net/netutil"
)

const (
	upstreamAddress         = "127.0.0.1:64738"
	maxRelayConnections     = 64
	maxPreAuthConnections   = 256
	maxCredentialInputBytes = 4096
	bearerCredentialBytes   = 43 // Unpadded base64url encoding of SHA-256.
)

var version = "dev"

type options struct {
	listen         string
	expectedHost   string
	certFile       string
	keyFile        string
	credentialFile string
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck("127.0.0.1:8443", "murmur.gulvox.com", "/run/relay-tls/mumble.crt"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "derive-credential" {
		if err := deriveCredentialCommand(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "derive credential:", err)
			os.Exit(1)
		}
		return
	}

	opts := options{}
	flag.StringVar(&opts.listen, "listen", "0.0.0.0:8443", "TCP address for HTTPS/WSS")
	flag.StringVar(&opts.expectedHost, "host", "murmur.gulvox.com", "required HTTP Host")
	flag.StringVar(&opts.certFile, "cert", "/run/relay-tls/mumble.crt", "TLS certificate chain")
	flag.StringVar(&opts.keyFile, "key", "/run/relay-tls/mumble.key", "TLS private key")
	flag.StringVar(&opts.credentialFile, "credential-file", "/run/secrets/GUL_RELAY_BEARER", "pre-derived relay bearer credential file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts, logger); err != nil {
		logger.Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func deriveCredentialCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("derive-credential", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	secretFile := flags.String("secret-file", "", "absolute path to the raw Mumble password file")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid derive-credential flags")
	}
	if flags.NArg() != 0 {
		return errors.New("raw credentials must not be passed as command arguments")
	}

	input := stdin
	var file *os.File
	if *secretFile != "" {
		if !filepath.IsAbs(*secretFile) || filepath.Clean(*secretFile) != *secretFile {
			return errors.New("secret-file must be a clean absolute path")
		}
		var err error
		file, err = os.Open(*secretFile)
		if err != nil {
			return fmt.Errorf("open secret-file: %w", err)
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("inspect secret-file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("secret-file must be a regular file")
		}
		input = file
	}

	secret, err := readOneLineSecret(input, maxCredentialInputBytes)
	if err != nil {
		return fmt.Errorf("read raw Mumble password: %w", err)
	}
	defer clear(secret)
	authorization := relayproto.Authorization(secret)
	credential, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || credential == "" {
		return errors.New("derive bearer credential")
	}
	if _, err := fmt.Fprintln(stdout, credential); err != nil {
		return fmt.Errorf("write bearer credential: %w", err)
	}
	return nil
}

func readOneLineSecret(input io.Reader, maxBytes int) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(input, int64(maxBytes+3)))
	if err != nil {
		return nil, err
	}
	if len(value) > maxBytes+2 {
		clear(value)
		return nil, errors.New("secret input is too large")
	}
	if bytes.HasSuffix(value, []byte("\r\n")) {
		value = value[:len(value)-2]
	} else if bytes.HasSuffix(value, []byte("\n")) {
		value = value[:len(value)-1]
	}
	if len(value) == 0 {
		clear(value)
		return nil, errors.New("secret input is empty")
	}
	if len(value) > maxBytes {
		clear(value)
		return nil, errors.New("secret input is too large")
	}
	if bytes.ContainsAny(value, "\r\n") {
		clear(value)
		return nil, errors.New("secret input must contain exactly one line")
	}
	return value, nil
}

func run(ctx context.Context, opts options, logger *slog.Logger) error {
	credentialFile, err := os.Open(opts.credentialFile)
	if err != nil {
		return fmt.Errorf("open relay bearer credential: %w", err)
	}
	credential, readErr := readOneLineSecret(credentialFile, bearerCredentialBytes)
	closeErr := credentialFile.Close()
	if readErr != nil {
		return fmt.Errorf("read relay bearer credential: %w", readErr)
	}
	if closeErr != nil {
		clear(credential)
		return fmt.Errorf("close relay bearer credential: %w", closeErr)
	}
	handler, err := relay.NewHandler(relay.Config{
		ExpectedHost:            opts.expectedHost,
		Upstream:                upstreamAddress,
		BearerCredential:        credential,
		MaxConnections:          maxRelayConnections,
		MaxConnectionsPerIP:     8,
		MaxWebSocketMessageSize: 64 << 10,
		DialTimeout:             3 * time.Second,
		AuthFailuresBeforeBan:   5,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         5 * time.Minute,
		MaxAuthTrackedSources:   4096,
	})
	clear(credential)
	if err != nil {
		return err
	}
	loader, err := relay.NewCertificateLoader(opts.certFile, opts.keyFile)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle(relay.Path, handler)
	mux.HandleFunc("/healthz", healthHandler(opts.expectedHost))
	server := &http.Server{
		Addr:              opts.listen,
		Handler:           rejectRequestBodies(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"http/1.1"},
			GetCertificate: loader.GetCertificate,
		},
	}

	listener, err := net.Listen("tcp4", opts.listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	limitedListener := netutil.LimitListener(listener, maxPreAuthConnections)
	tlsListener := tls.NewListener(limitedListener, server.TLSConfig)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(tlsListener) }()
	logger.Info("relay ready", "version", version, "listen", opts.listen)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve relay: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := handler.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown relay sessions: %w", err)
		}
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown relay: %w", err)
		}
		return nil
	}
}

func rejectRequestBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			w.Header().Set("Connection", "close")
			r.Close = true
			// net/http otherwise tries to drain up to 256 KiB after the handler
			// returns. An incomplete chunked body could hold the connection until
			// the client sends more data, so expire reads before rejecting it.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
			http.Error(w, "request body is not accepted", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func healthHandler(expectedHost string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
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

func healthcheck(address, expectedHost, certFile string) error {
	certificatePEM, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf("relay healthcheck certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		return errors.New("relay healthcheck certificate is invalid")
	}
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
		return fmt.Errorf("relay healthcheck request: %w", err)
	}
	request.Host = expectedHost
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("relay healthcheck request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("relay healthcheck status: %s", response.Status)
	}
	return nil
}
