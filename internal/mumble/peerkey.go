package mumble

import (
	"strconv"

	"github.com/LywwKkA-aD/gumble/gumble"
)

// How this client remembers one peer across a conversation.
//
// The engine keys per-peer volume and the local mute by something, and until
// now that something was the certificate hash Murmur computes and sends back.
// It is the right key when it exists: it survives the peer reconnecting,
// because a new session id is the same person.
//
// It does not always exist. A peer who presents no certificate gets an empty
// hash, and every empty hash is the same string - so one entry in the map
// would stand for every anonymous peer in the room at once. Nothing crashes;
// the volume of one stranger simply becomes the volume of all of them.
//
// That was not reachable while every Gul client carried a certificate to
// Murmur. It became reachable the moment the tunnel contract took the client's
// TLS session away and every session went anonymous, and it will stay
// reachable afterwards: the official Mumble client can connect without a
// certificate, and so can a phone.
//
// So the key falls back, and each rung says what it promises:
//
//   - h: the certificate hash. Survives reconnects and restarts.
//   - u: the registration id, for a peer registered on the server without a
//     certificate of their own. Also survives.
//   - s: the session id, which survives nothing. Murmur reuses session ids, so
//     a setting kept under one of these would eventually land on a stranger.
//     Whoever stores against an s: key has to drop it when the peer leaves.
const (
	peerKeyHash    = "h:"
	peerKeyUser    = "u:"
	peerKeySession = "s:"
)

// peerKey is what this client files a peer's audio settings under.
//
// Call it on the read loop only. gumble's User fields are stable there and
// nowhere else, which is the same rule OnAudioStream already follows.
func peerKey(user *gumble.User) string {
	if user == nil {
		return ""
	}
	if user.Hash != "" {
		return peerKeyHash + user.Hash
	}
	if user.UserID > 0 {
		return peerKeyUser + strconv.FormatUint(uint64(user.UserID), 10)
	}
	return peerKeySession + strconv.FormatUint(uint64(user.Session), 10)
}

// peerKeyIsMortal reports whether a key stands for this connection only, and
// therefore has to be forgotten when the peer goes.
func peerKeyIsMortal(key string) bool {
	return len(key) >= len(peerKeySession) && key[:len(peerKeySession)] == peerKeySession
}
