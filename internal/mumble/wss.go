package mumble

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

var (
	ErrRelayPasswordRequired = errors.New("server password is required for WSS")
	ErrRelayAuthentication   = errors.New("WSS relay authentication failed")
	// ErrRelayNotFound covers both a wrong address and a wrong password: the
	// relay deliberately answers them the same way, so the client cannot tell
	// them apart either.
	ErrRelayNotFound = errors.New("неверный адрес сервера или пароль")
	// ErrRelayRateLimited reports a relay that answered 429. Use errors.As with
	// *RateLimitedError to recover how long the relay asked us to wait.
	ErrRelayRateLimited = errors.New("WSS relay is refusing connection attempts")
	// ErrRelayFull reports a relay that answered 503: the credential was
	// accepted, the relay simply has no free slot. Use errors.As with
	// *RelayFullError to recover how long it asked us to wait.
	ErrRelayFull = errors.New("WSS relay has no free capacity")
	// ErrServerUnavailable is the relay reporting that the server behind it is
	// not answering. It is not a fact about the road, so the reconnect loop
	// must not spend the user's other roads looking for a server that is off.
	ErrServerUnavailable = errors.New("сервер не отвечает")
)

// maxRelayRetryAfter caps what a relay can make the client wait. A hostile or
// broken Retry-After must not park the reconnect loop for hours.
const maxRelayRetryAfter = 5 * time.Minute

// RateLimitedError carries the relay's Retry-After so the reconnect loop can
// honour the ban instead of hammering through its own backoff ladder.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return retryAfterMessage(ErrRelayRateLimited, e.RetryAfter)
}

// Is makes errors.Is(err, ErrRelayRateLimited) succeed for this type.
func (e *RateLimitedError) Is(target error) bool { return target == ErrRelayRateLimited }

// RelayFullError carries the Retry-After of a relay that is reachable and
// willing but out of capacity. It is as transient as a rate limit and travels
// the same path through the reconnect loop, with its own message for the user.
type RelayFullError struct {
	RetryAfter time.Duration
}

func (e *RelayFullError) Error() string {
	return retryAfterMessage(ErrRelayFull, e.RetryAfter)
}

// Is makes errors.Is(err, ErrRelayFull) succeed for this type.
func (e *RelayFullError) Is(target error) bool { return target == ErrRelayFull }

func retryAfterMessage(base error, retryAfter time.Duration) string {
	if retryAfter <= 0 {
		return base.Error()
	}
	return fmt.Sprintf("%s: retry after %s", base.Error(), retryAfter)
}

// relayStream is the outer WSS transport: a byte stream plus the websocket
// handle, which is what an abort needs. Closing the stream runs the WebSocket
// close handshake with an internal 5-second timeout that no context cancels,
// so a rejected connection to a wedged relay must be dropped, not closed.
type relayStream struct {
	net.Conn
	ws *websocket.Conn
	// stopChaff ends the goroutine that keeps the tunnel talking. It is a
	// no-op on a session that negotiated the plain contract, so both paths
	// close the same way.
	stopChaff func()
}

// Close stops the chaff before closing the stream. A goroutine writing into a
// closed connection would only produce errors, but it would also outlive the
// session that owns it, and reconnects are the normal case on the networks
// this exists for.
func (s *relayStream) Close() error {
	s.stopChaff()
	return s.Conn.Close()
}

func (s *relayStream) closeNow() {
	s.stopChaff()
	_ = s.ws.CloseNow()
}

func dialWSS(
	ctx context.Context,
	address string,
	credential relayproto.Credential,
	baseClient *http.Client,
) (*relayStream, error) {
	if credential == "" {
		return nil, ErrRelayPasswordRequired
	}

	// Where the tunnel answers and what it calls itself are derived from the
	// credential, so they differ per server and describe nothing. The pair
	// they replace announced the contents to anything that terminates TLS on
	// the way: `GET /mumble` with `Sec-WebSocket-Protocol: gul-mumble-v1`.
	names := relayproto.NamesFor(credential)
	// Chrome's TLS handshake and a browser's headers (browsertls.go): what is
	// visible outside the tunnel should look like the most ordinary thing on
	// the web, and what a TLS-inspecting middlebox reads inside it should not
	// say "Go program on an opaque connection".
	client := browserClient(baseClient)
	header := make(http.Header)
	header.Set("Authorization", credential.Header())
	applyBrowserHeaders(header, relayOrigin(address))
	ws, response, err := websocket.Dial(ctx, address+names.Path, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: header,
		// One name. Offering the older ones as insurance against a rollback
		// would cost the property this contract exists for: three derived hex
		// names in a WebSocket handshake is a shape ordinary browsing does not
		// have, and it would be offered on every connection to hedge against
		// an image nobody has deployed.
		Subprotocols:    []string{names.Tunnel},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized:
				return nil, ErrRelayAuthentication
			case http.StatusNotFound:
				// A relay that hides itself answers a wrong credential exactly
				// as it answers an address that does not exist, on purpose
				// (internal/relay/cover.go). So neither can be named alone.
				return nil, ErrRelayNotFound
			case http.StatusTooManyRequests:
				return nil, &RateLimitedError{RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
			case http.StatusServiceUnavailable:
				return nil, &RelayFullError{RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
			}
		}
		return nil, fmt.Errorf("WSS relay handshake failed: %w", err)
	}
	negotiated := ws.Subprotocol()
	if negotiated != names.Tunnel {
		_ = ws.CloseNow()
		return nil, errors.New("WSS relay did not negotiate the required protocol")
	}

	// The dial context only bounds connection setup. A background lifetime is
	// deliberate: cancelling the 10-second setup context must not kill a live
	// voice session. Session.Disconnect owns and closes the returned stream.
	stream := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	// Load-bearing order: NetConn disables the read limit (sets it to -1), so
	// the bound has to be re-applied afterwards or an unbounded message can
	// exhaust memory.
	ws.SetReadLimit(relayproto.MaxMessageBytes)

	// Every write leaves as a fixed-size frame and the tunnel keeps talking
	// through the silences (relayproto.Shape). The chaff outlives the dial
	// context on purpose, exactly as the stream does: cancelling setup must
	// not stop a live session from being shaped. websocket.NetConn promises
	// one message per Write, which is the guarantee the shaping rests on
	// (relayproto.AsMessageConn).
	shaped := relayproto.Shape(relayproto.AsMessageConn(stream))
	chaffCtx, stopChaff := context.WithCancel(context.Background())
	go shaped.SendChaff(chaffCtx)
	return &relayStream{Conn: shaped, ws: ws, stopChaff: stopChaff}, nil
}

// relayOrigin is the Origin a browser loading a page from this relay would
// send: the same authority, port and all. An address that cannot be parsed
// yields an empty origin rather than an error - the dial that follows will
// fail on its own and say why.
func relayOrigin(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return "https://" + parsed.Host
}

// parseRetryAfter reads both header forms (delta-seconds and HTTP-date) and
// clamps the result. An absent or unusable value yields zero, which leaves the
// caller on its own backoff.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return clampRetryAfter(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return clampRetryAfter(time.Until(deadline))
	}
	return 0
}

func clampRetryAfter(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return 0
	case d > maxRelayRetryAfter:
		return maxRelayRetryAfter
	default:
		return d
	}
}

// dialWSSTunnel opens the WebSocket road and runs the tunnel contract on it.
//
// There is no second TLS session here any more. It is the change the whole
// milestone turns on: measured on this wire, in the first 25 packets of the
// flow and inside the tunnel, the client's opening burst was 2002 bytes in
// seven packets with the nested handshake and is 286 in one without it, across
// the 300-byte line the published rule uses to tell ordinary connections from
// proxied ones. Padding could not close that; removing the handshake did.
//
// What the relay answers with is pinned exactly as the server's own
// certificate used to be, with one difference that has to be said out loud:
// the relay is now the one reporting it. The pin still catches the server's
// key changing. It is no longer proof against a relay that lies.
func dialWSSTunnel(
	ctx context.Context,
	ep endpoint,
	credential relayproto.Credential,
	tofu *TOFUStore,
	baseClient *http.Client,
) (net.Conn, error) {
	if ep.kind != endpointRelay {
		return nil, errors.New("relay endpoint is required")
	}
	if tofu == nil {
		return nil, errors.New("TOFU store is required")
	}
	stream, err := dialWSS(ctx, ep.address, credential, baseClient)
	if err != nil {
		return nil, err
	}
	if err := openTunnel(ctx, stream, ep.host, tofu); err != nil {
		stream.closeNow()
		return nil, err
	}
	return stream, nil
}

// openTunnel runs the exchange and settles what the client is allowed to
// believe about the far end.
func openTunnel(ctx context.Context, stream net.Conn, host string, tofu *TOFUStore) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
		defer func() { _ = stream.SetDeadline(time.Time{}) }()
	}
	// No certificate: an identity the relay cannot watch anybody prove
	// possession of is a claim, and a claim it honoured would let it be any
	// user it liked. The exchange that proves possession is the next step.
	if err := relayproto.WriteTunnelHello(stream, relayproto.TunnelHello{
		Version: relayproto.TunnelVersion,
	}); err != nil {
		return fmt.Errorf("tunnel hello: %w", err)
	}
	accept, err := relayproto.ReadTunnelAccept(stream)
	if err != nil {
		return fmt.Errorf("tunnel accept: %w", err)
	}
	switch accept.Status {
	case relayproto.TunnelAccepted:
	case relayproto.TunnelUpstreamDown:
		// Not a fact about the road. Saying so keeps the client from searching
		// through every other road it has for a server that is simply off.
		return ErrServerUnavailable
	default:
		return fmt.Errorf("%w: relay answered %s", relayproto.ErrTunnelProtocol, accept.Status)
	}
	if err := tofu.VerifyFingerprint(host, string(accept.Fingerprint)); err != nil {
		return err
	}
	if err := relayproto.ReadTunnelReady(stream); err != nil {
		return fmt.Errorf("tunnel ready: %w", err)
	}
	return nil
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
