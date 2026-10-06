package livekitlab

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestPublicRoomServiceRemovalUsesScopedAuthentication(t *testing.T) {
	cfg := publicTestConfig()
	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/twirp/livekit.RoomService/RemoveParticipant" || r.Method != "POST" {
			t.Error("unexpected admin operation")
			w.WriteHeader(500)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Error("missing scoped admin token")
			w.WriteHeader(401)
			return
		}
		mac := hmac.New(sha256.New, []byte(cfg.APISecret))
		_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		if !hmac.Equal(signature, mac.Sum(nil)) {
			t.Error("invalid admin token signature")
		}
		claims := publicClaims(t, token)
		video := claims["video"].(map[string]any)
		if claims["iss"] != cfg.APIKey || video["roomAdmin"] != true || video["room"] != "gul-channel-2" {
			t.Error("admin JWT scope mismatch")
		}
		body, _ := io.ReadAll(r.Body)
		var input livekit.RoomParticipantIdentity
		if proto.Unmarshal(body, &input) != nil || input.Room != "gul-channel-2" || input.Identity != "screen.123" {
			t.Error("incorrect admin removal target")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"code":"not_found","msg":"participant already absent"}`)
	}))
	defer upstream.Close()
	cfg.LiveKitInternalURL = upstream.URL
	h, err := NewPublicHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.broker.remover.RemoveParticipant(context.Background(), "gul-channel-2", "screen.123"); err != nil {
		t.Fatal("already absent participant was not idempotent")
	}
	if requests != 1 {
		t.Fatal("missing RoomService call")
	}
}

type blockingParticipantRemover struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingParticipantRemover) RemoveParticipant(ctx context.Context, _, _ string) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPublicSlowRemovalDoesNotBlockOtherSessions(t *testing.T) {
	remover := &blockingParticipantRemover{started: make(chan struct{}), release: make(chan struct{})}
	h, err := NewPublicHandler(publicTestConfig(), remover)
	if err != nil {
		t.Fatal(err)
	}
	one, two := publicLogin(t, h), publicLogin(t, h)
	result := make(chan int, 1)
	go func() {
		result <- publicRequest(h, "POST", "/api/gul/channel", one.SessionToken, `{"channelId":2}`, nil).Code
	}()
	select {
	case <-remover.started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	polled := make(chan int, 1)
	go func() { polled <- publicRequest(h, "GET", "/api/gul/state", two.SessionToken, "", nil).Code }()
	select {
	case status := <-polled:
		if status != 200 {
			t.Fatal("other session polling failed")
		}
	case <-time.After(time.Second):
		close(remover.release)
		t.Fatal("global mutex held during SFU call")
	}
	close(remover.release)
	if <-result != 200 {
		t.Fatal("channel transition failed")
	}
}

func TestPublicConcurrentTransitionsStaySerialized(t *testing.T) {
	remover := &fakeParticipantRemover{}
	h, err := NewPublicHandler(publicTestConfig(), remover)
	if err != nil {
		t.Fatal(err)
	}
	login := publicLogin(t, h)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			w := publicRequest(h, "POST", "/api/gul/channel", login.SessionToken, `{"channelId":2}`, nil)
			if w.Code != 200 {
				t.Error("concurrent channel request failed")
			}
		})
	}
	wg.Wait()
	state := gulResponse[livekitapi.State](t, publicRequest(h, "GET", "/api/gul/state", login.SessionToken, "", nil))
	if state.SelfChannel != 2 || state.Revision != 2 || len(remover.calls) != 2 {
		t.Fatal("duplicate transition or media cleanup")
	}
	if w := publicRequest(h, "POST", "/api/gul/logout", login.SessionToken, "", nil); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if len(remover.calls) != 4 || len(h.broker.sessions) != 0 {
		t.Fatal("logout left active session or media")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.RunMaintenance(ctx)
}
