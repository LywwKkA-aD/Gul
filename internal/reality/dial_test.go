package reality

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUserIDDerivation(t *testing.T) {
	// Independent Python hashlib/uuid vector for the deployment helper.
	if got := UserID("gul-test-password"); got != "0b70bd5c-254d-8d93-8a14-98359d1ad0fb" {
		t.Fatalf("unexpected derivation: %s", got)
	}
	if UserID(" password") == UserID("password") || UserID("password") == UserID("PASSWORD") {
		t.Fatal("password derivation must preserve the original UTF-8 bytes")
	}
}

func TestDialValidatesConfiguration(t *testing.T) {
	valid := Config{Server: "127.0.0.1:443", ServerName: "example.com", PublicKey: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), ShortID: "01ab", Password: "test-password"}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		target string
	}{
		{"password", func(c *Config) { c.Password = "" }, "127.0.0.1:64738"},
		{"server", func(c *Config) { c.Server = "secret@host:443" }, "127.0.0.1:64738"},
		{"port", func(c *Config) { c.Server = "host:0" }, "127.0.0.1:64738"},
		{"name", func(c *Config) { c.ServerName = "https://example.com" }, "127.0.0.1:64738"},
		{"ip-name", func(c *Config) { c.ServerName = "127.0.0.1" }, "127.0.0.1:64738"},
		{"key", func(c *Config) { c.PublicKey = "secret-invalid-key" }, "127.0.0.1:64738"},
		{"padded-key", func(c *Config) { c.PublicKey += "=" }, "127.0.0.1:64738"},
		{"short-id", func(c *Config) { c.ShortID = "xyz" }, "127.0.0.1:64738"},
		{"empty-id", func(c *Config) { c.ShortID = "" }, "127.0.0.1:64738"},
		{"target", func(c *Config) {}, "example.com:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.change(&cfg)
			conn, err := Dial(context.Background(), cfg, tc.target)
			if conn != nil || err == nil {
				t.Fatalf("invalid configuration accepted: %v, %v", conn, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-password") {
				t.Fatal("error leaked configuration")
			}
			if tc.name == "password" && !errors.Is(err, ErrPasswordRequired) {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if conn, err := Dial(ctx, valid, "127.0.0.1:64738"); conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial = %v, %v", conn, err)
	}
}

func TestOfficialREALITYFullDuplexAndLifetime(t *testing.T) {
	cfg := startRealityFixture(t)
	target, backendClosed := startEchoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialTarget(ctx, cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cancel() // Setup cancellation must not own a returned stream.
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("voice-and-chat\x00"), 32768)
	written := make(chan error, 1)
	go func() { _, err := conn.Write(payload); written <- err }()
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, echo) {
		t.Fatal("stream corrupted payload")
	}
	var closers sync.WaitGroup
	for range 4 {
		closers.Go(func() { _ = conn.Close() })
	}
	closers.Wait()
	select {
	case <-backendClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("close left the remote target connected")
	}
}

func TestREALITYRejectsWrongKeyAndVLESSPassword(t *testing.T) {
	for _, wrong := range []string{"key", "password", "short-id", "server-name"} {
		t.Run(wrong, func(t *testing.T) {
			cfg := startRealityFixture(t)
			switch wrong {
			case "key":
				cfg.PublicKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
			case "password":
				cfg.Password = "wrong-password-must-not-leak"
			case "short-id":
				cfg.ShortID = "ff"
			case "server-name":
				cfg.ServerName = "wrong.example"
			}
			target, connected := startEchoFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := dialTarget(ctx, cfg, target)
			if wrong != "password" {
				if conn != nil || !errors.Is(err, ErrAuthentication) {
					t.Fatalf("invalid REALITY authentication = %v, %v", conn, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
			_, _ = conn.Write([]byte("unauthorized"))
			if _, err := conn.Read(make([]byte, 1)); err == nil {
				t.Fatal("invalid credentials reached the target")
			}
			_ = conn.Close()
			select {
			case <-connected:
				t.Fatal("unauthorized connection reached the target")
			default:
			}
		})
	}
}

func TestREALITYReadDeadlineCanBeResetAndCloseUnblocks(t *testing.T) {
	cfg := startRealityFixture(t)
	target, _ := startEchoFixture(t)
	conn, err := dialTarget(context.Background(), cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("read timeout = %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Time{})
	readDone := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
	_ = conn.Close()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock read")
	}
}
