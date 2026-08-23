package mumble

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestTOFUPinAndMatch(t *testing.T) {
	s := NewTOFUStore(t.TempDir(), testLogger(t))

	if err := s.verify("srv:64738", "aaaa"); err != nil {
		t.Fatalf("first use must pin, got error: %v", err)
	}
	if err := s.verify("srv:64738", "aaaa"); err != nil {
		t.Fatalf("same fingerprint must pass, got: %v", err)
	}
	if err := s.verify("srv:64738", "bbbb"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("changed fingerprint must fail with ErrFingerprintChanged, got: %v", err)
	}
}

func TestTOFUPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()

	s1 := NewTOFUStore(dir, testLogger(t))
	if err := s1.verify("host", "cafe"); err != nil {
		t.Fatal(err)
	}

	s2 := NewTOFUStore(dir, testLogger(t))
	if err := s2.verify("host", "cafe"); err != nil {
		t.Fatalf("pinned fingerprint must survive reload, got: %v", err)
	}
	if err := s2.verify("host", "dead"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("mismatch after reload must fail, got: %v", err)
	}
}

func TestTOFUMismatchCarriesBothFingerprints(t *testing.T) {
	s := NewTOFUStore(t.TempDir(), testLogger(t))
	if err := s.verify("host", "old"); err != nil {
		t.Fatal(err)
	}

	err := s.verify("host", "new")

	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("mismatch must be recoverable with errors.As, got %T: %v", err, err)
	}
	if mismatch.Host != "host" || mismatch.Pinned != "old" || mismatch.Presented != "new" {
		t.Fatalf("mismatch = %+v, want host/old/new", mismatch)
	}
	if !errors.Is(err, ErrFingerprintChanged) {
		t.Fatal("mismatch must still match ErrFingerprintChanged")
	}
}

func TestTOFUPendingHoldsRejectedCandidate(t *testing.T) {
	s := NewTOFUStore(t.TempDir(), testLogger(t))
	if err := s.verify("host", "old"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Pending("host"); ok {
		t.Fatal("nothing is pending before a mismatch")
	}

	_ = s.verify("host", "new")

	pending, ok := s.Pending("host")
	if !ok || pending != "new" {
		t.Fatalf("Pending = (%q, %v), want (new, true)", pending, ok)
	}
}

func TestTOFUReplaceAcceptsNewFingerprint(t *testing.T) {
	dir := t.TempDir()

	s := NewTOFUStore(dir, testLogger(t))
	if err := s.verify("host", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.verify("host", "new"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("expected a mismatch, got: %v", err)
	}

	s.Replace("host", "new")
	if _, ok := s.Pending("host"); ok {
		t.Fatal("Replace must clear the pending candidate")
	}

	if err := s.verify("host", "new"); err != nil {
		t.Fatalf("accepted fingerprint must verify, got: %v", err)
	}
	if err := s.verify("host", "old"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("the superseded fingerprint must now be rejected, got: %v", err)
	}

	reloaded := NewTOFUStore(dir, testLogger(t))
	if err := reloaded.verify("host", "new"); err != nil {
		t.Fatalf("the accepted fingerprint must survive reload, got: %v", err)
	}
}

func TestTOFUFingerprintReportsPin(t *testing.T) {
	s := NewTOFUStore(t.TempDir(), testLogger(t))

	if _, ok := s.Fingerprint("host"); ok {
		t.Fatal("an unknown host has no pin")
	}
	if err := s.verify("host", "abcd"); err != nil {
		t.Fatal(err)
	}

	fp, ok := s.Fingerprint("host")
	if !ok || fp != "abcd" {
		t.Fatalf("Fingerprint = (%q, %v), want (abcd, true)", fp, ok)
	}
}

func TestTOFUCanonicalizesLegacyHostKeysAndPersistsMigration(t *testing.T) {
	dir := t.TempDir()
	writeKnownServers(t, dir, map[string]string{
		"MURMUR.EXAMPLE.COM.":    "dns-pin",
		"2001:0db8:0:0:0:0:0:1":  "ipv6-pin",
		"same-pin.example.test":  "shared-pin",
		"SAME-PIN.EXAMPLE.TEST.": "shared-pin",
	})

	store := NewTOFUStore(dir, testLogger(t))
	want := map[string]string{
		"murmur.example.com":    "dns-pin",
		"2001:db8::1":           "ipv6-pin",
		"same-pin.example.test": "shared-pin",
	}
	if !reflect.DeepEqual(store.known, want) {
		t.Fatalf("migrated pins = %#v, want %#v", store.known, want)
	}

	persisted := readKnownServers(t, dir)
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted pins = %#v, want %#v", persisted, want)
	}
	if _, err := os.Stat(filepath.Join(dir, tofuFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("migration left temporary file behind: %v", err)
	}
}

func TestTOFUDropsConflictingCanonicalLegacyPins(t *testing.T) {
	dir := t.TempDir()
	writeKnownServers(t, dir, map[string]string{
		"MURMUR.EXAMPLE.COM.": "first-pin",
		"murmur.example.com":  "different-pin",
		"other.example.test":  "kept-pin",
	})

	store := NewTOFUStore(dir, testLogger(t))

	// Fail-closed for the ambiguous host only: it is decided again on first
	// use, while every unambiguous pin survives and the app still starts.
	if fingerprint, ok := store.Fingerprint("murmur.example.com"); ok {
		t.Fatalf("conflicting host kept pin %q", fingerprint)
	}
	if fingerprint, ok := store.Fingerprint("other.example.test"); !ok || fingerprint != "kept-pin" {
		t.Fatalf("unrelated pin = (%q, %v), want (kept-pin, true)", fingerprint, ok)
	}
	if got := readKnownServers(t, dir); !reflect.DeepEqual(got, map[string]string{"other.example.test": "kept-pin"}) {
		t.Fatalf("persisted pins = %#v, want only the unambiguous one", got)
	}
}

// TestTOFUFirstUseOnAnUnwritableDirectoryConnects is the fresh install on a
// read-only config directory: no store file exists, none can be created, and
// the very first pin therefore cannot be written. Trust degrades to the
// session; refusing the connection instead would leave the user with a client
// that can never connect to anything.
func TestTOFUFirstUseOnAnUnwritableDirectoryConnects(t *testing.T) {
	dir := t.TempDir()
	denyWrites(t, dir)

	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))

	if err := store.verify("murmur.example.test", "session-pin"); err != nil {
		t.Fatalf("first use on an unwritable config directory: %v", err)
	}
	if fingerprint, ok := store.Fingerprint("murmur.example.test"); !ok || fingerprint != "session-pin" {
		t.Fatalf("pin = (%q, %v), want (session-pin, true) for this session", fingerprint, ok)
	}
	// Session-scoped trust is still trust: the same certificate passes, a
	// different one is still a mismatch.
	if err := store.verify("murmur.example.test", "session-pin"); err != nil {
		t.Fatalf("pinned fingerprint must verify: %v", err)
	}
	if err := store.verify("murmur.example.test", "other-pin"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("changed fingerprint = %v, want ErrFingerprintChanged", err)
	}
	if !strings.Contains(records.String(), "session-scoped") {
		t.Fatalf("degradation was not reported: %s", records.String())
	}
}

func TestTOFUFirstUseSurvivesABlockedSave(t *testing.T) {
	dir := t.TempDir()
	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))
	blockTOFUSave(t, dir)

	if err := store.verify("murmur.example.test", "new-pin"); err != nil {
		t.Fatalf("first use while persistence was blocked: %v", err)
	}
	if fingerprint, ok := store.Fingerprint("murmur.example.test"); !ok || fingerprint != "new-pin" {
		t.Fatalf("pin = (%q, %v), want (new-pin, true)", fingerprint, ok)
	}
	if _, ok := store.Pending("murmur.example.test"); ok {
		t.Fatal("a first use is not a pending replacement")
	}
	if !strings.Contains(records.String(), "session-scoped") {
		t.Fatalf("degradation was not reported: %s", records.String())
	}
}

// TestTOFUReplaceSurvivesABlockedSave: accepting a changed certificate is a
// decision the user just made in a dialog. A store that cannot record it keeps
// it for the session instead of discarding it and prompting again.
func TestTOFUReplaceSurvivesABlockedSave(t *testing.T) {
	dir := t.TempDir()
	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))
	if err := store.verify("murmur.example.test", "old-pin"); err != nil {
		t.Fatalf("pin old fingerprint: %v", err)
	}
	if err := store.verify("murmur.example.test", "new-pin"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("create pending replacement: %v", err)
	}
	blockTOFUSave(t, dir)

	store.Replace("murmur.example.test", "new-pin")

	if fingerprint, ok := store.Fingerprint("murmur.example.test"); !ok || fingerprint != "new-pin" {
		t.Fatalf("pin after replacement = (%q, %v), want (new-pin, true)", fingerprint, ok)
	}
	if _, ok := store.Pending("murmur.example.test"); ok {
		t.Fatal("Replace must clear the pending candidate")
	}
	if err := store.verify("murmur.example.test", "new-pin"); err != nil {
		t.Fatalf("the accepted fingerprint must verify: %v", err)
	}
	if !strings.Contains(records.String(), "session-scoped") {
		t.Fatalf("degradation was not reported: %s", records.String())
	}
}

func writeKnownServers(t *testing.T, dir string, known map[string]string) {
	t.Helper()
	data, err := json.MarshalIndent(known, "", "  ")
	if err != nil {
		t.Fatalf("marshal known servers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, tofuFileName), data, 0o600); err != nil {
		t.Fatalf("write known servers: %v", err)
	}
}

func readKnownServers(t *testing.T, dir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, tofuFileName))
	if err != nil {
		t.Fatalf("read known servers: %v", err)
	}
	var known map[string]string
	if err := json.Unmarshal(data, &known); err != nil {
		t.Fatalf("parse known servers: %v", err)
	}
	return known
}

func blockTOFUSave(t *testing.T, dir string) func() {
	t.Helper()
	path := filepath.Join(dir, tofuFileName+".tmp")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("block TOFU save: %v", err)
	}
	blocked := true
	t.Cleanup(func() {
		if blocked {
			_ = os.Remove(path)
		}
	})
	return func() {
		t.Helper()
		if !blocked {
			return
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("unblock TOFU save: %v", err)
		}
		blocked = false
	}
}

// TestTOFUKeepsStartingOnUncanonicalizableLegacyHost reproduces the pin an
// alpha.1 client recorded for a docker-compose service name: the underscore
// makes it fail host validation, which used to abort startup.
func TestTOFUKeepsStartingOnUncanonicalizableLegacyHost(t *testing.T) {
	dir := t.TempDir()
	writeKnownServers(t, dir, map[string]string{"mumble_server": "aa", "ok.example.test": "bb"})

	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))

	if store == nil {
		t.Fatal("a damaged pin database must not cost the application its start")
	}
	if fingerprint, ok := store.Fingerprint("mumble_server"); ok {
		t.Fatalf("invalid host kept pin %q", fingerprint)
	}
	if fingerprint, ok := store.Fingerprint("ok.example.test"); !ok || fingerprint != "bb" {
		t.Fatalf("valid pin = (%q, %v), want (bb, true)", fingerprint, ok)
	}
	logged := records.String()
	if !strings.Contains(logged, `"count":1`) {
		t.Fatalf("warning must report how many pins were dropped: %s", logged)
	}
	if strings.Contains(logged, "mumble_server") {
		t.Fatalf("warning leaked a server name into the log: %s", logged)
	}
	// The database is rewritten without the entry it cannot use.
	if got := readKnownServers(t, dir); !reflect.DeepEqual(got, map[string]string{"ok.example.test": "bb"}) {
		t.Fatalf("persisted pins = %#v", got)
	}
}

func TestTOFUKeepsStartingOnUnreadableDatabase(t *testing.T) {
	dir := t.TempDir()
	writeKnownServers(t, dir, map[string]string{"murmur.example.test": "aa"})
	denyAccess(t, filepath.Join(dir, tofuFileName))

	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))

	if _, ok := store.Fingerprint("murmur.example.test"); ok {
		t.Fatal("pins were reported from a database that could not be read")
	}
	if !strings.Contains(records.String(), "known servers unreadable") {
		t.Fatalf("degradation was not reported: %s", records.String())
	}
	// In-memory trust still works, and the unreadable file is left alone.
	if err := store.verify("murmur.example.test", "session-pin"); err != nil {
		t.Fatalf("first use in memory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, tofuFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("unreadable database was written to: %v", err)
	}
}

func TestTOFUKeepsStartingOnUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	// A migration is needed, so startup has to write - and cannot.
	writeKnownServers(t, dir, map[string]string{"MURMUR.EXAMPLE.TEST": "aa"})
	denyAccess(t, dir)

	var records bytes.Buffer
	store := NewTOFUStore(dir, slog.New(slog.NewJSONHandler(&records, nil)))

	if store == nil {
		t.Fatal("an unwritable config directory must not cost the application its start")
	}
	if !strings.Contains(records.String(), "known servers") {
		t.Fatalf("degradation was not reported: %s", records.String())
	}
	if err := store.verify("murmur.example.test", "aa"); err != nil {
		t.Fatalf("session-scoped trust must keep working: %v", err)
	}
}

func TestNewManagerStartsWithADamagedConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	writeKnownServers(t, dir, map[string]string{"mumble_server": "aa"})

	m, err := NewManager(dir, testLogger(t), Callbacks{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
}

func TestNewManagerStartsWithAnUnreadableConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	denyAccess(t, dir)

	m, err := NewManager(dir, testLogger(t), Callbacks{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	if len(m.cert.Certificate) == 0 {
		t.Fatal("a session identity must be available even without persistence")
	}
}

// denyWrites leaves path readable but not writable for the rest of the test:
// a config directory the client may inspect and may not add a file to.
func denyWrites(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", filepath.Base(path), err)
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatalf("deny writes to %s: %v", filepath.Base(path), err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
}

// denyAccess removes every permission bit from path for the rest of the test.
func denyAccess(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", filepath.Base(path), err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("deny access to %s: %v", filepath.Base(path), err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
}
