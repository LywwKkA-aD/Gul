package mumble

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	utls "github.com/refraction-networking/utls"
)

// What the outer TLS handshake looks like is the last thing about this client
// that is visible without decrypting anything, and Go's handshake looks like
// nothing else on the web.
//
// crypto/tls sends its own cipher list in its own order, its own extension
// order, and no GREASE at all - the deliberately invalid values every browser
// scatters through its ClientHello to keep middleboxes honest. The result is a
// JA3/JA4 fingerprint that says "a Go program" to anything that keeps a table
// of them, which is one lookup away from "not a browser, on 443, to a host
// nobody browses". Chrome's handshake, by contrast, is the single most common
// sight on the internet.
//
// So the outer handshake is Chrome's. Only the outer one: the Mumble TLS
// inside the tunnel is already invisible, and the client certificate that
// identifies the user lives there, where it belongs.

// browserHeaders are what a browser sends with a WebSocket handshake and Go
// does not. They matter to anything that terminates TLS on the way - a company
// proxy, an antivirus with HTTPS scanning - which reads the request in clear
// text and would otherwise find `User-Agent: Go-http-client/1.1` on a
// connection it cannot inspect.
//
// The user agent is a real Chrome one and is deliberately not derived from the
// running platform: a Windows client claiming macOS is far less remarkable
// than a client claiming to be Go.
const (
	browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	browserAcceptLanguage = "en-US,en;q=0.9"
)

// browserClient returns an HTTP client whose TLS handshake is Chrome's.
//
// It keeps whatever the base client already had - a test's trusted roots, a
// caller's proxy - and replaces only how the connection is established. A base
// client without an *http.Transport is left alone rather than silently
// rebuilt: that is a caller doing something deliberate.
func browserClient(base *http.Client) *http.Client {
	client := noRedirectHTTPClient(base)
	transport, ok := transportOf(client)
	if !ok {
		return client
	}
	clone := transport.Clone()
	tlsConfig := clone.TLSClientConfig
	// Whatever the base client used to reach the network keeps being used:
	// DialTLSContext replaces the handshake, not the route, and a caller that
	// installed a dialer of its own meant it.
	dial := clone.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	clone.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialBrowserTLS(ctx, network, address, tlsConfig, dial)
	}
	// DialTLSContext owns the handshake now, and the fields below would only
	// describe one that no longer happens.
	clone.TLSClientConfig = nil
	clone.ForceAttemptHTTP2 = false
	client.Transport = clone
	return client
}

// transportOf finds the *http.Transport a client dials through.
func transportOf(client *http.Client) (*http.Transport, bool) {
	if client.Transport == nil {
		return http.DefaultTransport.(*http.Transport), true
	}
	transport, ok := client.Transport.(*http.Transport)
	return transport, ok
}

// dialBrowserTLS opens one connection and completes Chrome's handshake on it.
//
// HelloChrome_Auto is the current Chrome the library ships, so the fingerprint
// follows the browser as the dependency is updated instead of freezing on the
// day this was written - a ClientHello from a Chrome three years out of date
// is its own kind of unusual.
func dialBrowserTLS(
	ctx context.Context,
	network, address string,
	base *tls.Config,
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	// The name to verify comes from the address, not from wherever the dialer
	// actually connects: those differ on purpose when a caller redirects one
	// to the other.
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	raw, err := dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	config := &utls.Config{ServerName: host}
	if base != nil {
		if base.ServerName != "" {
			config.ServerName = base.ServerName
		}
		config.RootCAs = base.RootCAs
		config.InsecureSkipVerify = base.InsecureSkipVerify
	}
	conn, err := browserHandshake(ctx, raw, config)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// browserHandshake performs Chrome's handshake, minus HTTP/2 in ALPN.
//
// The preset offers "h2, http/1.1" because that is what the browser offers,
// and against our own relay it costs nothing: the relay speaks only HTTP/1.1
// and picks it out of the list. Against anything else it is fatal. A CDN
// selects h2, the connection is then an HTTP/2 one, and the WebSocket dial -
// which is HTTP/1.1 semantics, and stays that way because RFC 8441 is not
// what this library implements - reads the server's first SETTINGS frame as a
// reply and gives up with "malformed HTTP response".
//
// Found the first time this client was pointed at a Cloudflare front, which is
// the only remaining idea for a user whose traffic dies to every host of ours
// and to no large provider. Offering one protocol where Chrome offers two is a
// deviation from the fingerprint, and it is taken deliberately: it is the
// difference between being able to hide behind a CDN and not.
func browserHandshake(ctx context.Context, raw net.Conn, config *utls.Config) (*utls.UConn, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		return nil, err
	}
	for _, extension := range spec.Extensions {
		alpn, ok := extension.(*utls.ALPNExtension)
		if !ok {
			continue
		}
		alpn.AlpnProtocols = []string{"http/1.1"}
	}

	conn := utls.UClient(raw, config, utls.HelloCustom)
	if err := conn.ApplyPreset(&spec); err != nil {
		return nil, err
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return conn, nil
}

// applyBrowserHeaders fills in what a browser would send and Go would not.
//
// origin is the server's own, which is what a page served from it would send.
// It has to carry the port when there is one: the WebSocket library on the
// other side compares the Origin against the request Host and refuses a
// mismatch, exactly as it would for a browser.
func applyBrowserHeaders(header http.Header, origin string) {
	header.Set("User-Agent", browserUserAgent)
	header.Set("Origin", origin)
	header.Set("Accept-Language", browserAcceptLanguage)
	header.Set("Cache-Control", "no-cache")
	header.Set("Pragma", "no-cache")
}
