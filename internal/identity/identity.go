// Package identity derives the certificate a Gul user is known by on one
// server, from a secret that never leaves their machine.
//
// It exists because removing the nested TLS took the user's identity with it.
// The client used to hold an RSA key and sign the handshake to Murmur itself;
// now the relay speaks that TLS, and a relay cannot sign with a key it does not
// have. Every session became anonymous, and Murmur reports an empty User.Hash
// for all of them.
//
// The obvious repair - the client signs on request, the relay asks - was
// designed and rejected. It is a signing oracle: the relay chooses the digest
// and the client cannot see what it covers, so a compromised relay can have you
// sign a handshake to somebody else's server and be you there. On TLS 1.3 the
// signer is handed a hash and cannot tell one destination from another at all.
// Closing that needs a hostile-transcript parser as the only guard, TLS 1.2
// forced instead of 1.3, and a 1798-byte request frame - eight cells - which is
// the same downward burst that removing the nested handshake got rid of. The
// transcript is the handshake; sending it back is sending it twice.
//
// So the secret travels instead of the signature, and it travels scoped:
//
//	master  32 bytes, on this machine only, one per user
//	host    HKDF(master, "host:" + server) -> 32 bytes, one per server
//	key     ed25519.NewKeyFromSeed(host)
//
// The relay is given the host seed and rebuilds the same pair with this same
// code. What that costs is exact and has to be said in full: the relay holds
// the key to your identity on its own server and can be you there, without
// you, for as long as it likes. What it cannot do is be you anywhere else -
// the master never moves, and a host seed is useless to any other server.
//
// That trade is not free and is not hidden. It is taken because the relay
// already terminates your Mumble session, reads your messages and sees the
// server password go past; a relay that is compromised has your conversation
// either way. The line it must not cross is the one this scoping draws: your
// identity on servers it has nothing to do with.
//
// Both ends import this package. That is the guarantee the bytes do not
// diverge - Murmur hashes the whole DER, so a template that differs by one
// field between client and relay gives the user a different name than the one
// their client is expecting, and nothing would say so.
package identity

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// SeedBytes is the size of both secrets: the master on disk and the per-host
// seed on the wire. Ed25519 takes exactly this.
const SeedBytes = ed25519.SeedSize

const (
	// commonName is what Murmur shows for a client that is not registered. It
	// was "Gul" when the client issued its own certificate and stays "Gul"
	// here: it is displayed, and changing it would rename everybody.
	commonName = "Gul"
	// hostInfo and serialInfo separate the two things derived from one seed.
	hostInfo   = "gul/identity/v1 host"
	serialInfo = "gul/identity/v1 serial"
)

// notBefore and notAfter are fixed, not computed.
//
// A certificate built from time.Now would have a different DER every time it
// was built, so the client and the relay would derive different bytes for the
// same user and Murmur would see a different person on every connection. The
// dates say nothing about anyone; they exist because the field is mandatory.
var (
	notBefore = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	notAfter  = time.Date(2049, time.December, 31, 23, 59, 59, 0, time.UTC)
)

// Identity is one user as one server knows them.
type Identity struct {
	// Certificate is what gets presented to Murmur.
	Certificate tls.Certificate
	// Fingerprint is SHA-1 over the whole DER, in lower-case hex: the value
	// Murmur computes and hands back as User.Hash.
	//
	// The client works this out before it connects, which is what turns the
	// hash from something the relay reports into something the client can
	// check. A relay that presented Murmur a different certificate is caught
	// by comparing this against what comes back.
	Fingerprint string
}

// HostSeed is the secret for one server: what the client sends and the relay
// rebuilds from. Deriving per host is the whole containment - a relay that
// holds this one can be you on its own server and nowhere else.
func HostSeed(master []byte, host string) ([]byte, error) {
	if len(master) != SeedBytes {
		return nil, fmt.Errorf("identity: master seed is %d bytes, want %d", len(master), SeedBytes)
	}
	if host == "" {
		return nil, errors.New("identity: a host is required to scope the seed")
	}
	return hkdf.Key(sha256.New, master, nil, hostInfo+" "+host, SeedBytes)
}

// FromHostSeed builds the identity a host seed stands for.
//
// This is the function both ends call, and it is the reason they agree. Every
// input to the certificate comes from the seed or from a constant above:
// nothing reads a clock, and nothing reads the random source - Ed25519 signing
// is deterministic, so x509.CreateCertificate needs none and accepts nil.
func FromHostSeed(seed []byte) (Identity, error) {
	if len(seed) != SeedBytes {
		return Identity{}, fmt.Errorf("identity: host seed is %d bytes, want %d", len(seed), SeedBytes)
	}
	key := ed25519.NewKeyFromSeed(seed)

	serial, err := hkdf.Key(sha256.New, seed, nil, serialInfo, 8)
	if err != nil {
		return Identity{}, fmt.Errorf("identity: serial: %w", err)
	}
	template := &x509.Certificate{
		// Positive and eight bytes: an RFC 5280 serial is a positive integer,
		// and the high bit is cleared rather than the number rejected so that
		// every seed yields a certificate.
		SerialNumber: new(big.Int).SetBytes(append([]byte{serial[0] & 0x7f}, serial[1:]...)),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		return Identity{}, fmt.Errorf("identity: certificate: %w", err)
	}

	// SHA-1 because that is what Murmur computes over the DER and reports as
	// User.Hash. It is a name here, not a signature: the certificate is
	// self-signed with Ed25519, and nothing trusts this digest for anything
	// except telling one peer from another.
	sum := sha1.Sum(der)
	return Identity{
		Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		Fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

// ForHost is the client's path: master to identity in one step, so nothing
// upstream has to know that a host seed exists.
func ForHost(master []byte, host string) (Identity, error) {
	seed, err := HostSeed(master, host)
	if err != nil {
		return Identity{}, err
	}
	return FromHostSeed(seed)
}
