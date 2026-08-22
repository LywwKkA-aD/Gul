package relay

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

const certificateCheckInterval = 30 * time.Second

type certificateStamp struct {
	certSize int64
	certTime int64
	keySize  int64
	keyTime  int64
}

// CertificateLoader checks the certificate pair at a bounded interval during
// new TLS handshakes. If an ACME renewal is observed between its two atomic
// file replacements, the last valid pair remains in service and a later
// handshake retries the load.
type CertificateLoader struct {
	certFile string
	keyFile  string

	mu      sync.Mutex
	current *tls.Certificate
	stamp   certificateStamp
	next    time.Time
	check   time.Duration
}

// NewCertificateLoader fails closed unless a valid pair exists at startup.
func NewCertificateLoader(certFile, keyFile string) (*CertificateLoader, error) {
	loader := &CertificateLoader{certFile: certFile, keyFile: keyFile}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load relay TLS certificate: %w", err)
	}
	loader.current = &certificate
	loader.stamp, _ = statCertificatePair(certFile, keyFile)
	loader.check = certificateCheckInterval
	loader.next = time.Now().Add(loader.check)
	return loader, nil
}

// GetCertificate implements tls.Config.GetCertificate. A transient invalid
// renewal never replaces the last pair known to parse and match.
func (l *CertificateLoader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if !now.Before(l.next) {
		l.next = now.Add(l.check)
		stamp, statErr := statCertificatePair(l.certFile, l.keyFile)
		if statErr == nil && stamp != l.stamp {
			if certificate, loadErr := tls.LoadX509KeyPair(l.certFile, l.keyFile); loadErr == nil {
				l.current = &certificate
				l.stamp = stamp
			}
		}
	}
	return l.current, nil
}

func statCertificatePair(certFile, keyFile string) (certificateStamp, error) {
	certInfo, err := os.Stat(certFile)
	if err != nil {
		return certificateStamp{}, err
	}
	keyInfo, err := os.Stat(keyFile)
	if err != nil {
		return certificateStamp{}, err
	}
	return certificateStamp{
		certSize: certInfo.Size(),
		certTime: certInfo.ModTime().UnixNano(),
		keySize:  keyInfo.Size(),
		keyTime:  keyInfo.ModTime().UnixNano(),
	}, nil
}
