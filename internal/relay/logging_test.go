package relay

import (
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestHandlerLogsSessionLifecycle(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.Logger = logger
	h := mustHandler(t, cfg)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Framed, the way a client sends: the counters below are of the bytes that
	// reached Murmur, which is the payload and not the padding around it.
	stream := relayproto.Shape(relayproto.AsMessageConn(websocket.NetConn(t.Context(), conn, websocket.MessageBinary)))
	completeTunnel(t, stream)
	if _, err := stream.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(stream, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close: %v", err)
	}

	opened := recordAttrs(records.await(t, "relay session opened"))
	if opened["source"] == "" {
		t.Fatalf("session open record has no source: %v", opened)
	}
	closed := recordAttrs(records.await(t, "relay session closed"))
	for _, key := range []string{"source", "duration", "bytes_from_client", "bytes_to_client", "reason"} {
		if _, ok := closed[key]; !ok {
			t.Fatalf("session close record is missing %q: %v", key, closed)
		}
	}
	if closed["bytes_from_client"] != "4" || closed["bytes_to_client"] != "4" {
		t.Fatalf("session close byte counts = %v", closed)
	}
}

func TestHandlerLogsCapacityRejection(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.MaxConnections = 1
	cfg.MaxConnectionsPerIP = 1
	cfg.Logger = logger
	h := mustHandler(t, cfg)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	}

	first, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	t.Cleanup(func() { _ = first.CloseNow() })
	waitForActiveSessions(t, h, 1)
	if _, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts); err == nil {
		t.Fatal("second dial unexpectedly succeeded")
	}

	attrs := recordAttrs(records.await(t, "relay capacity rejected"))
	if attrs["scope"] != "global" {
		t.Fatalf("capacity rejection scope = %q, want global", attrs["scope"])
	}
}

func TestHandlerLogsUpstreamDialFailure(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.Upstream = closedUpstream(t)
	cfg.Logger = logger
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	sayHello(t, relayproto.Shape(relayproto.AsMessageConn(
		websocket.NetConn(t.Context(), conn, websocket.MessageBinary))))

	attrs := recordAttrs(records.await(t, "relay upstream dial failed"))
	if attrs["error"] == "" {
		t.Fatalf("upstream failure record has no error: %v", attrs)
	}
}

func TestHandlerLogsAuthorizationBanActivation(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 1
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.Logger = logger
	h := mustHandler(t, cfg)

	for range 2 {
		_ = serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	}

	attrs := recordAttrs(records.await(t, "relay authorization ban activated"))
	if attrs["source"] != "192.0.2.10" {
		t.Fatalf("ban record source = %q, want 192.0.2.10", attrs["source"])
	}
	if attrs["retry_after"] == "" {
		t.Fatalf("ban record has no retry_after: %v", attrs)
	}
}

// closedUpstream returns an address nothing listens on: the port was bound and
// released, so a dial fails immediately instead of hanging.
func closedUpstream(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return address
}
