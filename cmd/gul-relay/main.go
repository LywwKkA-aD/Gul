// gul-relay exposes one authenticated WSS endpoint and forwards its opaque
// binary stream to the local Murmur listener. It is intentionally not a
// general-purpose proxy.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relay"
	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const (
	upstreamAddress = "127.0.0.1:64738"
	// defaultListenAddress deliberately names no address family. The relay
	// exists for networks the native Mumble port cannot cross, which includes
	// IPv6-only ones, and a bare port binds both families.
	defaultListenAddress = ":8443"
	maxRelayConnections  = 64
	maxSessionsPerIP     = 8
	// maxPreAuthConnections bounds every accepted connection; maxPreAuthPerSource
	// bounds one source's share of it. The per-source bound is what keeps the
	// endpoint alive: without it one source fills the global bound with idle
	// keep-alive connections and nobody else gets in.
	//
	// An established session keeps its accepted connection, so this bound has to
	// exceed maxSessionsPerIP. Equal values would leave a source running its
	// full quota of sessions with no room left to even open a handshake, and a
	// shared address would lock itself out.
	maxPreAuthConnections = 256
	maxPreAuthPerSource   = 2 * maxSessionsPerIP
	// idleConnectionTimeout drops an idle HTTP connection within seconds. An
	// unauthenticated keep-alive connection is the cheapest way to occupy the
	// endpoint, and net/http bounds an idle connection only if it is told to.
	// Established sessions are unaffected: the WebSocket is hijacked out of the
	// server and carries its own idle timeout.
	idleConnectionTimeout = 5 * time.Second
	// readHeaderTimeout bounds the request line and headers of a connection
	// that did send something; maxRequestHeaderBytes bounds their size.
	readHeaderTimeout     = 5 * time.Second
	maxRequestHeaderBytes = 16 << 10
	shutdownBudget        = 10 * time.Second
)

var version = "dev"

type options struct {
	listen             string
	expectedHost       string
	certFile           string
	keyFile            string
	credentialFile     string
	acceptLegacyBearer bool
	acceptLegacyNames  bool
	quic               bool
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(healthOptions{
			address:      "127.0.0.1:8443",
			expectedHost: "murmur.gulvox.com",
			certFile:     "/run/relay-tls/mumble.crt",
			warn:         os.Stderr,
		}); err != nil {
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
	logLevel := ""
	flag.StringVar(&opts.listen, "listen", defaultListenAddress, "TCP address for HTTPS/WSS; a bare port serves both address families")
	flag.StringVar(&opts.expectedHost, "host", "murmur.gulvox.com", "required HTTP Host")
	flag.StringVar(&opts.certFile, "cert", "/run/relay-tls/mumble.crt", "TLS certificate chain")
	flag.StringVar(&opts.keyFile, "key", "/run/relay-tls/mumble.key", "TLS private key")
	flag.StringVar(&opts.credentialFile, "credential-file", "/run/secrets/GUL_RELAY_BEARER", "pre-derived relay bearer credential file")
	flag.BoolVar(&opts.acceptLegacyBearer, "accept-legacy-bearer", true, "accept the v0.3.0-alpha.2 bearer credential during the deprecation window")
	flag.BoolVar(&opts.acceptLegacyNames, "accept-legacy-names", true, "accept the fixed /mumble path and gul-mumble-v1 subprotocol during the deprecation window")
	flag.BoolVar(&opts.quic, "quic", true, "also accept tunnels over QUIC on the same address, UDP")
	flag.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn or error")
	flag.Parse()

	level, err := parseLogLevel(logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts, logger); err != nil {
		logger.Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", value)
}

func run(ctx context.Context, opts options, logger *slog.Logger) error {
	listener, err := listenRelay(opts.listen)
	if err != nil {
		return err
	}
	return serve(ctx, opts, logger, listener)
}

// listenRelay binds the public listener. The network is "tcp", not "tcp4":
// restricting it to IPv4 excludes the IPv6-only clients the relay exists for.
func listenRelay(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	return listener, nil
}

func serve(ctx context.Context, opts options, logger *slog.Logger, listener net.Listener) error {
	// Serve and Shutdown close the listener themselves; this covers the paths
	// that fail before either of them takes it over.
	defer func() { _ = listener.Close() }()
	credentials, err := readCredentialFile(opts.credentialFile)
	if err != nil {
		return err
	}
	handler, err := relay.NewHandler(relayConfig(opts, credentials, logger))
	if err != nil {
		return err
	}
	loader, err := relay.NewCertificateLoader(opts.certFile, opts.keyFile, logger)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	// The cover site is the catch-all: every path the tunnel and the health
	// probe do not own answers as an ordinary website would (relay/cover.go).
	cover := handler.Cover()
	mux.Handle("/", cover)
	// One mount per name the relay answers on: the current pair is derived
	// from the credential, so it differs per server (relayproto.NamesFor).
	for _, path := range handler.TunnelPaths() {
		mux.Handle(path, handler)
	}
	mux.HandleFunc("/healthz", healthHandler(opts.expectedHost, loader.LastError, cover))
	server := relayServer(listener.Addr().String(), rejectRequestBodies(mux, cover), loader.GetCertificate, logger)

	limitedListener := relay.LimitListenerBySource(listener, maxPreAuthPerSource, maxPreAuthConnections, logger)
	tlsListener := tls.NewListener(limitedListener, server.TLSConfig)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(tlsListener) }()

	// The second road, on the same address over UDP. A failure to open it is
	// not fatal: a relay that answers on TCP is still a working relay, and a
	// host where UDP cannot be bound should not stop being one.
	var quicServer *relay.QUICServer
	if opts.quic {
		quicServer, err = relay.ListenQUIC(listener.Addr().String(), loader.GetCertificate, handler, logger)
		if err != nil {
			logger.Error("relay quic listener unavailable", "error", err)
			quicServer = nil
		} else {
			defer func() { _ = quicServer.Close() }()
			go func() {
				if err := quicServer.Serve(ctx); err != nil {
					logger.Error("relay quic listener stopped", "error", err)
				}
			}()
		}
	}

	logger.Info("relay ready",
		"version", version,
		"listen", listener.Addr().String(),
		"quic", quicServer != nil,
		"accept_legacy_bearer", opts.acceptLegacyBearer,
		"accept_legacy_names", opts.acceptLegacyNames,
	)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve relay: %w", err)
	case <-ctx.Done():
		logger.Info("relay draining sessions")
		if quicServer != nil {
			_ = quicServer.Close()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()
		// The HTTP server stops accepting first and its shutdown is always
		// awaited, so a failed session drain cannot leave the listener open.
		serverDone := make(chan error, 1)
		go func() { serverDone <- server.Shutdown(shutdownCtx) }()
		sessionErr := handler.Shutdown(shutdownCtx)
		serverErr := <-serverDone
		if err := errors.Join(sessionErr, serverErr); err != nil {
			return fmt.Errorf("shutdown relay: %w", err)
		}
		return nil
	}
}

// relayServer builds the HTTPS server. The timeouts live here rather than at
// the call site so a test can pin them: the idle timeout in particular is a
// security bound, not a comfort setting.
func relayServer(address string, handler http.Handler, getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error), logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleConnectionTimeout,
		MaxHeaderBytes:    maxRequestHeaderBytes,
		ErrorLog:          serverErrorLog(logger),
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"http/1.1"},
			GetCertificate: getCertificate,
		},
	}
}

func relayConfig(opts options, credentials []relayproto.Credential, logger *slog.Logger) relay.Config {
	return relay.Config{
		ExpectedHost:            opts.expectedHost,
		Upstream:                upstreamAddress,
		BearerCredentials:       credentials,
		AcceptLegacyBearer:      opts.acceptLegacyBearer,
		AcceptLegacyNames:       opts.acceptLegacyNames,
		MaxConnections:          maxRelayConnections,
		MaxConnectionsPerIP:     maxSessionsPerIP,
		MaxWebSocketMessageSize: relayproto.MaxMessageBytes,
		DialTimeout:             3 * time.Second,
		AuthFailuresBeforeBan:   5,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   4096,
		Logger:                  logger,
	}
}

// serverErrorLog routes net/http's own diagnostics into the structured logger.
// Failed TLS handshakes are attacker-driven noise on a public port and land at
// debug, where an operator can switch them on deliberately; everything else -
// a panic in a handler above all - stays visible at error level.
func serverErrorLog(logger *slog.Logger) *log.Logger {
	return log.New(&httpErrorWriter{logger: logger}, "", 0)
}

type httpErrorWriter struct {
	logger *slog.Logger
}

func (w *httpErrorWriter) Write(line []byte) (int, error) {
	message := strings.TrimSpace(string(line))
	if strings.Contains(message, "TLS handshake error") {
		w.logger.Debug("relay http server", "message", message)
		return len(line), nil
	}
	w.logger.Error("relay http server", "message", message)
	return len(line), nil
}

func rejectRequestBodies(next http.Handler, cover http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			w.Header().Set("Connection", "close")
			r.Close = true
			// net/http otherwise tries to drain up to 256 KiB after the handler
			// returns. An incomplete chunked body could hold the connection until
			// the client sends more data, so expire reads before rejecting it.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
			// The refusal reads as an ordinary site refusing an odd request,
			// not as a service with rules of its own (relay/cover.go).
			r.URL.Path = "/_"
			cover.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
