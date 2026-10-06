package mumble

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/LywwKkA-aD/Gul/internal/reality"
)

type realityDialer func(context.Context, reality.Config, string) (net.Conn, error)

func dialReality(ctx context.Context, cfg DialConfig, ep endpoint, tofu *TOFUStore) (net.Conn, error) {
	return dialRealityWith(ctx, cfg, ep, tofu, reality.Dial)
}

func dialRealityWith(ctx context.Context, cfg DialConfig, ep endpoint, tofu *TOFUStore, connect realityDialer) (net.Conn, error) {
	if ep.kind != endpointReality {
		return nil, errors.New("a VLESS REALITY server address is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(ep.address)
	if err != nil {
		return nil, errors.New("invalid VLESS server address")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	stream, err := connect(ctx, reality.Config{
		Server: net.JoinHostPort(ep.host, port), ServerName: ep.realityServerName,
		PublicKey: ep.realityPublicKey, ShortID: ep.realityShortID, Password: cfg.Password,
	}, mumbleTarget)
	if err != nil {
		if !nilStream(stream) {
			_ = stream.Close()
		}
		return nil, sanitizedRealityError(err, cfg.Password, ep)
	}
	secured, err := mumbleTLS(ctx, stream, cfg, ep, tofu)
	if err != nil {
		return nil, sanitizedRealityError(err, cfg.Password, ep)
	}
	return secured, nil
}

// The core normally returns generic errors. Keep the adapter defensive so a
// future dependency error cannot put profile values into UI or diagnostics.
type realityError struct {
	cause error
	text  string
}

func (e *realityError) Error() string { return e.text }
func (e *realityError) Unwrap() error { return e.cause }

func sanitizedRealityError(err error, password string, ep endpoint) error {
	message := err.Error()
	if password != "" {
		message = strings.ReplaceAll(message, password, "<password>")
	}
	message = RedactServer(message, ep.address)
	return &realityError{cause: err, text: message}
}
