package mumble

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
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
}

// ErrFingerprintChanged is wrapped into the error returned when a previously
// pinned server presents a different certificate.
var ErrFingerprintChanged = fmt.Errorf("server certificate changed since first use")

func NewTOFUStore(configDir string) (*TOFUStore, error) {
	s := &TOFUStore{
		path:  filepath.Join(configDir, tofuFileName),
		known: map[string]string{},
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
		return s.save()
	}
	if pinned != fingerprint {
		return fmt.Errorf("%w: host %s pinned %s, got %s", ErrFingerprintChanged, host, pinned, fingerprint)
	}
	return nil
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
