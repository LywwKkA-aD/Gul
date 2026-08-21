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
