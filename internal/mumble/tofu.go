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
	return s, nil
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
		s.known[host] = fingerprint
		delete(s.pending, host)
		return s.save()
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
	s.known[host] = fingerprint
	delete(s.pending, host)
	return s.save()
}

func (s *TOFUStore) save() error {
	data, err := json.MarshalIndent(s.known, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tofuFileName, err)
	}
	return os.Rename(tmp, s.path)
}
