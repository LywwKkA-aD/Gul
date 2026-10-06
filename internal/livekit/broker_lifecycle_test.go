package livekit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

func TestBrokerConnectionsCloseWhenManagerRunEnds(t *testing.T) {
	for _, failLogin := range []bool{false, true} {
		name := "disconnect"
		if failLogin {
			name = "failed_login"
		}
		t.Run(name, func(t *testing.T) {
			var opened, idle, closed atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/gul/login":
					_, _ = io.Copy(io.Discard, r.Body)
					if failLogin {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_ = json.NewEncoder(w).Encode(fixtureLogin(1))
				case "/api/gul/audio":
					var state api.AudioState
					if json.NewDecoder(r.Body).Decode(&state) != nil {
						t.Error("invalid audio request")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_ = json.NewEncoder(w).Encode(state)
				case "/api/gul/state":
					_ = json.NewEncoder(w).Encode(api.State{SelfSession: 7, SelfChannel: 1, Revision: 1})
				case "/api/gul/logout":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected broker route %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				switch state {
				case http.StateNew:
					opened.Add(1)
				case http.StateIdle:
					idle.Add(1)
				case http.StateClosed:
					closed.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), session.Callbacks{})
			defer manager.Close()
			manager.brokerFactory = func(string) brokerAPI { return newBroker(server.URL) }
			manager.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) { return &fakeMedia{}, nil }
			manager.Connect("http://127.0.0.1:8787", "alice", "")
			manager.mu.Lock()
			run := manager.run
			manager.mu.Unlock()
			if failLogin {
				waitFor(t, func() bool { return manager.Status().Error == ErrAuthentication.Error() })
			} else {
				waitFor(t, func() bool { return manager.Status().State == domain.StateConnected && idle.Load() > 0 })
				manager.Disconnect()
			}
			select {
			case <-run.done:
			case <-time.After(time.Second):
				t.Fatal("manager run did not finish")
			}
			if opened.Load() == 0 || idle.Load() == 0 {
				t.Fatal("fixture never established a reusable connection")
			}
			deadline := time.Now().Add(time.Second)
			for closed.Load() != opened.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if closed.Load() != opened.Load() {
				t.Fatalf("broker kept sockets after run exit: opened=%d closed=%d", opened.Load(), closed.Load())
			}
		})
	}
}
