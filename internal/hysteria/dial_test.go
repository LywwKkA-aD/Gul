package hysteria

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDialOfficialServerInteroperability(t *testing.T) {
	for _, mode := range []string{"", "salamander", "gecko"} {
		t.Run("obfs="+mode, func(t *testing.T) {
			cfg := startTestServer(t, mode, nil)
			target, backendClosed := startEchoBackend(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := Dial(ctx, cfg, target)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			cancel()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			greeting := make([]byte, len("ready"))
			if _, err := io.ReadFull(conn, greeting); err != nil || string(greeting) != "ready" {
				t.Fatalf("server-initiated greeting = %q, %v", greeting, err)
			}
			payload := bytes.Repeat([]byte("voice-and-chat\x00"), 32768)
			writeDone := make(chan error, 1)
			go func() {
				_, err := conn.Write(payload)
				writeDone <- err
			}()
			echo := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, echo); err != nil {
				t.Fatal(err)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(echo, payload) {
				t.Fatal("full duplex stream corrupted payload")
			}
			var closers sync.WaitGroup
			for range 4 {
				closers.Go(func() { _ = conn.Close() })
			}
			closers.Wait()
			select {
			case <-backendClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("closing returned connection left backend connection open")
			}
		})
	}
}

func TestDialRejectsAuthenticationWithoutLeakingPassword(t *testing.T) {
	cfg := startTestServer(t, "", nil)
	cfg.Password = "wrong-test-password"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, cfg, "127.0.0.1:64738")
	if conn != nil || !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Dial = %v, %v; want authentication error", conn, err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatal("authentication error included password")
	}
}

func TestDialRequiresVerifiedCertificate(t *testing.T) {
	cfg := startTestServer(t, "", nil)
	cfg.TLSConfig = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, cfg, "127.0.0.1:64738")
	if conn != nil || err == nil {
		t.Fatalf("Dial accepted untrusted certificate: %v, %v", conn, err)
	}
	if !terminalRejection(err) {
		t.Fatalf("official client certificate rejection would retry another address: %v", err)
	}
}

func TestStreamDeadlinesAndCloseUnblockReads(t *testing.T) {
	cfg := startTestServer(t, "", nil)
	target, _ := startEchoBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.ReadFull(conn, make([]byte, len("ready"))); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("read past deadline = %v, want timeout", err)
	}
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len("ping"))); err != nil {
		t.Fatalf("connection failed after resetting deadline: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		result <- err
	}()
	_ = conn.Close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read succeeded after closing connection")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock pending Read")
	}
}

func TestDialCancellationInterruptsHandshake(t *testing.T) {
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		conn, err := Dial(ctx, Config{Server: packet.LocalAddr().String(), Password: testPassword}, "127.0.0.1:64738")
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	_ = packet.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := packet.ReadFrom(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	cancel()
	expectCanceled(t, result)
}

func TestDialCancellationInterruptsPendingTargetConnection(t *testing.T) {
	outbound := &blockingOutbound{requested: make(chan struct{}), release: make(chan struct{})}
	cfg := startTestServer(t, "", outbound)
	defer close(outbound.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		conn, err := Dial(ctx, cfg, "127.0.0.1:64738")
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case <-outbound.requested:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not receive target connection request")
	}
	cancel()
	expectCanceled(t, result)
}

func TestDialRejectsInvalidConfigBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
		target string
	}{
		{"empty password", Config{Server: "127.0.0.1:443"}, "localhost:64738"},
		{"unsupported obfs", Config{Server: "127.0.0.1:443", Password: testPassword, Obfuscation: "invalid"}, "localhost:64738"},
		{"insecure TLS", Config{Server: "127.0.0.1:443", Password: testPassword, TLSConfig: &tls.Config{InsecureSkipVerify: true}}, "localhost:64738"},
		{"invalid server", Config{Server: "missing-port", Password: testPassword}, "localhost:64738"},
		{"invalid server port", Config{Server: "localhost:0", Password: testPassword}, "localhost:64738"},
		{"invalid target", Config{Server: "127.0.0.1:443", Password: testPassword}, "missing-port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := Dial(ctx, tc.config, tc.target)
			if conn != nil || err == nil {
				t.Fatalf("Dial accepted invalid configuration: %v, %v", conn, err)
			}
			if tc.name == "empty password" && !errors.Is(err, ErrPasswordRequired) {
				t.Fatalf("error = %v, want ErrPasswordRequired", err)
			}
		})
	}
}

func TestDialAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := Dial(ctx, Config{Server: "localhost:443", Password: testPassword}, "localhost:64738")
	if conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial = %v, %v; want context cancellation", conn, err)
	}
}

func expectCanceled(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Dial error = %v; want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not interrupt Dial")
	}
}
