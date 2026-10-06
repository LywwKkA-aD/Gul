package mumble

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/hysteria"
	"github.com/LywwKkA-aD/Gul/internal/identity"
	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumble/proto/MumbleProto"
	"google.golang.org/protobuf/proto"
)

func TestHysteriaSessionSyncChatVoiceAndIdentity(t *testing.T) {
	for _, mode := range []string{"", "salamander", "gecko"} {
		t.Run("obfs="+mode, func(t *testing.T) {
			backend := startMumbleFixture(t, true)
			proxy := startHysteriaFixture(t, backend.address, mode, []string{hysteriaFixturePassword})
			tofu := NewTOFUStore(t.TempDir(), testLogger(t))
			seed := bytes.Repeat([]byte{7}, identity.SeedBytes)
			expected, err := identity.ForHost(seed, proxy.endpoint.host)
			if err != nil {
				t.Fatal(err)
			}
			messages := make(chan string, 1)
			received := newDropBuffer[VoicePacket](4)
			listener := newVoiceListener(received)
			defer listener.stop()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			session, err := dial(DialConfig{
				Context: ctx, Address: proxy.endpoint.address,
				Username: "gul-integration", Password: hysteriaFixturePassword,
				OuterRoots: proxy.roots, IdentitySeed: seed,
			}, tofu, sessionHooks{
				textMessage: func(e *gumble.TextMessageEvent) { messages <- e.Message },
				audio:       listener,
			}, testLogger(t))
			if err != nil {
				t.Fatalf("complete Hysteria/Mumble connection failed: %v", err)
			}
			defer session.Disconnect()
			cancel() // Setup cancellation must not terminate an established session.
			if session.State() != "synced" {
				t.Fatalf("session state = %s, want synced", session.State())
			}
			select {
			case login := <-backend.logins:
				if login.username != "gul-integration" || !login.validAuth || !login.opus {
					t.Fatal("Mumble did not receive the expected authenticated Opus session")
				}
				if login.fingerprint != expected.Fingerprint {
					t.Fatal("Mumble TLS received a different client identity")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Mumble fixture did not receive authentication")
			}
			sum := sha256.Sum256(backend.certificate.Certificate[0])
			if got, ok := tofu.Fingerprint(proxy.endpoint.host); !ok || got != hex.EncodeToString(sum[:]) {
				t.Fatal("TOFU did not pin the actual Mumble TLS certificate")
			}
			select {
			case target := <-proxy.requests:
				if target != "127.0.0.1:64738" {
					t.Fatal("Gul requested an unexpected proxy destination")
				}
			default:
				t.Fatal("Hysteria did not receive a backend request")
			}
			const probe = "chat through the official Hysteria server"
			if err := session.client.Conn.WriteProto(&MumbleProto.TextMessage{
				ChannelId: []uint32{0}, Message: proto.String(probe),
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-messages:
				if message != probe {
					t.Fatal("chat payload changed in transit")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("chat response did not reach the client")
			}
			silence := []byte{0xf8, 0xff, 0xfe}
			if err := session.client.Conn.WriteAudio(voiceCodecOpus, 0, 7, true, silence, nil, nil, nil, false); err != nil {
				t.Fatal(err)
			}
			select {
			case packet := <-received.out():
				if !bytes.Equal(packet.Opus, silence) || packet.Sequence != 7 || !packet.Final || packet.Key != "h:"+expected.Fingerprint {
					t.Fatalf("voice packet changed in transit: sequence=%d final=%v", packet.Sequence, packet.Final)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("voice response did not reach the client")
			}
			_ = session.Disconnect()
			select {
			case <-backend.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("Disconnect left the proxied Mumble socket open")
			}
		})
	}
}

func TestHysteriaSessionRejectsAChangedMumbleCertificate(t *testing.T) {
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	first := startMumbleFixture(t, true)
	firstProxy := startHysteriaFixture(t, first.address, "", []string{hysteriaFixturePassword})
	session, err := Dial(DialConfig{
		Context: t.Context(), Address: firstProxy.endpoint.address,
		Username: "gul-tofu", Password: hysteriaFixturePassword, OuterRoots: firstProxy.roots,
	}, tofu, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Disconnect()

	replacement := startMumbleFixture(t, true)
	nextProxy := startHysteriaFixture(t, replacement.address, "", []string{hysteriaFixturePassword})
	session, err = Dial(DialConfig{
		Context: t.Context(), Address: nextProxy.endpoint.address,
		Username: "gul-tofu", Password: hysteriaFixturePassword, OuterRoots: nextProxy.roots,
	}, tofu, testLogger(t))
	if session != nil {
		_ = session.Disconnect()
	}
	if !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("changed Mumble certificate returned %v, want TOFU mismatch", err)
	}
	select {
	case <-replacement.logins:
		t.Fatal("Mumble received authentication before its certificate was accepted")
	default:
	}
}

func TestHysteriaSessionWrongPasswordNeverReachesMumble(t *testing.T) {
	proxy := startHysteriaFixture(t, "127.0.0.1:1", "", []string{hysteriaFixturePassword})
	const wrongPassword = "incorrect-private-test-password"
	session, err := Dial(DialConfig{
		Context: t.Context(), Address: proxy.endpoint.address,
		Username: "gul-auth", Password: wrongPassword, OuterRoots: proxy.roots,
	}, NewTOFUStore(t.TempDir(), testLogger(t)), testLogger(t))
	if session != nil {
		_ = session.Disconnect()
	}
	if !errors.Is(err, hysteria.ErrAuthentication) {
		t.Fatalf("wrong password returned %v, want authentication rejection", err)
	}
	if strings.Contains(err.Error(), wrongPassword) {
		t.Fatal("authentication error exposed the password")
	}
	select {
	case <-proxy.requests:
		t.Fatal("Hysteria opened a backend connection before authentication")
	default:
	}
}

func TestHysteriaSessionCancellationInterruptsMumbleSync(t *testing.T) {
	backend := startMumbleFixture(t, false)
	proxy := startHysteriaFixture(t, backend.address, "", []string{hysteriaFixturePassword})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	finished := make(chan error, 1)
	go func() {
		session, err := Dial(DialConfig{
			Context: ctx, Address: proxy.endpoint.address, Username: "gul-cancel",
			Password: hysteriaFixturePassword, OuterRoots: proxy.roots,
		}, tofu, testLogger(t))
		if session != nil {
			_ = session.Disconnect()
		}
		finished <- err
	}()
	select {
	case <-backend.logins:
	case err := <-finished:
		t.Fatalf("connection ended before synchronization: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Mumble never reached the synchronization phase")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled synchronization returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not interrupt Mumble synchronization")
	}
	select {
	case <-backend.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled synchronization left the backend connection open")
	}
}

func TestHysteriaSessionDisconnectReleasesClientAfterRemoteEOF(t *testing.T) {
	backend := startMumbleFixture(t, true)
	proxy := startHysteriaFixture(t, backend.address, "", []string{hysteriaFixturePassword})
	dropped := make(chan struct{}, 1)
	session, err := dial(DialConfig{
		Context: t.Context(), Address: proxy.endpoint.address, Username: "gul-drop",
		Password: hysteriaFixturePassword, OuterRoots: proxy.roots,
	}, NewTOFUStore(t.TempDir(), testLogger(t)), sessionHooks{
		disconnect: func(*gumble.DisconnectEvent) { dropped <- struct{}{} },
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Disconnect()
	backend.drop()
	select {
	case <-dropped:
	case <-time.After(2 * time.Second):
		t.Fatal("Mumble did not report the remote EOF")
	}
	if session.State() != "disconnected" {
		t.Fatal("regression fixture did not reach the already-disconnected state")
	}
	var closers sync.WaitGroup
	for range 8 {
		closers.Go(func() { _ = session.Disconnect() })
	}
	closers.Wait()
	select {
	case <-proxy.disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("remote EOF left the Hysteria client alive after Session.Disconnect")
	}
}
