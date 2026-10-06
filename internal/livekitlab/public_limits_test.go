package livekitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

func TestPublicGlobalLoginLimitBoundsClientMap(t *testing.T) {
	limits := newPublicLoginLimits()
	now := time.Unix(1_800_000_000, 0)
	limits.now = func() time.Time { return now }
	for i := 0; i < 5000; i++ {
		ip := netip.AddrFrom4([4]byte{192, 0, byte(i / 256), byte(i % 256)})
		if got := limits.allow(ip); got != (i < 120) {
			t.Fatal("global login limit not enforced")
		}
	}
	if len(limits.clients) > 120 {
		t.Fatal("blocked login traffic grew client map")
	}
	now = now.Add(time.Minute)
	if !limits.allow(netip.MustParseAddr("192.0.2.1")) || len(limits.clients) != 1 {
		t.Fatal("old rate buckets not collected")
	}
}

func TestPublicActiveSessionLimit(t *testing.T) {
	h, err := NewPublicHandler(publicTestConfig(), &fakeParticipantRemover{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(livekitapi.LoginRequest{Username: "friend", Password: publicTestPassword})
	for i := 0; i < 33; i++ {
		w := publicRequest(h, "POST", "/api/gul/login", "", string(body), func(r *http.Request) { r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i+1)) })
		want := 200
		if i == 32 {
			want = 429
		}
		if w.Code != want {
			t.Fatal("incorrect public session bound")
		}
	}
	if len(h.broker.sessions) != 32 {
		t.Fatal("session count exceeded bound")
	}
}
