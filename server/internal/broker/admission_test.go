package broker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

func signedTestClaims(t *testing.T, h *PublicHandler, original string, change func(map[string]any)) string {
	t.Helper()
	claims := publicClaims(t, original)
	change(claims)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims)).SignedString([]byte(h.broker.cfg.APISecret))
	if err != nil {
		t.Fatal("fixture token signing failed")
	}
	return token
}
func TestManagedAdmissionChecksEveryRouteAndSignedSessionNonce(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	h, key, _ := managedFixtureAt(t, upstream.URL)
	owner := managedLogin(t, h, key.Credential)
	for _, path := range []string{"/rtc", "/rtc/validate", "/rtc/v1", "/rtc/v1/validate"} {
		if managedDo(t, h, "GET", path+"?access_token="+owner.Grant.Token, "", nil).Code != 200 {
			t.Fatal("valid media admission denied")
		}
	}
	before := calls.Load()
	for _, change := range []func(map[string]any){func(c map[string]any) { delete(c, "exp") }, func(c map[string]any) { c["iss"] = "foreign" }, func(c map[string]any) { c["sub"] = "screen.1" }, func(c map[string]any) { c["attributes"].(map[string]any)["sessionNonce"] = "" }, func(c map[string]any) { c["attributes"].(map[string]any)["serverId"] = strings.Repeat("a", 32) }, func(c map[string]any) { c["attributes"].(map[string]any)["memberId"] = "" }, func(c map[string]any) { c["video"].(map[string]any)["room"] = "gul-channel-2" }} {
		token := signedTestClaims(t, h, owner.Grant.Token, change)
		if managedDo(t, h, "GET", "/rtc?access_token="+token, "", nil).Code != 403 {
			t.Fatal("invalid signed admission accepted")
		}
	}
	if managedDo(t, h, "GET", "/rtc?access_token="+owner.Grant.Token, owner.Grant.Token, nil).Code != 401 {
		t.Fatal("ambiguous media credentials accepted")
	}
	if managedDo(t, h, "GET", "/rtc?access_token="+owner.Grant.Token+"&access_token="+owner.Grant.Token, "", nil).Code != 401 {
		t.Fatal("duplicate query tokens accepted")
	}
	if calls.Load() != before {
		t.Fatal("denied media reached SFU")
	}
}
func TestManagedRefreshedTokenCannotSurviveMoveLogoutOrSessionIDReuse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	h, key, _ := managedFixtureAt(t, upstream.URL)
	owner := managedLogin(t, h, key.Credential)
	refreshed := signedTestClaims(t, h, owner.Grant.Token, func(c map[string]any) { c["exp"] = time.Now().Add(time.Hour).Unix() })
	if managedDo(t, h, "GET", "/rtc/v1/validate?access_token="+refreshed, "", nil).Code != 200 {
		t.Fatal("SFU refresh attributes were rejected")
	}
	if managedDo(t, h, "POST", "/api/gul/channel", owner.SessionToken, map[string]any{"channelId": 2}).Code != 200 {
		t.Fatal("move failed")
	}
	if managedDo(t, h, "GET", "/rtc?access_token="+refreshed, "", nil).Code != 403 {
		t.Fatal("refreshed old-channel token replayed")
	}
	if managedDo(t, h, "POST", "/api/gul/logout", owner.SessionToken, map[string]any{}).Code != 204 {
		t.Fatal("logout failed")
	}
	h.broker.newID = func() uint32 { return owner.SessionID }
	replacement := managedLogin(t, h, key.Credential)
	if replacement.SessionID != owner.SessionID {
		t.Fatal("reuse fixture invalid")
	}
	if managedDo(t, h, "GET", "/rtc?access_token="+refreshed, "", nil).Code != 403 {
		t.Fatal("old nonce replayed against reused session ID")
	}
}

func TestManagedLongRefreshedTokenRequiresLiveBrokerLease(t *testing.T) {
	h, key, _ := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	now := time.Now()
	h.broker.now = func() time.Time { return now }
	refreshed := signedTestClaims(t, h, owner.Grant.Token, func(c map[string]any) { c["exp"] = now.Add(time.Hour).Unix() })
	h.broker.mu.Lock()
	accepted := h.broker.admitLocked(refreshed) != nil
	h.broker.mu.Unlock()
	if !accepted {
		t.Fatal("active refreshed grant denied")
	}
	now = now.Add(61 * time.Second)
	h.broker.mu.Lock()
	accepted = h.broker.admitLocked(refreshed) != nil
	h.broker.mu.Unlock()
	if accepted {
		t.Fatal("long-lived media refresh outlived broker lease")
	}
}
func TestManagedACLClosesUpgradedSignalBeforeRemovingParticipants(t *testing.T) {
	opened := make(chan struct{})
	closed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(opened)
		defer close(closed)
		defer socket.Close()
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, key, m := managedFixtureAt(t, upstream.URL)
	owner := managedLogin(t, h, key.Credential)
	guest := managedLogin(t, h, "")
	if managedDo(t, h, "POST", "/api/gul/channel", guest.SessionToken, map[string]any{"channelId": 2}).Code != 200 {
		t.Fatal("move failed")
	}
	// Acquire this channel's new generation without exposing either token.
	var grant string
	h.broker.mu.Lock()
	for _, s := range h.broker.sessions {
		if s.ID == guest.SessionID {
			grant = h.broker.grantLocked(s, "voice", h.broker.now()).Token
		}
	}
	h.broker.mu.Unlock()
	server := httptest.NewServer(h)
	defer server.Close()
	headers := http.Header{"Host": []string{"voice.example.test"}, "X-Forwarded-Proto": []string{"https"}, "X-Forwarded-For": []string{"192.0.2.4"}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/rtc?access_token="+grant, headers)
	if err != nil {
		t.Fatal("owned signaling fixture failed")
	}
	defer socket.Close()
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("upstream never opened")
	}
	m.mu.Lock()
	m.removed = nil
	m.mu.Unlock()
	w := managedDo(t, h, "POST", "/api/gul/channels/update", owner.SessionToken, map[string]any{"channelId": 2, "version": 1, "name": "closed", "access": "restricted", "allowedMemberIds": []string{}})
	if w.Code != 200 {
		t.Fatal("ACL close failed")
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("stale signaling remained connected")
	}
	m.mu.Lock()
	removed := len(m.removed)
	m.mu.Unlock()
	if removed != 2 {
		t.Fatal("both media identities were not removed")
	}
	if managedDo(t, h, "GET", "/rtc?access_token="+grant, "", nil).Code != 403 {
		t.Fatal("revoked signal replayed")
	}
}
