package mumble

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTOFUPinAndMatch(t *testing.T) {
	s, err := NewTOFUStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

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

	s1, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.verify("host", "cafe"); err != nil {
		t.Fatal(err)
	}

	s2, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.verify("host", "cafe"); err != nil {
		t.Fatalf("pinned fingerprint must survive reload, got: %v", err)
	}
	if err := s2.verify("host", "dead"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("mismatch after reload must fail, got: %v", err)
	}
}

func TestTOFUMismatchCarriesBothFingerprints(t *testing.T) {
	s, err := NewTOFUStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verify("host", "old"); err != nil {
		t.Fatal(err)
	}

	err = s.verify("host", "new")

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
	s, err := NewTOFUStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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

	s, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verify("host", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.verify("host", "new"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("expected a mismatch, got: %v", err)
	}

	if err := s.Replace("host", "new"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, ok := s.Pending("host"); ok {
		t.Fatal("Replace must clear the pending candidate")
	}

	if err := s.verify("host", "new"); err != nil {
		t.Fatalf("accepted fingerprint must verify, got: %v", err)
	}
	if err := s.verify("host", "old"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("the superseded fingerprint must now be rejected, got: %v", err)
	}

	reloaded, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.verify("host", "new"); err != nil {
		t.Fatalf("the accepted fingerprint must survive reload, got: %v", err)
	}
}

func TestTOFUFingerprintReportsPin(t *testing.T) {
	s, err := NewTOFUStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

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

	store, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatalf("load legacy pins: %v", err)
	}
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

func TestTOFURejectsConflictingCanonicalLegacyPinsWithoutRewrite(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]string{
		"MURMUR.EXAMPLE.COM.": "first-pin",
		"murmur.example.com":  "different-pin",
	}
	writeKnownServers(t, dir, legacy)
	path := filepath.Join(dir, tofuFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy pins before migration: %v", err)
	}

	_, err = NewTOFUStore(dir)
	if err == nil {
		t.Fatal("conflicting equivalent host pins were accepted")
	}
	if !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflict error = %q, want an actionable explanation", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read legacy pins after rejected migration: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatal("rejected migration rewrote the original TOFU database")
	}
}

func TestTOFUFirstUseSaveFailureDoesNotPublishPinInMemory(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	unblock := blockTOFUSave(t, dir)

	if err := store.verify("murmur.example.test", "new-pin"); err == nil {
		t.Fatal("first use unexpectedly succeeded while persistence was blocked")
	}
	if fingerprint, ok := store.Fingerprint("murmur.example.test"); ok {
		t.Fatalf("failed first-use save published pin %q in memory", fingerprint)
	}
	if _, ok := store.Pending("murmur.example.test"); ok {
		t.Fatal("failed first-use save created a pending replacement")
	}

	unblock()
	if err := store.verify("murmur.example.test", "new-pin"); err != nil {
		t.Fatalf("first use after persistence recovered: %v", err)
	}
}

func TestTOFUReplaceSaveFailureRetainsOldPinAndPendingCandidate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTOFUStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.verify("murmur.example.test", "old-pin"); err != nil {
		t.Fatalf("pin old fingerprint: %v", err)
	}
	if err := store.verify("murmur.example.test", "new-pin"); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("create pending replacement: %v", err)
	}
	unblock := blockTOFUSave(t, dir)

	if err := store.Replace("murmur.example.test", "new-pin"); err == nil {
		t.Fatal("replacement unexpectedly succeeded while persistence was blocked")
	}
	if fingerprint, ok := store.Fingerprint("murmur.example.test"); !ok || fingerprint != "old-pin" {
		t.Fatalf("pin after failed replacement = (%q, %v), want (old-pin, true)", fingerprint, ok)
	}
	if pending, ok := store.Pending("murmur.example.test"); !ok || pending != "new-pin" {
		t.Fatalf("pending after failed replacement = (%q, %v), want (new-pin, true)", pending, ok)
	}

	unblock()
	if err := store.Replace("murmur.example.test", "new-pin"); err != nil {
		t.Fatalf("replace after persistence recovered: %v", err)
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
