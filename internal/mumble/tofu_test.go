package mumble

import (
	"errors"
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
