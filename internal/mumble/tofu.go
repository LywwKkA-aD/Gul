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
	// persist is false once the database is known to be unusable on disk -
	// unreadable at startup, or unwritable at any point, including the very
	// first pin on a fresh install. The session then keeps its pins in memory:
	// a config directory the app cannot read or write must degrade trust to
	// first-use, never block the application or the connections it makes.
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
		s.saveKnown(canonical)
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

// VerifyFingerprint pins a server certificate the client did not fetch itself.
//
// It exists because the tunnel contract took the fetching away: the relay
// opens Mumble TLS to the server now, and reports the leaf it found in the
// accept frame. The value pinned is the same one this store has always held -
// SHA-256 over the leaf's DER - so existing pins keep matching and nobody sees
// a fingerprint prompt because of the change.
//
// What changed is the standing of the claim, and it must not be blurred: when
// the client dialled the server itself, a mismatch meant the server's key had
// changed. Now it means the relay says the server's key has changed. It still
// catches that; it is no longer evidence against a relay that lies.
func (s *TOFUStore) VerifyFingerprint(host, fingerprint string) error {
	if fingerprint == "" {
		return fmt.Errorf("the relay named no certificate for %s", host)
	}
	return s.verify(host, fingerprint)
}

func (s *TOFUStore) verify(host, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pinned, ok := s.known[host]
	if !ok {
		candidate := knownWithPin(s.known, host, fingerprint)
		// First use establishes trust for this session whether or not the
		// decision can be recorded: a config directory that cannot be written
		// costs the pin its lifetime, never the connection.
		s.saveKnown(candidate)
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
//
// It cannot fail: the acceptance always holds for this session, and only its
// persistence depends on the store being writable.
func (s *TOFUStore) Replace(host, fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := knownWithPin(s.known, host, fingerprint)
	s.saveKnown(candidate)
	s.known = candidate
	delete(s.pending, host)
}

func knownWithPin(known map[string]string, host, fingerprint string) map[string]string {
	candidate := make(map[string]string, len(known)+1)
	for existingHost, existingFingerprint := range known {
		candidate[existingHost] = existingFingerprint
	}
	candidate[host] = fingerprint
	return candidate
}

// saveKnown persists the database. A write that fails costs persistence and
// nothing else: the store says so once, drops to session-scoped pins and keeps
// answering, because refusing to trust anything the client cannot write down
// makes an unwritable config directory an unusable application.
//
// Caller holds s.mu (construction is single-threaded).
func (s *TOFUStore) saveKnown(known map[string]string) {
	if !s.persist {
		return
	}
	if err := s.writeKnown(known); err != nil {
		s.persist = false
		s.log.Warn("known servers not writable, pins are session-scoped",
			"file", tofuFileName, "error", err)
	}
}

func (s *TOFUStore) writeKnown(known map[string]string) error {
	data, err := json.MarshalIndent(known, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tofuFileName, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		// Leaving the temporary file behind would keep the next attempt from
		// writing it, so the failure would outlive its cause.
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", tofuFileName, err)
	}
	return nil
}
