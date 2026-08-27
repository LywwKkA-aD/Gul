package relay

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// The contract that carries no nested TLS: the relay terminates the client's
// tunnel and speaks Mumble TLS to the server itself, over the loopback the two
// of them share.
//
// This is a deliberate reversal of a principle this project wrote down on
// 2026-08-23 and kept until now - that the relay does not terminate trust
// (docs/DECISIONS.md). It was reversed knowingly and for a measured reason: a
// nested TLS handshake is what the classifier reads, and no amount of padding
// removes it. What it costs is written where the promise used to be, not left
// in a comment: the relay now sees Mumble in the clear, including the server
// password in the Authenticate packet.
//
// What it does not cost, and must never cost: the ability to be somebody. This
// build opens anonymous sessions only. Presenting a client certificate the
// relay did not watch anyone prove possession of would let the relay be any
// user it liked, and consent to reading is not consent to impersonation.

const (
	// tunnelHandshakeTimeout bounds the exchange before the upstream is
	// dialled. A half-open handshake holds a capacity slot, and today the
	// nested TLS one is bounded by nothing but the session idle timeout - a
	// minute per slot for a peer that need only connect and go quiet.
	tunnelHandshakeTimeout = 5 * time.Second
)

// upstreamTLS is how the relay talks to the server behind it.
//
// The certificate is self-signed - it is the identity clients have always
// pinned - so there is no chain to verify and InsecureSkipVerify is not a
// shortcut here but a statement of fact. What replaces the chain is the pin:
// when the operator supplies a fingerprint the leaf must match it, and when
// they do not, the relay records what it saw and passes it on for the client
// to pin instead.
//
// The difference matters and has to be said plainly: with a pin the relay
// checks; without one, the relay is repeating what it was told. The client
// cannot tell those apart from the outside, so the operator's documentation
// has to.
func (h *Handler) upstreamTLS(conn net.Conn) (*tls.Conn, []byte, error) {
	var leaf []byte
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: h.upstreamName,
		// The chain does not exist; the pin below is the check.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("relay upstream presented no certificate")
			}
			leaf = raw[0]
			if h.upstreamFingerprint == "" {
				return nil
			}
			sum := sha256.Sum256(leaf)
			if got := hex.EncodeToString(sum[:]); got != h.upstreamFingerprint {
				return fmt.Errorf("relay upstream fingerprint %s does not match the pin", got)
			}
			return nil
		},
	}
	inner := tls.Client(conn, config)
	if err := inner.HandshakeContext(h.ctx); err != nil {
		return nil, nil, err
	}
	return inner, leaf, nil
}

// serveTunnel runs the tunnel contract on an admitted stream and carries the
// session.
//
// The order is chosen so the client is told which side is at fault. The
// upstream is dialled after the hello and the answer names the outcome, so a
// server that is simply not running reaches the user as "the server is down"
// rather than as a road that opened and died - which is what it looks like
// today, and what sends the client hunting through every other road it has.
func (h *Handler) serveTunnel(stream net.Conn, sourceIP, sourceBlock, transport string) {
	_ = stream.SetDeadline(time.Now().Add(tunnelHandshakeTimeout))
	hello, err := relayproto.ReadTunnelHello(stream)
	if err != nil {
		h.logger.Warn("relay tunnel handshake failed",
			"source", sourceIP, "transport", transport, "error", err)
		return
	}
	if hello.Version != relayproto.TunnelVersion {
		h.logger.Warn("relay tunnel version refused",
			"source", sourceIP, "transport", transport, "version", hello.Version)
		_ = relayproto.WriteTunnelAccept(stream, relayproto.TunnelAccept{
			Version: relayproto.TunnelVersion,
			Status:  relayproto.TunnelVersionUnsupported,
		})
		return
	}
	// A certificate in the hello is a claim nobody has proved. Until the
	// exchange that proves it exists, the session is anonymous, and saying so
	// here is cheaper than discovering later that it was quietly trusted.
	if len(hello.Certificate) != 0 {
		h.logger.Debug("relay tunnel ignored an unproven identity",
			"source", sourceIP, "transport", transport)
	}

	upstream, localAddress, err := h.dialUpstream(sourceBlock)
	if err != nil {
		h.logger.Error("relay upstream dial failed",
			"source", sourceIP, "transport", transport, "upstream", h.upstream, "error", err)
		_ = relayproto.WriteTunnelAccept(stream, relayproto.TunnelAccept{
			Version: relayproto.TunnelVersion,
			Status:  relayproto.TunnelUpstreamDown,
		})
		return
	}
	defer func() { _ = upstream.Close() }()

	inner, leaf, err := h.upstreamTLS(upstream)
	if err != nil {
		h.logger.Error("relay upstream TLS failed",
			"source", sourceIP, "transport", transport, "error", err)
		_ = relayproto.WriteTunnelAccept(stream, relayproto.TunnelAccept{
			Version: relayproto.TunnelVersion,
			Status:  relayproto.TunnelUpstreamDown,
		})
		return
	}
	defer func() { _ = inner.Close() }()

	if err := relayproto.WriteTunnelAccept(stream, relayproto.TunnelAccept{
		Version:     relayproto.TunnelVersion,
		Status:      relayproto.TunnelAccepted,
		Certificate: leaf,
	}); err != nil {
		return
	}
	if err := relayproto.WriteTunnelReady(stream); err != nil {
		return
	}
	// The handshake budget is spent; the session lives under the session
	// timeouts from here.
	_ = stream.SetDeadline(time.Time{})

	h.pump(stream, inner, sourceIP, localAddress, transport, contractTunnel)
}
