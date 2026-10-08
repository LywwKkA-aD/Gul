package broker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type deferredAdmissionTransport struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *deferredAdmissionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	d.once.Do(func() { close(d.started) })
	<-d.release
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
}

func TestManagedPendingJoinRemainsFencedUntilProxyCompletes(t *testing.T) {
	h, key, m := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	guest := managedLogin(t, h, "")
	if managedDo(t, h, "POST", "/api/gul/channel", guest.SessionToken, map[string]any{"channelId": 2}).Code != 200 {
		t.Fatal("move failed")
	}
	transport := &deferredAdmissionTransport{started: make(chan struct{}), release: make(chan struct{})}
	h.broker.signalTransport = transport
	mux := http.NewServeMux()
	h.broker.registerAdmission(mux, publicTestConfig())
	var token string
	h.broker.mu.Lock()
	for _, s := range h.broker.sessions {
		if s.ID == guest.SessionID {
			token = h.broker.grantLocked(s, "voice", h.broker.now()).Token
		}
	}
	h.broker.mu.Unlock()
	complete := make(chan struct{})
	go func() {
		defer close(complete)
		r := httptest.NewRequest("GET", "/rtc/validate?access_token="+token, nil)
		mux.ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-transport.started
	m.mu.Lock()
	m.removed = nil
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("POST", "/api/gul/channels/update", strings.NewReader(`{"channelId":2,"version":1,"name":"closed","access":"restricted","allowedMemberIds":[]}`)).WithContext(ctx)
	request.RemoteAddr = "127.0.0.1:12000"
	request.Host = "voice.example.test"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+owner.SessionToken)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "192.0.2.4")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 503 {
		t.Fatal("pending join cleanup falsely completed")
	}
	m.mu.Lock()
	count := len(m.removed)
	m.mu.Unlock()
	if count != 0 {
		t.Fatal("SFU removal ran before pending join completed")
	}
	if managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, map[string]any{"channelId": 2, "version": 2}).Code != 409 {
		t.Fatal("pending join did not fence deletion")
	}
	close(transport.release)
	<-complete
	h.cleanupExpired(context.Background())
	if managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, map[string]any{"channelId": 2, "version": 2}).Code != 200 {
		t.Fatal("finished pending join did not release deletion")
	}
}
