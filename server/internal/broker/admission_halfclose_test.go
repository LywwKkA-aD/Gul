package broker

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManagedSignalWriterPreservesResponseController(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := signalResponseWriter{ResponseWriter: recorder, ctx: context.Background()}
	writer.Header().Set("Content-Type", "text/plain")
	writer.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(writer, "accepted")
	controller := http.NewResponseController(writer)
	if controller.Flush() != nil || !recorder.Flushed || recorder.Code != http.StatusAccepted || recorder.Body.String() != "accepted" {
		t.Fatal("non-upgraded response delegation changed")
	}
	if _, _, err := controller.Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatal("unsupported hijack changed its response-controller result")
	}
}

func TestManagedCleanupClosesAlreadyHalfClosedSignal(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = socket.Close()
		}
	}))
	defer upstream.Close()
	h, _, m := managedFixtureAt(t, upstream.URL)
	login := managedLogin(t, h, "")
	server := httptest.NewServer(h)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal("fixture dial failed")
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	req := &http.Request{Method: "GET", URL: &url.URL{Path: "/rtc", RawQuery: "access_token=" + login.Grant.Token}, Host: "voice.example.test", Header: http.Header{
		"Connection": []string{"Upgrade"}, "Upgrade": []string{"websocket"}, "Sec-Websocket-Key": []string{"dGhlIHNhbXBsZSBub25jZQ=="}, "Sec-Websocket-Version": []string{"13"}, "X-Forwarded-Proto": []string{"https"}, "X-Forwarded-For": []string{"192.0.2.4"},
	}}
	if req.Write(conn) != nil {
		t.Fatal("fixture handshake write failed")
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil || response.StatusCode != 101 {
		t.Fatal("fixture did not upgrade")
	}
	// Observe the downstream read half-close without closing its write half.
	// ReverseProxy has now completed its upstream-to-client copy with nil error.
	if _, err = reader.ReadByte(); err != io.EOF {
		t.Fatal("fixture did not observe half-close")
	}
	h.broker.mu.Lock()
	var session *gulSession
	for _, s := range h.broker.sessions {
		if s.ID == login.SessionID {
			session = s
		}
	}
	active := len(h.broker.flows)
	h.broker.mu.Unlock()
	if active != 1 || session == nil {
		t.Fatal("fixture has no tracked half-closed flow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := h.broker.cleanSessionMedia(ctx, session); err != nil {
		t.Fatal("cancelled half-closed signaling flow blocked session media cleanup")
	}
	m.mu.Lock()
	removed := len(m.removed)
	m.mu.Unlock()
	if removed != 2 {
		t.Fatal("cleanup did not remove both media identities")
	}
}
