// Package relayproto defines the narrow wire contract shared by the Gul app
// and its fixed-target WSS relay: the path, the subprotocol, the message
// size bound and the bearer credential derivation.
package relayproto

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const (
	// MaxMessageBytes bounds one WebSocket message in either direction. The
	// client sends one whole Mumble TCP packet per message, so the bound has
	// to clear the largest packet murmur legitimately carries (images in
	// chat, user textures: hundreds of KiB), while staying far below
	// gumble's 10 MiB MaximumPacketBytes. Both sides MUST apply it: the
	// websocket library disables its own limit when a connection is
	// adapted into a net.Conn.
	MaxMessageBytes = 1 << 20

	// bearerIterations is the PBKDF2 work factor (OWASP 2023 guidance for
	// PBKDF2-HMAC-SHA256). Derivation costs on the order of 100 ms: derive
	// once per process or per password, never per request or per frame.
	bearerIterations = 600_000

	bearerSalt  = "gul-relay-v2 bearer"
	namesDomain = "gul-relay-v2 tunnel names"
	v2Prefix    = "v2."
)

// Names are the address and the subprotocol one server's tunnel answers on.
//
// They carry no meaning of their own: both are derived from the credential, so
// every server has its own pair, and a pair tells an observer nothing except
// that some site has a WebSocket endpoint - which describes a large share of
// the web. Only somebody who already knows the server password can work out
// where the tunnel is, and knowing that still gets them a "not found" without
// the credential.
type Names struct {
	Path string
	// Subprotocol is the plain byte stream, Shaped the framed one that pads
	// every write to a fixed grid (shaped.go). Two names rather than a version
	// byte inside the tunnel, because the WebSocket handshake already
	// negotiates exactly this: the client offers both, newest first, and an
	// older relay that only knows the plain one picks it without either side
	// needing to ask.
	Subprotocol string
	Shaped      string
	// Tunnel is the contract that carries no nested TLS: a short
	// authenticated handshake and then records, instead of a second TLS
	// session inside the first. It is a third name rather than a version byte
	// for the same reason the second one was - the WebSocket handshake
	// already negotiates exactly this - and it costs nothing to derive,
	// because NamesFor had twelve bytes of its HMAC spare.
	Tunnel string
}

// NamesFor derives the tunnel names from the credential.
//
// The credential is already a stretched secret (Derive spends 600k PBKDF2
// iterations on it), so one HMAC over it is enough here and is cheap enough to
// run at startup on both sides. It is one-way: the path travels in clear text
// inside the TLS session, and it must not hand back the credential that
// produced it. It reveals nothing new in any case - an observer who can read
// the path is reading the Authorization header in the same request.
func NamesFor(c Credential) Names {
	mac := hmac.New(sha256.New, []byte(c))
	_, _ = mac.Write([]byte(namesDomain))
	sum := mac.Sum(nil)
	return Names{
		Path:        "/ws/" + hex.EncodeToString(sum[:8]),
		Subprotocol: hex.EncodeToString(sum[8:14]),
		Shaped:      hex.EncodeToString(sum[14:20]),
		Tunnel:      hex.EncodeToString(sum[20:26]),
	}
}

// Credential is a derived bearer token without the "Bearer " scheme.
//
// It is a static shared secret: every client derives the same value from
// the server join password, it is replayable by design, and HTTPS is what
// keeps it confidential in transit. What the derivation buys is that a
// leaked header does not hand over the Mumble password - recovering it
// means brute-forcing the password through the PBKDF2 work factor.
type Credential string

// Derive computes the current (v2) bearer credential from the server join
// password. Expensive by design; cache the result.
func Derive(secret []byte) Credential {
	key, err := pbkdf2.Key(sha256.New, string(secret), []byte(bearerSalt), bearerIterations, 32)
	if err != nil {
		// pbkdf2.Key only fails on a zero-length key or iteration count,
		// both fixed constants here; a failure is a programming error.
		panic("relayproto: pbkdf2 derivation failed: " + err.Error())
	}
	return Credential(v2Prefix + base64.RawURLEncoding.EncodeToString(key))
}

// Header renders the credential as an Authorization header value.
func (c Credential) Header() string { return "Bearer " + string(c) }

// Legacy reports whether the credential predates the v2 scheme: a bare
// single-HMAC value, cheap enough to brute-force offline from a leaked header.
// Nothing derives one any more - the relay only recognizes the shape, so it can
// refuse it and say so in the log rather than call it malformed.
func (c Credential) Legacy() bool { return !strings.HasPrefix(string(c), v2Prefix) }

// Matches compares two credentials in constant time (over SHA-256 digests,
// so differing lengths leak nothing either).
func (c Credential) Matches(other Credential) bool {
	a := sha256.Sum256([]byte(c))
	b := sha256.Sum256([]byte(other))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// ParseHeader extracts the credential from an Authorization header value,
// accepting only the strict single-token Bearer shape with a well-formed
// token: an optional v2 prefix followed by unpadded base64url. Anything
// else is rejected before any comparison happens.
func ParseHeader(header string) (Credential, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	body := strings.TrimPrefix(token, v2Prefix)
	if body == "" || len(body) > 128 || !validBase64URL(body) {
		return "", false
	}
	return Credential(token), true
}

func validBase64URL(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// The QUIC tunnel carries the same bytes as the WebSocket one, over a single
// bidirectional stream, and authenticates with the same credential.
//
// ALPN is "h3" because a QUIC Initial packet is not secret: it is protected
// with keys anyone can derive from the connection ID and the version salt, so
// the SNI and the protocol name travel in the open. A name of our own would be
// visible and unique, which is the opposite of the point; "h3" is the most
// numerous thing on UDP 443. Making the tunnel survive an active prober that
// speaks real HTTP/3 to it is a separate piece of work.
const (
	QUICALPN = "h3"
	// quicPreambleMax bounds the credential a stream may present before it is
	// read, so an opening peer cannot make the relay buffer anything.
	quicPreambleMax = 160
)

// WriteQUICPreamble states who is calling, ahead of the tunnel bytes: a
// two-byte length and the credential.
func WriteQUICPreamble(w io.Writer, c Credential) error {
	if len(c) == 0 || len(c) > quicPreambleMax {
		return errors.New("relayproto: credential does not fit the preamble")
	}
	frame := make([]byte, 2+len(c))
	binary.BigEndian.PutUint16(frame, uint16(len(c)))
	copy(frame[2:], c)
	_, err := w.Write(frame)
	return err
}

// ReadQUICPreamble reads what WriteQUICPreamble wrote. A length beyond the
// bound is refused before a single byte of it is read.
func ReadQUICPreamble(r io.Reader) (Credential, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", err
	}
	size := int(binary.BigEndian.Uint16(header[:]))
	if size == 0 || size > quicPreambleMax {
		return "", errors.New("relayproto: preamble length out of range")
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return "", err
	}
	return Credential(body), nil
}
