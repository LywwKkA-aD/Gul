package mumble

import (
	"errors"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/hysteria"
	"github.com/LywwKkA-aD/Gul/internal/reality"
)

const testRelayAddress = "hysteria2://murmur.example.test"

func TestOldTransportHintsCannotEnableLegacyConnections(t *testing.T) {
	for _, hint := range []Transport{"", "wss", "quic", "direct", "hysteria"} {
		t.Run(string(hint), func(t *testing.T) {
			sink := newStatusSink()
			m := newTestManager(t, Callbacks{OnStatus: sink.record})
			m.PreferTransport(testRelayAddress, string(hint))
			dials := make(chan Transport, 2)
			m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
				dials <- cfg.Transport
				return nil, errors.New("network unavailable")
			}
			m.Connect(testRelayAddress, "gul", "test password")
			sink.expect(t, domain.StateConnecting)
			sink.expect(t, domain.StateDisconnected)
			m.Disconnect()
			if got := <-dials; got != TransportHysteria {
				t.Fatalf("dial used %q", got)
			}
			if len(dials) != 0 {
				t.Fatal("failure silently tried another transport")
			}
		})
	}
}

func TestManagerRealityEndpointCannotFallBack(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.PreferTransport(testRealityAddress, string(TransportHysteria))
	dials := make(chan Transport, 2)
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		dials <- cfg.Transport
		return nil, errors.New("network unavailable")
	}
	m.Connect(testRealityAddress, "gul", "test password")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateDisconnected)
	m.Disconnect()
	if got := <-dials; got != TransportReality {
		t.Fatalf("VLESS profile used %q", got)
	}
	if len(dials) != 0 {
		t.Fatal("failure silently tried another transport")
	}
}

func TestRealityAuthenticationErrorsAreTerminal(t *testing.T) {
	for _, err := range []error{reality.ErrPasswordRequired, reality.ErrAuthentication} {
		if !isTerminalDialError(&realityError{cause: err, text: "safe error"}) {
			t.Fatalf("invalid REALITY credentials would reconnect indefinitely: %v", err)
		}
	}
}

func TestHysteriaAuthFailureIsTerminal(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.dialFn = func(_ DialConfig, _ sessionHooks) (*Session, error) {
		return nil, hysteria.ErrAuthentication
	}
	m.Connect(testRelayAddress, "gul", "wrong")
	sink.expect(t, domain.StateConnecting)
	status := sink.expect(t, domain.StateDisconnected)
	if status.Error != hysteria.ErrAuthentication.Error() {
		t.Fatalf("error = %q", status.Error)
	}
}

func TestRememberedHysteriaSuccessOnlyNotifiesOnChange(t *testing.T) {
	chooser := newTransportChooser()
	if !chooser.succeeded(testRelayAddress, TransportHysteria) {
		t.Fatal("first success was lost")
	}
	if chooser.succeeded(testRelayAddress, TransportHysteria) {
		t.Fatal("repeat success rewrites settings")
	}
	chooser.failed(testRelayAddress)
	if !chooser.succeeded(testRelayAddress, TransportHysteria) {
		t.Fatal("recovered connection was not recorded")
	}
}
