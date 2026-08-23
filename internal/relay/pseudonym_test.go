package relay

import (
	"net"
	"runtime"
	"testing"
)

func TestPseudonymousLoopbackIsStableAndSeparatesSourceIPs(t *testing.T) {
	key := sourceAddressKey([]byte(testCredential("relay secret")))
	first := pseudonymousLoopback(key, "192.0.2.10")
	again := pseudonymousLoopback(key, "192.0.2.10")
	other := pseudonymousLoopback(key, "198.51.100.20")

	if !first.IsLoopback() {
		t.Fatalf("source = %s, want loopback", first)
	}
	if !first.Equal(again) {
		t.Fatalf("mapping is not stable: %s != %s", first, again)
	}
	if first.Equal(other) {
		t.Fatalf("different source IPs collided: %s", first)
	}
	if first.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatal("pseudonym must not use Murmur's ordinary loopback identity")
	}
}

// TestLoopbackFromDigestAvoidsUnbindableAddresses covers the silent lockout:
// Linux refuses to bind 127.0.0.0 and 127.255.255.255, so a user whose source
// address hashed onto either one could never reach Murmur again.
func TestLoopbackFromDigestAvoidsUnbindableAddresses(t *testing.T) {
	tests := []struct {
		name   string
		digest []byte
		want   net.IP
	}{
		{name: "network address", digest: []byte{0, 0, 0}, want: net.IPv4(127, 0, 0, 2)},
		{name: "murmur identity", digest: []byte{0, 0, 1}, want: net.IPv4(127, 0, 0, 3)},
		{name: "broadcast address", digest: []byte{255, 255, 255}, want: net.IPv4(127, 0, 0, 4)},
		{name: "ordinary", digest: []byte{1, 2, 3}, want: net.IPv4(127, 1, 2, 3)},
		{name: "high ordinary", digest: []byte{255, 255, 254}, want: net.IPv4(127, 255, 255, 254)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := loopbackFromDigest(tc.digest); !got.Equal(tc.want) {
				t.Fatalf("loopbackFromDigest(%v) = %s, want %s", tc.digest, got, tc.want)
			}
		})
	}
}

func TestLoopbackFromDigestNeverReturnsAnUnusableAddress(t *testing.T) {
	unusable := []net.IP{net.IPv4(127, 0, 0, 0), net.IPv4(127, 0, 0, 1), net.IPv4(127, 255, 255, 255)}
	digest := make([]byte, 3)
	for first := range 256 {
		for second := range 256 {
			for _, third := range []byte{0, 1, 2, 127, 254, 255} {
				digest[0], digest[1], digest[2] = byte(first), byte(second), third
				got := loopbackFromDigest(digest)
				for _, forbidden := range unusable {
					if got.Equal(forbidden) {
						t.Fatalf("digest %v mapped to %s", digest, got)
					}
				}
			}
		}
	}
}

// TestLoopbackRemapsAreBindable proves the remapped addresses are the usable
// ones. The whole mechanism is Linux-only: elsewhere the kernel assigns just
// 127.0.0.1 and the relay does not bind a local address at all.
func TestLoopbackRemapsAreBindable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux routes the whole 127/8 block locally")
	}
	for _, digest := range [][]byte{{0, 0, 0}, {0, 0, 1}, {255, 255, 255}} {
		address := loopbackFromDigest(digest)
		listener, err := net.Listen("tcp", net.JoinHostPort(address.String(), "0"))
		if err != nil {
			t.Fatalf("bind %s: %v", address, err)
		}
		_ = listener.Close()
	}
}
