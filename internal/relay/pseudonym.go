package relay

import (
	"crypto/hmac"
	"crypto/sha256"
	"net"
	"runtime"
)

const sourceAddressDomain = "gul-relay-v1 source-address"

// sourceAddressKey derives the HMAC key behind the pseudonyms from the expected
// v2 bearer credential, so the mapping from source block to loopback alias
// cannot be reproduced without the relay secret. It keys the mapping; sourceKey
// produces the values that go into it.
func sourceAddressKey(credential []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, credential)
	_, _ = mac.Write([]byte(sourceAddressDomain))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}

// pseudonymousUpstreamAddress resolves the local address an upstream dial
// binds for one folded source key, or nil where the platform cannot route it.
//
// Linux routes the whole 127/8 block locally, which is what gives Murmur a
// distinct autoban bucket per source block. Other operating systems assign
// only 127.0.0.1, so every session there shares Murmur's ordinary loopback
// identity and nothing may be bound.
func (h *Handler) pseudonymousUpstreamAddress(source string) net.IP {
	if runtime.GOOS != "linux" {
		return nil
	}
	return pseudonymousLoopback(h.pseudonymKey, source)
}

// pseudonymousLoopback maps one source key to a stable address inside 127/8,
// used as the local address of the upstream connection. The argument is the
// folded key from sourceKey, never a raw address: an IPv6 subscriber who
// rotates addresses inside a /64 would otherwise get a fresh Murmur autoban
// bucket per attempt.
func pseudonymousLoopback(key [sha256.Size]byte, source string) net.IP {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(source))
	return loopbackFromDigest(mac.Sum(nil))
}

// loopbackFromDigest folds a digest into a bindable 127/8 address. Three of
// the 2^24 candidates are unusable and must be remapped instead of handed out:
// Linux refuses to bind the network address 127.0.0.0 and the broadcast
// address 127.255.255.255 with EADDRNOTAVAIL, which would lock the affected
// sources out permanently, and 127.0.0.1 is Murmur's own identity. The
// replacements are ordinary members of the range, so a rare collision with a
// naturally derived address only shares one autoban bucket.
func loopbackFromDigest(digest []byte) net.IP {
	switch {
	case digest[0] == 0 && digest[1] == 0 && digest[2] == 0:
		return net.IPv4(127, 0, 0, 2)
	case digest[0] == 0 && digest[1] == 0 && digest[2] == 1:
		return net.IPv4(127, 0, 0, 3)
	case digest[0] == 255 && digest[1] == 255 && digest[2] == 255:
		return net.IPv4(127, 0, 0, 4)
	}
	return net.IPv4(127, digest[0], digest[1], digest[2])
}
