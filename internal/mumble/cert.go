package mumble

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	certFileName = "cert.pem"
	keyFileName  = "key.pem"

	// certCommonName is what the server shows for an unregistered client.
	certCommonName = "Gul"
	// certValidityYears keeps the identity stable for the lifetime of the
	// install; Mumble never checks the chain, only the certificate hash.
	certValidityYears = 20
	// certBits is RSA-2048: Mumble servers and the official client have used
	// RSA since forever, ECDSA identities are not universally accepted.
	certBits = 2048
)

// ClientCertificate returns the persistent client identity stored in dir,
// generating a fresh self-signed RSA-2048 pair on first run. The certificate
// is what gives the account a stable User.Hash on the server, so it must be
// reused across runs: regenerating it creates a brand new identity.
//
// A directory the app cannot read or write costs persistence, not startup:
// the identity is then generated for this run only and the degradation is
// logged. The one hard error left is a stored pair that exists and cannot be
// parsed, because replacing it silently would drop the user's server-side
// identity without them ever asking for it.
//
// Both files are written with 0600 permissions.
func ClientCertificate(dir string, log *slog.Logger) (tls.Certificate, error) {
	if log == nil {
		log = slog.Default()
	}
	certPath := filepath.Join(dir, certFileName)
	keyPath := filepath.Join(dir, keyFileName)

	certExists, certErr := fileExists(certPath)
	keyExists, keyErr := fileExists(keyPath)
	if certErr != nil || keyErr != nil {
		log.Warn("stored client identity unreadable, using a session identity",
			"error", errors.Join(certErr, keyErr))
		return ephemeralClientCertificate()
	}

	if certExists && keyExists {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			// Never silently regenerate: a new key pair is a new identity on
			// the server. Let the user decide to delete the broken files.
			return tls.Certificate{}, fmt.Errorf("load client certificate from %s: %w", dir, err)
		}
		return cert, nil
	}

	// A half-written pair carries no usable identity - start over.
	certPEM, keyPEM, err := generateClientCertificate()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writeSecret(keyPath, keyPEM); err != nil {
		log.Warn("client identity not writable, using a session identity",
			"file", keyFileName, "error", err)
	} else if err := writeSecret(certPath, certPEM); err != nil {
		// Leaving a key without its certificate behind would look like a
		// half-written pair on the next run, which regenerates anyway.
		log.Warn("client identity not writable, using a session identity",
			"file", certFileName, "error", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse generated client certificate: %w", err)
	}
	return cert, nil
}

// ephemeralClientCertificate builds an identity that lives for this run only.
func ephemeralClientCertificate() (tls.Certificate, error) {
	certPEM, keyPEM, err := generateClientCertificate()
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse generated client certificate: %w", err)
	}
	return cert, nil
}

// generateClientCertificate builds a self-signed RSA-2048 certificate usable
// as a TLS client certificate, returning the PEM encodings of both halves.
func generateClientCertificate() (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, certBits)
	if err != nil {
		return nil, nil, fmt.Errorf("generate rsa key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial number: %w", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: certCommonName},
		// Backdate slightly so a skewed server clock does not reject a
		// certificate generated seconds ago.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(certValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
}

// writeSecret writes data atomically with owner-only permissions. The explicit
// Chmod defends against a permissive process umask.
func writeSecret(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
