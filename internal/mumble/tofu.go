package mumble

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const tofuFileName = "known_servers.json"

// TOFUStore implements trust-on-first-use for server certificates, mirroring
// the Mumble model: self-signed certificates are the norm, so instead of CA
// verification we pin the SHA-256 fingerprint of the leaf certificate on the
// first connection and reject any later change.
type TOFUStore struct {
	mu   sync.Mutex
	path string
	// host -> lowercase hex SHA-256 of the leaf certificate DER
	known map[string]string
	// host -> fingerprint that was rejected on the last mismatch. Kept in
	// memory only: it is a prompt candidate, not a trust decision.
	pending map[string]string
}

// ErrFingerprintChanged is reported when a previously pinned server presents a
// different certificate. Match it with errors.Is; use errors.As with
// *MismatchError to recover both fingerprints for the user prompt.
var ErrFingerprintChanged = errors.New("server certificate changed since first use")

// MismatchError carries the pinned and the presented fingerprint so the caller
// can build a TOFU prompt without parsing an error string.
type MismatchError struct {
	Host      string
	Pinned    string
	Presented string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("%s: host %s pinned %s, got %s",
		ErrFingerprintChanged.Error(), e.Host, e.Pinned, e.Presented)
}

// Is makes errors.Is(err, ErrFingerprintChanged) succeed for this type.
func (e *MismatchError) Is(target error) bool { return target == ErrFingerprintChanged }

func NewTOFUStore(configDir string) (*TOFUStore, error) {
	s := &TOFUStore{
		path:    filepath.Join(configDir, tofuFileName),
		known:   map[string]string{},
		pending: map[string]string{},
	}
	data, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", tofuFileName, err)
	}
	if err := json.Unmarshal(data, &s.known); err != nil {
		return nil, fmt.Errorf("parse %s: %w", tofuFileName, err)
	}
	canonical, changed, err := canonicalizeKnownServers(s.known)
	if err != nil {
		return nil, fmt.Errorf("migrate %s: %w", tofuFileName, err)
	}
	if changed {
		if err := s.saveKnown(canonical); err != nil {
			return nil, fmt.Errorf("migrate %s: %w", tofuFileName, err)
		}
	}
	s.known = canonical
	return s, nil
}

// canonicalizeKnownServers preserves pins created before endpoint host
// canonicalization was introduced. Equivalent legacy spellings must collapse
// to one key; conflicting fingerprints fail closed so an upgrade can never
// turn an existing pin into a fresh first-use trust decision.
func canonicalizeKnownServers(known map[string]string) (map[string]string, bool, error) {
	canonical := make(map[string]string, len(known))
	changed := false
	for host, fingerprint := range known {
		canonicalName, err := canonicalHost(host)
		if err != nil {
			return nil, false, errors.New("contains a server host that cannot be canonicalized")
		}
		if pinned, ok := canonical[canonicalName]; ok {
			if pinned != fingerprint {
				return nil, false, errors.New("contains conflicting pins for an equivalent server host")
			}
			changed = true
			continue
		}
		canonical[canonicalName] = fingerprint
		if canonicalName != host {
			changed = true
		}
	}
	return canonical, changed, nil
}

// TLSConfig returns a tls.Config that accepts the pinned certificate for host
// (pinning it on first contact) and rejects everything else.
func (s *TOFUStore) TLSConfig(host string) *tls.Config {
	return &tls.Config{
		// CA verification is intentionally disabled: trust is established by
		// fingerprint pinning in VerifyPeerCertificate below.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("server presented no certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			return s.verify(host, hex.EncodeToString(sum[:]))
		},
	}
}

func (s *TOFUStore) verify(host, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pinned, ok := s.known[host]
	if !ok {
		candidate := knownWithPin(s.known, host, fingerprint)
		if err := s.saveKnown(candidate); err != nil {
			return err
		}
		s.known = candidate
		delete(s.pending, host)
		return nil
	}
	if pinned != fingerprint {
		// Remember the candidate so the prompt survives an error value that
		// gets flattened somewhere between crypto/tls and the caller.
		s.pending[host] = fingerprint
		return &MismatchError{Host: host, Pinned: pinned, Presented: fingerprint}
	}
	delete(s.pending, host)
	return nil
}

// Fingerprint returns the fingerprint currently pinned for host.
func (s *TOFUStore) Fingerprint(host string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, ok := s.known[host]
	return fp, ok
}

// Pending returns the fingerprint rejected by the last mismatch for host. It is
// the candidate the user is asked to accept, and is cleared once the outcome is
// decided (Replace, or a later successful verification).
func (s *TOFUStore) Pending(host string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, ok := s.pending[host]
	return fp, ok
}

// Replace pins fingerprint for host, overriding whatever was pinned before.
// It implements the user accepting a changed server certificate, so it must
// only be called after an explicit confirmation.
func (s *TOFUStore) Replace(host, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := knownWithPin(s.known, host, fingerprint)
	if err := s.saveKnown(candidate); err != nil {
		return err
	}
	s.known = candidate
	delete(s.pending, host)
	return nil
}

func knownWithPin(known map[string]string, host, fingerprint string) map[string]string {
	candidate := make(map[string]string, len(known)+1)
	for existingHost, existingFingerprint := range known {
		candidate[existingHost] = existingFingerprint
	}
	candidate[host] = fingerprint
	return candidate
}

func (s *TOFUStore) saveKnown(known map[string]string) error {
	data, err := json.MarshalIndent(known, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tofuFileName, err)
	}
	return os.Rename(tmp, s.path)
}
