package relay

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSourceKeyFoldsAddressesToTheBlockOneSubscriberControls(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "ipv4", host: "198.51.100.7", want: "198.51.100.7"},
		{name: "ipv4 mapped", host: "::ffff:198.51.100.7", want: "198.51.100.7"},
		{name: "ipv6", host: "2001:db8:1:2:3:4:5:6", want: "2001:db8:1:2::/64"},
		{name: "ipv6 rotated inside one prefix", host: "2001:db8:1:2::dead:beef", want: "2001:db8:1:2::/64"},
		{name: "ipv6 neighboring prefix", host: "2001:db8:1:3::1", want: "2001:db8:1:3::/64"},
		{name: "ipv6 zone", host: "fe80::1%eth0", want: "fe80::/64"},
		{name: "unparseable", host: "not-an-address", want: "not-an-address"},
		{name: "empty", host: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceKey(tc.host); got != tc.want {
				t.Fatalf("sourceKey(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestSourcePrefixKey(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{name: "ipv4", addr: mustAddr("198.51.100.7:443"), want: "198.51.100.7"},
		{name: "ipv4 mapped", addr: mustAddr("[::ffff:198.51.100.7]:443"), want: "198.51.100.7"},
		{name: "ipv6", addr: mustAddr("[2001:db8:1:2:3:4:5:6]:443"), want: "2001:db8:1:2::/64"},
		{name: "ipv6 loopback", addr: mustAddr("[::1]:443"), want: "::/64"},
		{name: "nil", addr: nil, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourcePrefixKey(tc.addr); got != tc.want {
				t.Fatalf("sourcePrefixKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandlerBansOneIPv6PrefixDespiteAddressRotation is the evasion: an IPv6
// subscriber holds a /64, so keying the authorization ban on the full address
// hands the same customer a fresh allowance of password guesses per address,
// 2^64 times over.
func TestHandlerBansOneIPv6PrefixDespiteAddressRotation(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 3
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	h := mustHandler(t, cfg)

	for _, address := range []string{"2001:db8::1", "2001:db8::2", "2001:db8::3"} {
		if got := serveAuthorizationAttempt(h, address, "wrong secret").Code; got != http.StatusUnauthorized {
			t.Fatalf("guess from %s = %d, want 401", address, got)
		}
	}
	banned := serveAuthorizationAttempt(h, "2001:db8::dead:beef", "wrong secret")
	if banned.Code != http.StatusTooManyRequests {
		t.Fatalf("guess from a rotated address = %d, want 429", banned.Code)
	}
	if got := banned.Header().Get("Retry-After"); got == "" {
		t.Fatal("ban response carries no Retry-After")
	}
	// The block is the subscriber, so the ban stops there: a neighboring /64
	// is a different customer and keeps its own allowance.
	if got := serveAuthorizationAttempt(h, "2001:db8:0:1::1", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("guess from another /64 = %d, want 401", got)
	}
}

// TestHandlerBansIPv4AddressesIndividually pins the other half: IPv4 stays at
// /32, so one address never bans its neighbor.
func TestHandlerBansIPv4AddressesIndividually(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 2
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	h := mustHandler(t, cfg)

	for range cfg.AuthFailuresBeforeBan {
		if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
			t.Fatalf("guess = %d, want 401", got)
		}
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("guess past the allowance = %d, want 429", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.11", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("guess from the neighboring address = %d, want 401", got)
	}
}

// TestHandlerCountsRotatingIPv6AddressesAgainstOneSessionLimit drives real
// sessions: rotating inside a /64 used to multiply the per-source session quota
// by the size of the block.
func TestHandlerCountsRotatingIPv6AddressesAgainstOneSessionLimit(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.MaxConnectionsPerIP = 1
	h := mustHandler(t, cfg)
	server := rotatingSourceServer(t, h, "2001:db8::1", "2001:db8::dead:beef", "2001:db8:0:1::1")
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{Subprotocol},
	}

	first, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	t.Cleanup(func() { _ = first.CloseNow() })

	_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err == nil {
		t.Fatal("a rotated address bought a second session")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %#v, want 503", response)
	}

	// Another /64 is another subscriber and is served normally.
	other, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err != nil {
		t.Fatalf("dial from another /64: %v", err)
	}
	t.Cleanup(func() { _ = other.CloseNow() })
}

// TestHandlerMapsOneIPv6PrefixToOneUpstreamPseudonym covers the third keyed
// defense: the loopback alias the upstream connection binds is Murmur's autoban
// bucket, so a rotating subscriber must land in exactly one of them.
func TestHandlerMapsOneIPv6PrefixToOneUpstreamPseudonym(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	h := mustHandler(t, cfg)
	resolved := make(chan string, 3)
	// Only Linux routes the 127/8 aliases, so the test observes the key the
	// session resolves rather than the address it binds. nil keeps the dial on
	// the platform default, which is what every other system does anyway.
	h.upstreamLocalAddress = func(source string) net.IP {
		resolved <- source
		return nil
	}
	server := rotatingSourceServer(t, h, "2001:db8::1", "2001:db8::dead:beef", "2001:db8:0:1::1")
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{Subprotocol},
	}

	keys := make([]string, 0, 3)
	for session := range 3 {
		conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
		if err != nil {
			t.Fatalf("dial %d: %v", session, err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		select {
		case key := <-resolved:
			keys = append(keys, key)
		case <-time.After(3 * time.Second):
			t.Fatalf("session %d never resolved an upstream address", session)
		}
	}

	if keys[0] != "2001:db8::/64" {
		t.Fatalf("upstream key = %q, want 2001:db8::/64", keys[0])
	}
	if keys[1] != keys[0] {
		t.Fatalf("a rotated address took a second autoban bucket: %q and %q", keys[0], keys[1])
	}
	if keys[2] == keys[0] {
		t.Fatalf("another /64 shares the bucket %q", keys[2])
	}

	key := sourceAddressKey([]byte(testCredential("server secret")))
	if !pseudonymousLoopback(key, keys[0]).Equal(pseudonymousLoopback(key, keys[1])) {
		t.Fatal("one source key produced two loopback aliases")
	}
}

// rotatingSourceServer labels each request with the next address, so a test can
// drive one relay from sources it chooses. httptest dials over loopback, and
// RemoteAddr is the only place the handler learns a source from.
func rotatingSourceServer(t *testing.T, handler http.Handler, addresses ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	next := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		address := addresses[len(addresses)-1]
		if next < len(addresses) {
			address = addresses[next]
		}
		next++
		mu.Unlock()
		r.RemoteAddr = net.JoinHostPort(address, "40000")
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}
