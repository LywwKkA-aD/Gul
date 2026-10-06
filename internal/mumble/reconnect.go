package mumble

import (
	"context"
	"errors"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/gumble/gumble"
)

// run is the connect/reconnect loop. Exactly one runs per Connect.
//
// The first failed attempt returns an error to the connect form. After a
// session succeeds, unexpected drops retry with 1s, 2s, 4s ... capped at 30s,
// until Disconnect, Close, or a terminal condition (kick, ban, credentials).
func (m *Manager) run(ctx context.Context, c credentials, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	attempt := 0
	reconnecting := false
	for {
		if isStopped(stop) {
			return
		}
		if !reconnecting {
			m.emitStatus(domain.ConnectionStatus{State: domain.StateConnecting, Server: c.address})
		}

		dropped := make(chan *gumble.DisconnectEvent, 1)
		transport := m.transports.next(c.kind, c.key)
		session, err := m.dialOnce(ctx, c, transport, dropped)
		if ctx.Err() != nil {
			_ = session.Disconnect()
			return
		}
		if err != nil {
			var mismatch *MismatchError
			if errors.As(err, &mismatch) {
				if !m.awaitFingerprint(c.address, mismatch, stop) {
					return
				}
				// Accepted: retry immediately, the backoff is untouched.
				continue
			}

			// The address never reaches gul.log: network errors embed
			// host:port on their own (PLAN.md §10.7).
			m.log.Warn("connect attempt failed", "error", RedactServer(err.Error(), c.address))

			if !reconnecting || isTerminalDialError(err) {
				m.emitStatus(domain.ConnectionStatus{
					State:  domain.StateDisconnected,
					Server: c.address,
					Error:  err.Error(),
				})
				return
			}
			m.emitStatus(domain.ConnectionStatus{State: domain.StateReconnecting, Server: c.address})
			if !sleepOrStop(m.backoffFn(attempt), stop) {
				return
			}
			attempt++
			continue
		}

		attempt = 0
		m.setSession(session)
		m.publishConnected(session, c.address)

		event, stopped, silent := m.waitSession(session, c.key, transport, dropped, stop)
		m.clearSession()
		_ = session.Disconnect()
		if stopped {
			return
		}

		reason, terminal := disconnectReason(event)
		// note is the diagnostic the user actually sees on the reconnect
		// banner. Only the two cases below carry one: an ordinary drop leaves
		// the banner as it was rather than flashing "connection lost" at every
		// blip. Both constants are addressless, so neither is redacted.
		note := ""
		switch {
		case silent:
			// A session without a round trip is rebuilt from a fresh tunnel.
			m.transports.failed(c.key)
			m.log.Info("Hysteria session did not answer, reconnecting",
				"transport", string(transport))
			reason, terminal = reasonNoRoundTrip, false
			note = reasonNoRoundTrip
		case session.stalledUplink():
			// Rebuild the tunnel when only the outgoing direction stops.
			m.transports.failed(c.key)
			m.log.Info("Hysteria uplink stalled, reconnecting",
				"transport", string(transport))
			reason = reasonUplinkStalled
			note = reasonUplinkStalled
		}
		if terminal {
			m.emitStatus(domain.ConnectionStatus{
				State: domain.StateDisconnected, Server: c.address, Error: reason,
			})
			return
		}
		// The transport's own error, where gumble had none of its own. Without
		// it a network fault reaches the log as a bare "connection lost", which
		// is exactly what a user's diagnostics said while telling us nothing.
		lost := []any{"reason", RedactServer(reason, c.address), "transport", string(transport)}
		if err := session.transportError(); err != nil {
			lost = append(lost, "error", RedactServer(err.Error(), c.address))
		}
		// The last reading of the panel, on the one line every lost session
		// writes. A session that dies between two ticks would otherwise leave
		// no account of itself at all.
		if vitals, ok := session.vitals(); ok {
			lost = append(lost, "vitals", vitals.redact(c.address))
		}
		// The voice counters belong on this line whether or not the session
		// had a panel of its own: they live on the Manager, they survive the
		// session that lost them, and a drop with a growing tx_errors is a
		// different story from a drop with none.
		lost = append(lost, "voice", m.voice.stats())
		m.log.Warn("connection lost", lost...)
		reconnecting = true
		m.emitStatus(domain.ConnectionStatus{State: domain.StateReconnecting, Server: c.address, Error: note})
		if !sleepOrStop(m.backoffFn(attempt), stop) {
			return
		}
		attempt++
	}
}

// waitSession owns the stats ticker for exactly one live session. Returning
// stops new requests before reconnecting; publishLatency rejects any response
// that was already in flight from the old client.
// It also holds the round-trip gate: silent reports a session that never
// proved our packets reach the server (roundTripGrace).
func (m *Manager) waitSession(
	session *Session,
	address string,
	transport Transport,
	dropped <-chan *gumble.DisconnectEvent,
	stop <-chan struct{},
) (event *gumble.DisconnectEvent, stopped, silent bool) {
	client := session.client
	interval := m.statsInterval
	if interval <= 0 {
		interval = statsPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sample := m.sampleLatencyFn
	if sample == nil {
		sample = m.sampleLatency
	}
	sample(client)

	verify := time.NewTimer(m.roundTripGrace)
	defer verify.Stop()

	for {
		select {
		case <-stop:
			return nil, true, false
		case event := <-dropped:
			return event, false, false
		case <-verify.C:
			if m.roundTripFn != nil && !m.roundTripFn(client) {
				return nil, false, true
			}
			// Remember Hysteria only after a Mumble round trip succeeds.
			if m.transports.succeeded(address, transport) {
				m.log.Info("Hysteria round trip verified", "transport", string(transport))
				if cb := m.cb.OnTransport; cb != nil {
					cb(address, string(transport))
				}
			}
		case <-ticker.C:
			sample(client)
			m.logVitals(session, transport)
		}
	}
}
