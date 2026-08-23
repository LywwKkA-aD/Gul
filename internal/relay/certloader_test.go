package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCertificateLoaderReloadsAndKeepsLastValidPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "relay.crt")
	keyFile := filepath.Join(dir, "relay.key")
	writeTestCertificate(t, certFile, keyFile, 1)

	loader, err := NewCertificateLoader(certFile, keyFile, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	loader.check = 0
	loader.next = time.Time{}
	first, err := loader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("first certificate: %v", err)
	}
	if got := certificateSerial(t, first); got != 1 {
		t.Fatalf("serial = %d, want 1", got)
	}

	writeTestCertificate(t, certFile, keyFile, 2)
	second, err := loader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("reloaded certificate: %v", err)
	}
	if got := certificateSerial(t, second); got != 2 {
		t.Fatalf("serial = %d, want 2", got)
	}

	if err := os.WriteFile(keyFile, []byte("incomplete renewal"), 0o600); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}
	lastGood, err := loader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("last valid certificate: %v", err)
	}
	if got := certificateSerial(t, lastGood); got != 2 {
		t.Fatalf("serial after invalid renewal = %d, want 2", got)
	}
}

func TestCertificateLoaderRequiresInitialValidPair(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewCertificateLoader(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("expected missing certificate error")
	}
}

func TestCertificateLoaderIsSafeForConcurrentHandshakes(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "relay.crt")
	keyFile := filepath.Join(dir, "relay.key")
	writeTestCertificate(t, certFile, keyFile, 7)
	loader, err := NewCertificateLoader(certFile, keyFile, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			certificate, err := loader.GetCertificate(nil)
			if err != nil || certificate == nil {
				t.Errorf("certificate = %v, error = %v", certificate, err)
			}
		}()
	}
	wg.Wait()
}

func writeTestCertificate(t *testing.T, certFile, keyFile string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "murmur.example.test"},
		DNSNames:     []string{"murmur.example.test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func certificateSerial(t *testing.T, certificate *tls.Certificate) int64 {
	t.Helper()
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return leaf.SerialNumber.Int64()
}

// TestCertificateLoaderReportsPersistentReloadFailures covers the silent
// failure: a certificate pair that stops loading kept the last valid one in
// service with nothing in the journal and nothing for the health command to
// report, until it expired.
func TestCertificateLoaderReportsPersistentReloadFailures(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "relay.crt")
	keyFile := filepath.Join(dir, "relay.key")
	writeTestCertificate(t, certFile, keyFile, 1)

	logger, records := newRecordingLogger()
	loader, err := NewCertificateLoader(certFile, keyFile, logger)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	loader.now = func() time.Time { return now }
	loader.check = 0
	loader.next = time.Time{}
	if loader.LastError() != nil {
		t.Fatalf("healthy loader reports %v", loader.LastError())
	}

	if err := os.WriteFile(keyFile, []byte("incomplete renewal"), 0o600); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}
	certificate, err := loader.GetCertificate(nil)
	if err != nil || certificate == nil {
		t.Fatalf("broken pair stopped serving: %v", err)
	}
	if loader.LastError() == nil {
		t.Fatal("reload failure was discarded")
	}
	if got := countRecords(records, "relay certificate reload failed"); got != 1 {
		t.Fatalf("failure records = %d, want 1", got)
	}

	// Handshakes keep arriving; the failure must not be logged for each one.
	now = now.Add(certificateErrorLogInterval - time.Second)
	for range 5 {
		_, _ = loader.GetCertificate(nil)
	}
	if got := countRecords(records, "relay certificate reload failed"); got != 1 {
		t.Fatalf("failure records within the throttle window = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	_, _ = loader.GetCertificate(nil)
	if got := countRecords(records, "relay certificate reload failed"); got != 2 {
		t.Fatalf("failure records after the throttle window = %d, want 2", got)
	}

	writeTestCertificate(t, certFile, keyFile, 2)
	recovered, err := loader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("recovered certificate: %v", err)
	}
	if got := certificateSerial(t, recovered); got != 2 {
		t.Fatalf("serial after recovery = %d, want 2", got)
	}
	if loader.LastError() != nil {
		t.Fatalf("recovered loader still reports %v", loader.LastError())
	}
	if _, ok := records.find("relay certificate reload recovered"); !ok {
		t.Fatal("recovery was not logged")
	}
}

func TestCertificateLoaderReportsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "relay.crt")
	keyFile := filepath.Join(dir, "relay.key")
	writeTestCertificate(t, certFile, keyFile, 3)

	loader, err := NewCertificateLoader(certFile, keyFile, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	loader.check = 0
	loader.next = time.Time{}
	if err := os.Remove(keyFile); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if _, err := loader.GetCertificate(nil); err != nil {
		t.Fatalf("loader stopped serving: %v", err)
	}
	if loader.LastError() == nil {
		t.Fatal("missing key file was not reported")
	}
}

func countRecords(handler *recordingHandler, message string) int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	count := 0
	for _, record := range handler.records {
		if record.Message == message {
			count++
		}
	}
	return count
}
