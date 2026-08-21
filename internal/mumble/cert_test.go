package mumble

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestClientCertificateGeneratesOnFirstRun(t *testing.T) {
	dir := t.TempDir()

	cert, err := ClientCertificate(dir)
	if err != nil {
		t.Fatalf("first run must generate a certificate: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("certificate chain is empty")
	}

	for _, name := range []string{certFileName, keyFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must exist after generation: %v", name, err)
		}
	}
}

func TestClientCertificateIsReusedAcrossRuns(t *testing.T) {
	dir := t.TempDir()

	first, err := ClientCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ClientCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The identity on the server is the certificate hash: a regenerated pair
	// would silently create a new account.
	if !bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Fatal("second run must reuse the stored certificate, not generate a new one")
	}
}

func TestClientCertificateProperties(t *testing.T) {
	dir := t.TempDir()

	cert, err := ClientCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse generated certificate: %v", err)
	}

	key, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("expected an RSA public key, got %T", leaf.PublicKey)
	}
	if got := key.N.BitLen(); got != certBits {
		t.Fatalf("expected RSA-%d, got RSA-%d", certBits, got)
	}
	if leaf.Subject.CommonName != certCommonName {
		t.Fatalf("common name = %q, want %q", leaf.Subject.CommonName, certCommonName)
	}

	var clientAuth bool
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			clientAuth = true
		}
	}
	if !clientAuth {
		t.Fatal("certificate must be usable for TLS client authentication")
	}

	if !leaf.NotBefore.Before(time.Now()) {
		t.Fatal("certificate must already be valid")
	}
	wantExpiry := time.Now().AddDate(certValidityYears, 0, 0)
	if leaf.NotAfter.Before(wantExpiry.AddDate(0, 0, -2)) {
		t.Fatalf("certificate expires at %s, want roughly %s", leaf.NotAfter, wantExpiry)
	}
}

func TestClientCertificateFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on windows")
	}
	dir := t.TempDir()

	if _, err := ClientCertificate(dir); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{certFileName, keyFileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s permissions = %#o, want 0600", name, perm)
		}
	}
}

func TestClientCertificateReportsBrokenPair(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, certFileName), []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, keyFileName), []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Regenerating silently would swap the user's identity behind their back.
	if _, err := ClientCertificate(dir); err == nil {
		t.Fatal("a corrupted key pair must be reported, not replaced")
	}
}

func TestClientCertificateRegeneratesHalfWrittenPair(t *testing.T) {
	dir := t.TempDir()

	// Only a key on disk carries no identity: there is nothing to preserve.
	if err := os.WriteFile(filepath.Join(dir, keyFileName), []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientCertificate(dir); err != nil {
		t.Fatalf("a lone key file must not block generation: %v", err)
	}
}
