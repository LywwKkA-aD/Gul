package relayproto

import (
	"strings"
	"testing"
)

func TestDeriveIsDeterministicAndSecretBound(t *testing.T) {
	secret := []byte("spaces, unicode: páss\nheader-safe")
	a := Derive(secret)
	b := Derive(secret)
	if a != b {
		t.Fatal("derivation is not deterministic")
	}
	if strings.Contains(string(a), string(secret)) {
		t.Fatal("credential exposes the raw secret")
	}
	if a.Legacy() {
		t.Fatalf("v2 credential %q reported as legacy", a)
	}
	if Derive([]byte("different")).Matches(a) {
		t.Fatal("different secret matched")
	}
	if !a.Matches(b) {
		t.Fatal("equal credentials did not match")
	}
}

// The value v0.3.0-alpha.2 clients sent for this password. Nothing derives it
// any more; it is written out so the tests can still present its shape.
const legacyVector Credential = "EFh3GkAqEN8212SD6yFQdvF7iRkDciWJHYuMloUCIoQ"

func TestLegacyIsRecognizedByShapeAlone(t *testing.T) {
	if !legacyVector.Legacy() {
		t.Fatal("a credential without the v2 prefix was not reported as legacy")
	}
	if Derive([]byte("correct horse battery staple")).Legacy() {
		t.Fatal("a v2 credential was reported as legacy")
	}
	if legacyVector.Matches(Derive([]byte("correct horse battery staple"))) {
		t.Fatal("v1 and v2 credentials of the same secret must differ")
	}
}

func TestParseHeader(t *testing.T) {
	v2 := Derive([]byte("secret"))
	v1 := legacyVector
	for _, header := range []string{v2.Header(), v1.Header(), "bearer " + string(v2)} {
		c, ok := ParseHeader(header)
		if !ok {
			t.Errorf("rejected %q", header)
			continue
		}
		if !c.Matches(v2) && !c.Matches(v1) {
			t.Errorf("parsed %q does not match its source", header)
		}
	}
	rejected := []string{
		"", "Basic abc", "Bearer", "Bearer ", "Bearer not base64!", "Bearer YQ extra",
		"Bearer v2.", "Bearer v2.has=padding", "Bearer " + strings.Repeat("a", 200),
		"Bearer\ttab", "Bearer a\r\nb",
	}
	for _, header := range rejected {
		if _, ok := ParseHeader(header); ok {
			t.Errorf("accepted %q", header)
		}
	}
}

func BenchmarkDerive(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Derive([]byte("correct horse battery staple"))
	}
}
