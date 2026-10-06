package livekitlab

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

func gulHandler(now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	newGulBroker(Config{APIKey: "test-key", APISecret: strings.Repeat("s", 32)}, now).register(mux)
	return localOnly(mux)
}

func gulRequest(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func gulResponse[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected response status: %d", w.Code)
	}
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal("invalid response JSON")
	}
	return result
}

func gulLogin(t *testing.T, h http.Handler, name string) livekitapi.LoginResponse {
	t.Helper()
	body, _ := json.Marshal(livekitapi.LoginRequest{Username: name})
	return gulResponse[livekitapi.LoginResponse](t, gulRequest(h, "POST", "/api/gul/login", "", string(body)))
}

func gulState(t *testing.T, h http.Handler, token string) livekitapi.State {
	t.Helper()
	return gulResponse[livekitapi.State](t, gulRequest(h, "GET", "/api/gul/state", token, ""))
}

func allGulUsers(tree domain.ChannelNode) []domain.UserInfo {
	users := append([]domain.UserInfo{}, tree.Users...)
	for _, child := range tree.Children {
		users = append(users, allGulUsers(child)...)
	}
	return users
}

func TestGulLoginRosterAndRoleGrants(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := gulHandler(func() time.Time { return now })
	first := gulLogin(t, h, "  Тест Первый  ")
	second := gulLogin(t, h, "Тест Второй")
	if first.SessionToken == "" || len(first.SessionToken) != 43 || first.SessionID == 0 || first.SessionID > 0x7fffffff || first.SessionID == second.SessionID {
		t.Fatal("invalid session identifiers")
	}
	if first.Name != "Тест Первый" || first.Identity != "voice."+strconv.FormatUint(uint64(first.SessionID), 10) || first.ChannelID != 1 || first.Revision != 1 {
		t.Fatal("invalid session metadata")
	}
	state := gulState(t, h, first.SessionToken)
	if state.SelfSession != first.SessionID || state.SelfChannel != 1 || state.Revision != 1 || state.Tree.Name != "Gul LiveKit" || len(state.Tree.Children) != 3 {
		t.Fatal("invalid initial snapshot")
	}
	users := allGulUsers(state.Tree)
	if len(users) != 2 {
		t.Fatalf("roster length = %d", len(users))
	}
	for _, user := range users {
		if user.IsSelf != (user.Session == first.SessionID) || user.ChannelID != 1 || user.Key != "s:livekit:"+strconv.FormatUint(uint64(user.Session), 10) {
			t.Fatal("invalid logical roster user")
		}
	}
	checkGulGrant(t, first.Grant, first, "voice", now)
	screen := gulResponse[livekitapi.Grant](t, gulRequest(h, "POST", "/api/gul/screen", first.SessionToken, `{"channelId":1,"revision":1}`))
	checkGulGrant(t, screen, first, "screen", now)
	if screen.Identity == first.Grant.Identity || len(allGulUsers(gulState(t, h, first.SessionToken).Tree)) != 2 {
		t.Fatal("screen companion changed logical roster")
	}
}

func checkGulGrant(t *testing.T, grant livekitapi.Grant, login livekitapi.LoginResponse, role string, now time.Time) {
	t.Helper()
	id := strconv.FormatUint(uint64(login.SessionID), 10)
	if grant.URL != ServerURL || grant.Identity != role+"."+id || grant.OwnerIdentity != "voice."+id || grant.Room != "gul-channel-"+strconv.FormatUint(uint64(login.ChannelID), 10) || grant.ChannelID != login.ChannelID || grant.SessionID != login.SessionID || grant.Revision != login.Revision {
		t.Fatal("grant metadata mismatch")
	}
	parts := strings.Split(grant.Token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		t.Fatal("invalid JWT signature")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != grant.Identity || claims["name"] != login.Name || claims["iss"] != "test-key" || int64(claims["exp"].(float64)) != now.Add(5*time.Minute).Unix() {
		t.Fatal("invalid JWT ownership or lifetime")
	}
	video := claims["video"].(map[string]any)
	if video["room"] != grant.Room || video["roomJoin"] != true || video["canPublish"] != true || video["canSubscribe"] != true || video["canPublishData"] != (role == "voice") || video["canUpdateOwnMetadata"] != false {
		t.Fatal("invalid media grants")
	}
	for _, key := range []string{"roomAdmin", "roomCreate", "roomList", "roomRecord", "ingressAdmin"} {
		if _, ok := video[key]; ok {
			t.Fatal("unexpected administrative grant")
		}
	}
	sources := video["canPublishSources"].([]any)
	if role == "voice" {
		if len(sources) != 1 || sources[0] != "microphone" {
			t.Fatal("invalid voice sources")
		}
	} else if len(sources) != 2 || sources[0] != "screen_share" || sources[1] != "screen_share_audio" {
		t.Fatal("invalid screen sources")
	}
	attributes := claims["attributes"].(map[string]any)
	if attributes["ownerIdentity"] != grant.OwnerIdentity || attributes["role"] != role || attributes["sessionId"] != id || attributes["channelId"] != strconv.FormatUint(uint64(grant.ChannelID), 10) || attributes["revision"] != strconv.FormatUint(grant.Revision, 10) {
		t.Fatal("invalid server-authored attributes")
	}
}

func TestGulChannelGenerationAndStaleScreen(t *testing.T) {
	h := gulHandler(time.Now)
	first := gulLogin(t, h, "one")
	second := gulLogin(t, h, "two")
	for _, id := range []uint32{2, 0, 3, 1} {
		previous := first
		first = gulResponse[livekitapi.LoginResponse](t, gulRequest(h, "POST", "/api/gul/channel", first.SessionToken, fmt.Sprintf(`{"channelId":%d}`, id)))
		if first.ChannelID != id || first.Revision != previous.Revision+1 || first.SessionID != previous.SessionID || first.SessionToken != previous.SessionToken {
			t.Fatal("channel switch lost logical session")
		}
		stale := fmt.Sprintf(`{"channelId":%d,"revision":%d}`, previous.ChannelID, previous.Revision)
		if w := gulRequest(h, "POST", "/api/gul/screen", first.SessionToken, stale); w.Code != 409 {
			t.Fatalf("stale screen status = %d", w.Code)
		}
		current := fmt.Sprintf(`{"channelId":%d,"revision":%d}`, first.ChannelID, first.Revision)
		grant := gulResponse[livekitapi.Grant](t, gulRequest(h, "POST", "/api/gul/screen", first.SessionToken, current))
		if grant.Room != first.Grant.Room {
			t.Fatal("screen grant can access another channel")
		}
		other := gulState(t, h, second.SessionToken)
		if other.Revision != 1 || other.SelfChannel != 1 {
			t.Fatal("another user's switch changed own generation")
		}
		for _, user := range allGulUsers(other.Tree) {
			if user.Session == first.SessionID && user.ChannelID != id {
				t.Fatal("roster did not move user")
			}
		}
	}
	unchanged := gulResponse[livekitapi.LoginResponse](t, gulRequest(h, "POST", "/api/gul/channel", first.SessionToken, `{"channelId":1}`))
	if unchanged.Revision != first.Revision {
		t.Fatal("idempotent join changed generation")
	}
	if w := gulRequest(h, "POST", "/api/gul/channel", first.SessionToken, `{"channelId":99}`); w.Code != 400 {
		t.Fatal("unknown channel accepted")
	}
}

func TestGulAudioAndLogout(t *testing.T) {
	h := gulHandler(time.Now)
	a := gulLogin(t, h, "one")
	b := gulLogin(t, h, "two")
	got := gulResponse[livekitapi.AudioState](t, gulRequest(h, "POST", "/api/gul/audio", a.SessionToken, `{"muted":false,"deafened":true}`))
	if !got.Muted || !got.Deafened {
		t.Fatal("deafen did not imply mute")
	}
	for _, user := range allGulUsers(gulState(t, h, b.SessionToken).Tree) {
		if user.Session == a.SessionID && (!user.SelfMute || !user.SelfDeaf || user.IsSelf) {
			t.Fatal("audio state not shared")
		}
	}
	if gulState(t, h, a.SessionToken).Revision != 1 {
		t.Fatal("audio change invalidated screen generation")
	}
	got = gulResponse[livekitapi.AudioState](t, gulRequest(h, "POST", "/api/gul/audio", a.SessionToken, `{"muted":false,"deafened":false}`))
	if got.Muted || got.Deafened {
		t.Fatal("audio state did not clear")
	}
	if w := gulRequest(h, "POST", "/api/gul/logout", a.SessionToken, ""); w.Code != 204 {
		t.Fatalf("logout status = %d", w.Code)
	}
	if w := gulRequest(h, "GET", "/api/gul/state", a.SessionToken, ""); w.Code != 401 {
		t.Fatal("logged-out session still valid")
	}
	if len(allGulUsers(gulState(t, h, b.SessionToken).Tree)) != 1 {
		t.Fatal("logged-out user remained in roster")
	}
}

func TestGulLeaseRefreshAndExpiration(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := gulHandler(func() time.Time { return now })
	a := gulLogin(t, h, "one")
	b := gulLogin(t, h, "two")
	now = now.Add(50 * time.Second)
	_ = gulState(t, h, a.SessionToken)
	now = now.Add(11 * time.Second)
	if w := gulRequest(h, "GET", "/api/gul/state", b.SessionToken, ""); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	if len(allGulUsers(gulState(t, h, a.SessionToken).Tree)) != 1 {
		t.Fatal("expired user remained in roster")
	}
	now = now.Add(60 * time.Second)
	if w := gulRequest(h, "POST", "/api/gul/screen", a.SessionToken, `{"channelId":1,"revision":1}`); w.Code != 401 {
		t.Fatal("expired session minted screen token")
	}
}

func TestGulInvalidRequests(t *testing.T) {
	h := gulHandler(time.Now)
	login := gulLogin(t, h, "valid")
	for _, tt := range []struct {
		name, method, path, token, body string
		code                            int
	}{
		{"missing auth", "GET", "/api/gul/state", "", "", 401},
		{"bad auth", "GET", "/api/gul/state", "invalid", "", 401},
		{"wrong login method", "GET", "/api/gul/login", "", "", 405},
		{"wrong state method", "POST", "/api/gul/state", login.SessionToken, `{}`, 405},
		{"password", "POST", "/api/gul/login", "", `{"username":"test","password":"anything"}`, 400},
		{"blank nick", "POST", "/api/gul/login", "", `{"username":"  "}`, 400},
		{"control nick", "POST", "/api/gul/login", "", `{"username":"te\u0000st"}`, 400},
		{"long nick", "POST", "/api/gul/login", "", `{"username":"` + strings.Repeat("ю", 65) + `"}`, 400},
		{"unknown login field", "POST", "/api/gul/login", "", `{"username":"user","role":"admin"}`, 400},
		{"trailing json", "POST", "/api/gul/login", "", `{"username":"user"}{}`, 400},
		{"oversized", "POST", "/api/gul/login", "", strings.Repeat(" ", 4097) + `{}`, 400},
		{"malformed json", "POST", "/api/gul/channel", login.SessionToken, `{`, 400},
		{"negative channel", "POST", "/api/gul/channel", login.SessionToken, `{"channelId":-1}`, 400},
		{"unknown audio field", "POST", "/api/gul/audio", login.SessionToken, `{"muted":true,"other":true}`, 400},
		{"screen wrong channel", "POST", "/api/gul/screen", login.SessionToken, `{"channelId":2,"revision":1}`, 409},
		{"screen future rev", "POST", "/api/gul/screen", login.SessionToken, `{"channelId":1,"revision":2}`, 409},
		{"screen unknown field", "POST", "/api/gul/screen", login.SessionToken, `{"channelId":1,"revision":1,"identity":"voice.1"}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := gulRequest(h, tt.method, tt.path, tt.token, tt.body)
			if w.Code != tt.code {
				t.Fatalf("status = %d, want %d", w.Code, tt.code)
			}
		})
	}
}

func TestGulConcurrentSessions(t *testing.T) {
	h := gulHandler(time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			login := gulLogin(t, h, "concurrent")
			for j := 0; j < 10; j++ {
				_ = gulState(t, h, login.SessionToken)
				_ = gulResponse[livekitapi.AudioState](t, gulRequest(h, "POST", "/api/gul/audio", login.SessionToken, `{"muted":true,"deafened":false}`))
				_ = gulResponse[livekitapi.LoginResponse](t, gulRequest(h, "POST", "/api/gul/channel", login.SessionToken, fmt.Sprintf(`{"channelId":%d}`, j%4)))
			}
			if w := gulRequest(h, "POST", "/api/gul/logout", login.SessionToken, ""); w.Code != 204 {
				t.Errorf("logout status = %d", w.Code)
			}
		})
	}
	wg.Wait()
	last := gulLogin(t, h, "last")
	if len(allGulUsers(gulState(t, h, last.SessionToken).Tree)) != 1 {
		t.Fatal("concurrent session cleanup incomplete")
	}
}

func TestGulRoutesRegisteredByLabHandler(t *testing.T) {
	h := testServer(t)
	login := gulLogin(t, h, "integration")
	if gulState(t, h, login.SessionToken).SelfSession != login.SessionID {
		t.Fatal("new routes not exposed by lab handler")
	}
	if w := request(h, "POST", `{"identity":"old-lab"}`, nil); w.Code != 200 {
		t.Fatal("existing screen lab endpoint regressed")
	}
}
