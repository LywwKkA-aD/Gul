package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

var (
	ErrRelayPasswordRequired = errors.New("server password is required for WSS")
	ErrRelayAuthentication   = errors.New("WSS relay authentication failed")
)

func dialWSS(ctx context.Context, address, password string, baseClient *http.Client) (net.Conn, error) {
	if password == "" {
		return nil, ErrRelayPasswordRequired
	}

	client := noRedirectHTTPClient(baseClient)
	header := make(http.Header)
	header.Set("Authorization", relayproto.Authorization([]byte(password)))
	ws, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{
		HTTPClient:      client,
		HTTPHeader:      header,
		Subprotocols:    []string{relayproto.Subprotocol},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil && response.StatusCode == http.StatusUnauthorized {
			return nil, ErrRelayAuthentication
		}
		return nil, fmt.Errorf("WSS relay handshake failed: %w", err)
	}
	if ws.Subprotocol() != relayproto.Subprotocol {
		_ = ws.Close(websocket.StatusProtocolError, "required subprotocol was not negotiated")
		return nil, errors.New("WSS relay did not negotiate the required protocol")
	}

	// The dial context only bounds connection setup. A background lifetime is
	// deliberate: cancelling the 10-second setup context must not kill a live
	// voice session. Session.Disconnect owns and closes the returned stream.
	stream := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	ws.SetReadLimit(64 << 10)
	return stream, nil
}

// dialWSSMumbleTLS establishes the outer CA-verified WSS connection, then
// performs the ordinary Mumble TLS handshake inside that opaque stream. The
// inner certificate keeps the same TOFU identity as a direct connection.
func dialWSSMumbleTLS(
	ctx context.Context,
	ep endpoint,
	password string,
	tofu *TOFUStore,
	certificate *tls.Certificate,
	baseClient *http.Client,
) (net.Conn, error) {
	if ep.kind != endpointWSS {
		return nil, errors.New("WSS endpoint is required")
	}
	if tofu == nil {
		return nil, errors.New("TOFU store is required")
	}

	stream, err := dialWSS(ctx, ep.address, password, baseClient)
	if err != nil {
		return nil, err
	}
	tlsConfig := tofu.TLSConfig(ep.host)
	// tls.Client does not infer a hostname from a net.Conn. Set it explicitly
	// so the inner Mumble handshake still sends the public hostname as SNI.
	tlsConfig.ServerName = ep.host
	if certificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*certificate}
	}
	inner := tls.Client(stream, tlsConfig)
	if err := inner.HandshakeContext(ctx); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("inner Mumble TLS handshake failed: %w", err)
	}
	return inner, nil
}

func noRedirectHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}
