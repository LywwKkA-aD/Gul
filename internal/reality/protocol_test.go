package reality

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVLESSRequestWireVector(t *testing.T) {
	header, err := requestHeader("gul-test-password", "127.0.0.1:64738")
	if err != nil {
		t.Fatal(err)
	}
	// version + UUID + addon length + TCP + port + IPv4 type + address.
	const expected = "000b70bd5c254d8d938a1498359d1ad0fb0001fce2017f000001"
	if hex.EncodeToString(header) != expected {
		t.Fatalf("wire header = %x", header)
	}
	for _, target := range []string{"localhost:64738", "10.0.0.1:64738", "[::1]:64738", "127.0.0.1:0", "127.0.0.1:65536", "bad"} {
		if _, err := requestHeader("test", target); err == nil {
			t.Fatalf("accepted unsupported target %s", target)
		}
	}
}

func TestVLESSResponseRejectsMalformedAndPartialHeaders(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want error
	}{
		{"empty", nil, io.EOF},
		{"partial", []byte{0}, io.ErrUnexpectedEOF},
		{"version", []byte{1, 0}, ErrProtocol},
		{"unsupported-addons", []byte{0, 1}, ErrProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			front, back := net.Pipe()
			defer front.Close()
			go func() { _, _ = back.Write(tc.wire); _ = back.Close() }()
			conn := &vlessConn{Conn: front}
			if n, err := conn.Read(make([]byte, 4)); n != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("Read = %d, %v", n, err)
			}
			if errors.Is(tc.want, ErrProtocol) {
				if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, ErrProtocol) {
					t.Fatalf("lost sticky protocol error: %v", err)
				}
			}
		})
	}
}

func TestVLESSPartialHeaderSurvivesReadDeadline(t *testing.T) {
	front, back := net.Pipe()
	defer front.Close()
	defer back.Close()
	conn := &vlessConn{Conn: front}
	firstWritten := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = back.Write([]byte{0})
		close(firstWritten)
		<-release
		_, _ = back.Write([]byte{0, 'o', 'k'})
	}()
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, err := conn.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("partial header timeout = %v", err)
	}
	<-firstWritten
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	close(release)
	payload := make([]byte, 2)
	if _, err := io.ReadFull(conn, payload); err != nil || string(payload) != "ok" {
		t.Fatalf("resumed header = %q, %v", payload, err)
	}
	if n, err := conn.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty Read = %d, %v", n, err)
	}
}

func TestREALITYCancellationClosesStalledHandshake(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			cfg := startRealityFixture(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cfg.Server = listener.Addr().String()
			accepted, closed := make(chan struct{}), make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				close(accepted)
				_, _ = io.Copy(io.Discard, conn)
				close(closed)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				conn, err := Dial(ctx, cfg, "127.0.0.1:64738")
				if conn != nil {
					_ = conn.Close()
				}
				result <- err
			}()
			<-accepted
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-result:
				want := context.Canceled
				if mode == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("canceled handshake = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt REALITY handshake")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("canceled handshake left TCP connection open")
			}
		})
	}
}

func TestREALITYRejectsOrdinaryTLSWithoutSendingVLESSIdentity(t *testing.T) {
	cfg := startRealityFixture(t)
	certificateSource := httptest.NewTLSServer(nil)
	certificate := certificateSource.TLS.Certificates[0]
	certificateSource.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.Server = listener.Addr().String()
	applicationData := make(chan []byte, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			applicationData <- nil
			return
		}
		defer raw.Close()
		_ = raw.SetDeadline(time.Now().Add(time.Second))
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13})
		data := make([]byte, 1024)
		n, _ := conn.Read(data)
		applicationData <- data[:n]
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := Dial(ctx, cfg, "127.0.0.1:64738")
	if conn != nil || !errors.Is(err, ErrAuthentication) {
		t.Fatalf("accepted ordinary certificate = %v, %v", conn, err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatal("error leaked password")
	}
	if got := <-applicationData; !bytes.Equal(got, nil) {
		t.Fatal("sent application data before authenticating REALITY")
	}
}
