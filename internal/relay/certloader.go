package relay

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	certificateCheckInterval = 30 * time.Second
	// certificateErrorLogInterval throttles the reload failure line. The check
	// itself is already bounded to one attempt per interval, but a permanently
	// broken pair would still write a line every 30 s for as long as the
	// service runs.
	certificateErrorLogInterval = 5 * time.Minute
)

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
//
// A pair that stays broken keeps the service up on the last valid certificate
// until it expires, which is the right trade against dropping every session.
// It must not be silent: each failure is recorded for LastError and written to
// the log at a throttled rate.
type CertificateLoader struct {
	certFile string
	keyFile  string
	logger   *slog.Logger
	now      func() time.Time

	mu         sync.Mutex
	current    *tls.Certificate
	stamp      certificateStamp
	next       time.Time
	check      time.Duration
	lastError  error
	lastLogged time.Time
}

// NewCertificateLoader fails closed unless a valid pair exists at startup.
func NewCertificateLoader(certFile, keyFile string, logger *slog.Logger) (*CertificateLoader, error) {
	loader := &CertificateLoader{
		certFile: certFile,
		keyFile:  keyFile,
		logger:   loggerOrDefault(logger),
		now:      time.Now,
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load relay TLS certificate: %w", err)
	}
	loader.current = &certificate
	loader.stamp, _ = statCertificatePair(certFile, keyFile)
	loader.check = certificateCheckInterval
	loader.next = loader.now().Add(loader.check)
	return loader, nil
}

// GetCertificate implements tls.Config.GetCertificate. A transient invalid
// renewal never replaces the last pair known to parse and match.
func (l *CertificateLoader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if !now.Before(l.next) {
		l.next = now.Add(l.check)
		l.reload(now)
	}
	return l.current, nil
}

// LastError reports the most recent reload failure, or nil when the pair on
// disk is the one being served. The health endpoint surfaces it so a stale
// certificate is visible before it expires.
func (l *CertificateLoader) LastError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastError
}

func (l *CertificateLoader) reload(now time.Time) {
	stamp, statErr := statCertificatePair(l.certFile, l.keyFile)
	if statErr != nil {
		l.recordFailure(now, fmt.Errorf("stat relay TLS certificate: %w", statErr))
		return
	}
	if stamp == l.stamp {
		return
	}
	certificate, loadErr := tls.LoadX509KeyPair(l.certFile, l.keyFile)
	if loadErr != nil {
		l.recordFailure(now, fmt.Errorf("load relay TLS certificate: %w", loadErr))
		return
	}
	if l.lastError != nil {
		l.logger.Info("relay certificate reload recovered", "cert", l.certFile)
	}
	l.current = &certificate
	l.stamp = stamp
	l.lastError = nil
	l.lastLogged = time.Time{}
}

func (l *CertificateLoader) recordFailure(now time.Time, err error) {
	l.lastError = err
	if !l.lastLogged.IsZero() && now.Sub(l.lastLogged) < certificateErrorLogInterval {
		return
	}
	l.lastLogged = now
	l.logger.Error("relay certificate reload failed",
		"cert", l.certFile,
		"key", l.keyFile,
		"error", err,
	)
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
