package hysteria

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/apernet/quic-go"
)

const addressFallbackDelay = 250 * time.Millisecond

type addressDialFunc func(context.Context, net.IPAddr) (net.Conn, error)

type addressResult struct {
	conn net.Conn
	err  error
}

// raceAddresses staggers at most two attempts, alternating address families so
// a silent IPv6 path cannot prevent a working IPv4 path from being tried.
func raceAddresses(ctx context.Context, addresses []net.IPAddr, dial addressDialFunc) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("hysteria endpoint has no IP addresses")
	}
	ordered := interleaveFamilies(addresses)
	attemptCtx, cancel := context.WithCancel(ctx)
	results := make(chan addressResult, 2)
	next, active := 0, 0
	defer func() {
		cancel()
		// A losing attempt may have connected just before cancellation. Drain
		// every attempt and close any such connection before returning.
		for active > 0 {
			result := <-results
			active--
			if result.conn != nil {
				_ = result.conn.Close()
			}
		}
	}()
	start := func() {
		address := ordered[next]
		next++
		active++
		go func() {
			conn, err := dial(attemptCtx, address)
			results <- addressResult{conn: conn, err: err}
		}()
	}
	start()
	timer := time.NewTimer(addressFallbackDelay)
	defer timer.Stop()
	var timerC <-chan time.Time
	armTimer := func() {
		if active < 2 && next < len(ordered) {
			timer.Reset(addressFallbackDelay)
			timerC = timer.C
		} else {
			timer.Stop()
			timerC = nil
		}
	}
	armTimer()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timerC:
			start()
			armTimer()
		case result := <-results:
			active--
			if ctx.Err() != nil {
				if result.conn != nil {
					_ = result.conn.Close()
				}
				return nil, ctx.Err()
			}
			if result.err == nil {
				return result.conn, nil
			}
			if terminalRejection(result.err) {
				return nil, result.err
			}
			if next < len(ordered) {
				start()
			} else if active == 0 {
				return nil, result.err
			}
			armTimer()
		}
	}
}

func interleaveFamilies(addresses []net.IPAddr) []net.IPAddr {
	firstIsIPv4 := addresses[0].IP.To4() != nil
	var first, second []net.IPAddr
	for _, address := range addresses {
		if (address.IP.To4() != nil) == firstIsIPv4 {
			first = append(first, address)
		} else {
			second = append(second, address)
		}
	}
	ordered := make([]net.IPAddr, 0, len(addresses))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			ordered = append(ordered, first[i])
		}
		if i < len(second) {
			ordered = append(ordered, second[i])
		}
	}
	return ordered
}

func terminalRejection(err error) bool {
	if errors.Is(err, ErrAuthentication) {
		return true
	}
	// The pinned QUIC fork flattens Chrome/uTLS verification errors into a
	// local INTERNAL_ERROR instead of preserving their x509 error chain.
	var transport *quic.TransportError
	if errors.As(err, &transport) && !transport.Remote && transport.ErrorCode == quic.InternalError &&
		strings.HasPrefix(transport.ErrorMessage, "tls: failed to verify certificate:") {
		return true
	}
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verification) || errors.As(err, &authority) ||
		errors.As(err, &hostname) || errors.As(err, &invalid)
}
