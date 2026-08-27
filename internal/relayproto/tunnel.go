package relayproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The tunnel contract: what the client and the relay say to each other before
// Mumble packets start flowing, in place of a second TLS session.
//
// A nested TLS handshake is what an on-path classifier reads. Measured on our
// own wire, inside the tunnel and in the first 25 packets of the flow, the
// client's opening burst is 2002 bytes in seven packets today and 286 bytes in
// one without it - across the 300-byte line that separates ordinary
// connections from proxied ones in the published rule
// (internal/mumble/openingshape_test.go, docs/DECISIONS.md).
//
// So the exchange here is deliberately small and deliberately fixed in shape.
// Everything it carries is already inside the outer TLS session and inside the
// cell grid, so nothing here is secret and nothing here needs padding of its
// own: one frame is one Write, and the shaper turns each into whole cells.
//
// What it is not, yet: authenticated. Possession of the credential is proved
// before this runs, by the bearer on the WebSocket road and the preamble on
// QUIC, and the key exchange that will replace both belongs to the step after
// this one. Nothing in this file may be treated as proof of anything.

const (
	// TunnelVersion is the contract this build speaks. It travels in the
	// first frame so that a mismatch is answered rather than guessed at: two
	// ends that disagree should say so, not deadlock while each waits for a
	// frame the other will never send.
	TunnelVersion = 1

	// tunnelHeaderBytes is kind(1) + length(2).
	tunnelHeaderBytes = 3
	// tunnelMaxPayload bounds one frame. The largest thing the contract
	// carries is a certificate in DER, and 8 KiB clears any certificate an
	// ordinary Mumble server or client presents with room to spare.
	tunnelMaxPayload = 8 << 10
)

// Frame kinds. The numbering is fixed; a kind this build does not know is an
// error rather than something to skip, because skipping would mean carrying on
// with a peer that believes it told us something.
const (
	tunnelKindHello  byte = 0x01
	tunnelKindAccept byte = 0x02
	tunnelKindReady  byte = 0x03
)

// TunnelStatus is what the relay says about the server behind it.
//
// It exists because today there is no way to say it. When Murmur is down the
// relay closes the WebSocket with an internal error the client never reads, so
// the client sees a road that opened and then died - and isRoadFailure treats
// that as evidence about the road and starts trying the other one. A server
// that is simply not running should not cost the user a search through every
// road they have.
type TunnelStatus byte

const (
	// TunnelAccepted means the relay reached the server and the tunnel is open.
	TunnelAccepted TunnelStatus = 0
	// TunnelUpstreamDown means the relay is fine and the server behind it is
	// not. Nothing about the road is wrong.
	TunnelUpstreamDown TunnelStatus = 1
	// TunnelVersionUnsupported means the two ends do not speak the same
	// contract. Another road will not help either.
	TunnelVersionUnsupported TunnelStatus = 2
)

func (s TunnelStatus) String() string {
	switch s {
	case TunnelAccepted:
		return "accepted"
	case TunnelUpstreamDown:
		return "upstream down"
	case TunnelVersionUnsupported:
		return "version unsupported"
	}
	return fmt.Sprintf("status %d", byte(s))
}

// ErrTunnelProtocol is every way the exchange can be malformed. The reason is
// in the wrapped error and in the relay's journal; the client shows one
// message, because a peer that cannot speak the contract is one failure to a
// user however it failed.
var ErrTunnelProtocol = errors.New("tunnel handshake failed")

// TunnelHello is the client's opening frame.
type TunnelHello struct {
	Version byte
	// Certificate is the client's identity in DER, or empty for an anonymous
	// session. Empty is what this build sends: proving possession of the key
	// belongs to the step that follows, and a certificate presented without
	// that proof would be a claim anybody could copy.
	Certificate []byte
}

// TunnelAccept is the relay's answer.
type TunnelAccept struct {
	Version byte
	Status  TunnelStatus
	// Certificate is the leaf the server behind the relay presented, in DER,
	// so the client can pin it exactly as it pinned it when the inner TLS was
	// its own. It is an attestation and not a proof: the relay checked it, and
	// the client is taking the relay's word. That difference has to reach the
	// user's documentation, not just this comment.
	Certificate []byte
}

// WriteTunnelHello sends the opening frame as one write.
func WriteTunnelHello(w io.Writer, hello TunnelHello) error {
	payload := make([]byte, 1+len(hello.Certificate))
	payload[0] = hello.Version
	copy(payload[1:], hello.Certificate)
	return writeTunnelFrame(w, tunnelKindHello, payload)
}

// ReadTunnelHello reads it.
func ReadTunnelHello(r io.Reader) (TunnelHello, error) {
	payload, err := readTunnelFrame(r, tunnelKindHello)
	if err != nil {
		return TunnelHello{}, err
	}
	if len(payload) < 1 {
		return TunnelHello{}, fmt.Errorf("%w: hello carries no version", ErrTunnelProtocol)
	}
	return TunnelHello{Version: payload[0], Certificate: payload[1:]}, nil
}

// WriteTunnelAccept sends the answer as one write.
func WriteTunnelAccept(w io.Writer, accept TunnelAccept) error {
	payload := make([]byte, 2+len(accept.Certificate))
	payload[0] = accept.Version
	payload[1] = byte(accept.Status)
	copy(payload[2:], accept.Certificate)
	return writeTunnelFrame(w, tunnelKindAccept, payload)
}

// ReadTunnelAccept reads it.
func ReadTunnelAccept(r io.Reader) (TunnelAccept, error) {
	payload, err := readTunnelFrame(r, tunnelKindAccept)
	if err != nil {
		return TunnelAccept{}, err
	}
	if len(payload) < 2 {
		return TunnelAccept{}, fmt.Errorf("%w: accept carries no status", ErrTunnelProtocol)
	}
	return TunnelAccept{
		Version:     payload[0],
		Status:      TunnelStatus(payload[1]),
		Certificate: payload[2:],
	}, nil
}

// WriteTunnelReady says the relay has the server on the line and Mumble
// packets follow. It is separate from the accept because the accept is sent
// before the upstream is dialled - a client that has to wait should know it is
// waiting on the server rather than on the road.
func WriteTunnelReady(w io.Writer) error {
	return writeTunnelFrame(w, tunnelKindReady, nil)
}

// ReadTunnelReady waits for it.
func ReadTunnelReady(r io.Reader) error {
	_, err := readTunnelFrame(r, tunnelKindReady)
	return err
}

func writeTunnelFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > tunnelMaxPayload {
		return fmt.Errorf("%w: frame of %d bytes exceeds the %d byte bound",
			ErrTunnelProtocol, len(payload), tunnelMaxPayload)
	}
	frame := make([]byte, tunnelHeaderBytes+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[tunnelHeaderBytes:], payload)
	// One frame, one Write: the shaper turns a Write into whole cells, and a
	// frame split across two of them would be two writes of different shape.
	if _, err := w.Write(frame); err != nil {
		return err
	}
	return nil
}

// readTunnelFrame reads the next frame and insists it is the one expected.
//
// Insisting is the point. A contract where either side may send anything next
// is a contract where a confused peer is indistinguishable from a hostile one,
// and where a frame that arrives in the wrong order gets acted on.
func readTunnelFrame(r io.Reader, want byte) ([]byte, error) {
	header := make([]byte, tunnelHeaderBytes)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTunnelProtocol, err)
	}
	length := binary.BigEndian.Uint16(header[1:3])
	if int(length) > tunnelMaxPayload {
		return nil, fmt.Errorf("%w: frame declares %d bytes, over the %d byte bound",
			ErrTunnelProtocol, length, tunnelMaxPayload)
	}
	if header[0] != want {
		return nil, fmt.Errorf("%w: expected frame %#x, got %#x", ErrTunnelProtocol, want, header[0])
	}
	if length == 0 {
		return nil, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTunnelProtocol, err)
	}
	return payload, nil
}
