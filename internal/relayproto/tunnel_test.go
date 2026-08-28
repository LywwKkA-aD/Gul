package relayproto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// The exchange has to survive the trip in both directions, carrying what it is
// for: a certificate that may be there or not, and a status that has to arrive
// even when it is the bad one.
func TestTunnelFramesRoundTrip(t *testing.T) {
	t.Parallel()
	certificate := bytes.Repeat([]byte{0x30, 0x82, 0x01, 0x0a}, 300) // DER-shaped, 1200 bytes
	fingerprint := bytes.Repeat([]byte{0xab}, 64)                    // hex of SHA-256

	for name, tc := range map[string]struct {
		hello  TunnelHello
		accept TunnelAccept
	}{
		"anonymous, accepted": {
			hello:  TunnelHello{Version: TunnelVersion},
			accept: TunnelAccept{Version: TunnelVersion, Status: TunnelAccepted, Fingerprint: fingerprint},
		},
		"identified, accepted": {
			hello:  TunnelHello{Version: TunnelVersion, Identity: certificate},
			accept: TunnelAccept{Version: TunnelVersion, Status: TunnelAccepted, Fingerprint: fingerprint},
		},
		"the server behind the relay is down": {
			hello:  TunnelHello{Version: TunnelVersion},
			accept: TunnelAccept{Version: TunnelVersion, Status: TunnelUpstreamDown},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var up, down bytes.Buffer

			if err := WriteTunnelHello(&up, tc.hello); err != nil {
				t.Fatalf("write hello: %v", err)
			}
			gotHello, err := ReadTunnelHello(&up)
			if err != nil {
				t.Fatalf("read hello: %v", err)
			}
			if gotHello.Version != tc.hello.Version || !bytes.Equal(gotHello.Identity, tc.hello.Identity) {
				t.Fatalf("hello = %+v, want %+v", gotHello, tc.hello)
			}

			if err := WriteTunnelAccept(&down, tc.accept); err != nil {
				t.Fatalf("write accept: %v", err)
			}
			gotAccept, err := ReadTunnelAccept(&down)
			if err != nil {
				t.Fatalf("read accept: %v", err)
			}
			if gotAccept.Status != tc.accept.Status ||
				!bytes.Equal(gotAccept.Fingerprint, tc.accept.Fingerprint) {
				t.Fatalf("accept = %+v, want %+v", gotAccept, tc.accept)
			}

			if err := WriteTunnelReady(&down); err != nil {
				t.Fatalf("write ready: %v", err)
			}
			if err := ReadTunnelReady(&down); err != nil {
				t.Fatalf("read ready: %v", err)
			}
		})
	}
}

// One frame is one Write. The shaper turns a Write into whole cells, so a
// frame that left in two writes would leave as two runs of cells with a gap
// between them - and the gap, not the size, is what the burst analysis reads.
func TestATunnelFrameIsOneWrite(t *testing.T) {
	t.Parallel()
	wire := &writeCounter{}

	if err := WriteTunnelHello(wire, TunnelHello{
		Version:  TunnelVersion,
		Identity: bytes.Repeat([]byte{7}, 1200),
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if err := WriteTunnelReady(wire); err != nil {
		t.Fatalf("write ready: %v", err)
	}

	if wire.writes != 2 {
		t.Fatalf("two frames took %d writes, want 2", wire.writes)
	}
}

// A frame that is not the one expected is an error, not something to skip. A
// contract where either side may send anything next cannot tell a confused
// peer from a hostile one, and acts on frames that arrive out of order.
func TestAFrameOutOfOrderIsRefused(t *testing.T) {
	t.Parallel()
	// An accept, which is well formed and would parse happily as a hello if
	// the kind were not checked - it has a version byte in the same place.
	// A ready frame would not prove anything here: it is empty, so the
	// "carries no version" check would refuse it whether the kind was read or
	// not, and the test would pass with the kind check deleted.
	var wire bytes.Buffer
	if err := WriteTunnelAccept(&wire, TunnelAccept{Version: TunnelVersion, Status: TunnelAccepted}); err != nil {
		t.Fatalf("write accept: %v", err)
	}

	_, err := ReadTunnelHello(&wire)

	if !errors.Is(err, ErrTunnelProtocol) {
		t.Fatalf("error = %v, want ErrTunnelProtocol", err)
	}
	if !strings.Contains(err.Error(), "expected frame") {
		t.Fatalf("error = %v, want it to name the frame it wanted", err)
	}
}

// Everything read from the wire needs a bound, including a length field that
// arrived from outside. A peer that declares more than the contract carries is
// refused before a buffer that size is allocated for it.
func TestATunnelFrameCannotDeclareMoreThanTheBound(t *testing.T) {
	t.Parallel()
	// A length past the bound, followed by a reader that would happily supply
	// that many bytes. Truncating instead would prove nothing: a short read
	// fails on its own, with the bound deleted, and the test would pass while
	// the peer decided how much this process allocates.
	oversized := append([]byte{tunnelKindHello, 0xFF, 0xFF}, make([]byte, 0xFFFF)...)

	_, err := ReadTunnelHello(bytes.NewReader(oversized))

	if !errors.Is(err, ErrTunnelProtocol) {
		t.Fatalf("error = %v, want ErrTunnelProtocol", err)
	}
	if !strings.Contains(err.Error(), "bound") {
		t.Fatalf("error = %v, want it to name the bound that was exceeded", err)
	}
}

// A truncated exchange has to fail rather than block or return half a frame.
func TestATruncatedTunnelFrameFails(t *testing.T) {
	t.Parallel()
	var whole bytes.Buffer
	if err := WriteTunnelAccept(&whole, TunnelAccept{
		Version:     TunnelVersion,
		Status:      TunnelAccepted,
		Fingerprint: bytes.Repeat([]byte{9}, 64),
	}); err != nil {
		t.Fatalf("write accept: %v", err)
	}
	cut := whole.Bytes()[:whole.Len()-8]

	_, err := ReadTunnelAccept(bytes.NewReader(cut))

	if !errors.Is(err, ErrTunnelProtocol) {
		t.Fatalf("error = %v, want ErrTunnelProtocol", err)
	}
}

// The status has to survive being the unhappy one, because that is the case it
// was added for: today a server that is down reaches the client as a road that
// opened and died, and the client goes looking for another road it does not
// need.
func TestTheStatusSaysWhoIsAtFault(t *testing.T) {
	t.Parallel()
	if TunnelUpstreamDown == TunnelAccepted {
		t.Fatal("a server that is down is indistinguishable from one that is not")
	}
	for _, status := range []TunnelStatus{TunnelAccepted, TunnelUpstreamDown, TunnelVersionUnsupported} {
		if status.String() == "" {
			t.Errorf("status %d has no name for the journal", byte(status))
		}
	}
}

// writeCounter counts writes, which is the property the shaping depends on.
type writeCounter struct{ writes int }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}
