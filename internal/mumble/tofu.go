package mumble

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	log  *slog.Logger
	path string
	// persist is false once the database is known to be unusable on disk.
	// The session then keeps its pins in memory: a config directory the app
	// cannot read or write must degrade trust to first-use, never block the
	// application or every connection it makes.
	persist bool
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

// NewTOFUStore loads the pin database from configDir.
//
// It never fails. A damaged, unreadable or unwritable database costs pins -
// the affected hosts fall back to a first-use decision - and that price is
// paid per host: a store problem must never keep the application from
// starting, because a client that does not start cannot even be diagnosed.
// Dropped entries are reported by count and reason; host names stay out of
// the record so a shared diagnostics archive carries no connection arguments.
func NewTOFUStore(configDir string, log *slog.Logger) *TOFUStore {
	if log == nil {
		log = slog.Default()
	}
	s := &TOFUStore{
		log:     log,
		path:    filepath.Join(configDir, tofuFileName),
		persist: true,
		known:   map[string]string{},
		pending: map[string]string{},
	}

	data, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return s
	case err != nil:
		// The file exists but cannot be read. Writing over it would destroy
		// pins that are still there, so this session stays in memory.
		s.persist = false
		s.log.Warn("known servers unreadable, pins are session-scoped", "file", tofuFileName, "error", err)
		return s
	}

	stored := map[string]string{}
	if err := json.Unmarshal(data, &stored); err != nil {
		s.log.Warn("known servers damaged, starting a new database", "file", tofuFileName, "error", err)
		return s
	}

	canonical, dropped, changed := canonicalizeKnownServers(stored)
	if dropped.invalidHosts > 0 {
		s.log.Warn("dropped known servers: host cannot be canonicalized",
			"file", tofuFileName, "count", dropped.invalidHosts)
	}
	if dropped.conflicts > 0 {
		s.log.Warn("dropped known servers: equivalent host spellings pin different fingerprints",
			"file", tofuFileName, "count", dropped.conflicts)
	}
	s.known = canonical
	if changed {
		if err := s.saveKnown(canonical); err != nil {
			s.persist = false
			s.log.Warn("known servers not writable, pins are session-scoped",
				"file", tofuFileName, "error", err)
		}
	}
	return s
}

// droppedPins counts the entries a migration had to discard, by reason.
type droppedPins struct {
	invalidHosts int
	conflicts    int
}

// canonicalizeKnownServers preserves pins created before endpoint host
// canonicalization was introduced. Equivalent legacy spellings collapse to one
// key; an entry whose host cannot be canonicalized at all, and every spelling
// of a host whose duplicates disagree, are dropped. Dropping is fail-closed
// per host - that host loses its pin and is decided again on first use - and
// deliberately not fail-closed per application.
func canonicalizeKnownServers(stored map[string]string) (map[string]string, droppedPins, bool) {
	canonical := make(map[string]string, len(stored))
	conflicting := map[string]bool{}
	dropped := droppedPins{}
	changed := false

	for host, fingerprint := range stored {
		canonicalName, err := canonicalHost(host)
		if err != nil {
			dropped.invalidHosts++
			changed = true
			continue
		}
		if canonicalName != host {
			changed = true
		}
		if pinned, ok := canonical[canonicalName]; ok {
			if pinned != fingerprint {
				conflicting[canonicalName] = true
			}
			changed = true
			continue
		}
		canonical[canonicalName] = fingerprint
	}

	for canonicalName := range conflicting {
		delete(canonical, canonicalName)
		dropped.conflicts++
	}
	return canonical, dropped, changed
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

// saveKnown persists the database, unless startup already established that the
// file is not usable. A write that fails at runtime stays an error: the caller
// must not publish a pin it could not record.
func (s *TOFUStore) saveKnown(known map[string]string) error {
	if !s.persist {
		return nil
	}
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
