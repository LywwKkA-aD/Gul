package broker

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
	"github.com/LywwKkA-aD/Gul/server/internal/catalog"
)

type managedRemover struct {
	mu           sync.Mutex
	removed      []string
	participants []string
	fail         bool
}

func (m *managedRemover) RemoveParticipant(context.Context, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return context.DeadlineExceeded
	}
	m.removed = append(m.removed, "removed")
	return nil
}
func (m *managedRemover) ListParticipants(context.Context, string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return nil, context.DeadlineExceeded
	}
	return append([]string{}, m.participants...), nil
}

func managedFixture(t *testing.T) (*PublicHandler, catalog.OwnerKey, *managedRemover) {
	return managedFixtureAt(t, "")
}
func managedFixtureAt(t *testing.T, upstream string) (*PublicHandler, catalog.OwnerKey, *managedRemover) {
	t.Helper()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	cfg := publicTestConfig()
	if upstream != "" {
		cfg.LiveKitInternalURL = upstream
	}
	cfg.StatePath = filepath.Join(dir, "catalog.json")
	key, err := catalog.Bootstrap(cfg.StatePath)
	if err != nil {
		t.Fatal("bootstrap failed")
	}
	m := &managedRemover{}
	h, err := NewPublicHandler(cfg, m)
	if err != nil {
		t.Fatal("managed broker unavailable")
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, key, m
}
func managedDo(t *testing.T, h *PublicHandler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
	r.RemoteAddr = "127.0.0.1:12000"
	r.Host = "voice.example.test"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", "192.0.2.4")
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func managedLogin(t *testing.T, h *PublicHandler, credential string) api.LoginResponse {
	t.Helper()
	input := map[string]any{"username": "same nickname", "password": publicTestPassword, "protocolVersion": 2}
	if credential != "" {
		input["memberCredential"] = credential
	}
	w := managedDo(t, h, "POST", "/api/gul/login", "", input)
	var result api.LoginResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("managed login failed")
	}
	return result
}

func TestManagedExplicitInvalidMemberKeyNeverDowngradesToGuest(t *testing.T) {
	h, _, _ := managedFixture(t)
	for _, credential := range []any{"", nil, false, 12, "malformed"} {
		w := managedDo(t, h, "POST", "/api/gul/login", "", map[string]any{"username": "member", "password": publicTestPassword, "protocolVersion": 2, "memberCredential": credential})
		if w.Code != 400 && w.Code != 401 {
			t.Fatal("explicit invalid key became anonymous guest")
		}
	}
	if len(h.broker.sessions) != 0 {
		t.Fatal("failed key login created a session")
	}
	guest := managedLogin(t, h, "")
	if guest.Member.Role != "guest" {
		t.Fatal("omitted key did not allow guest")
	}
}
func managedState(t *testing.T, w *httptest.ResponseRecorder) api.State {
	t.Helper()
	var s api.State
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		t.Fatalf("managed operation status %d", w.Code)
	}
	return s
}

func TestManagedOwnerIdentityIsExplicitAndGuestsCannotAdminister(t *testing.T) {
	h, key, _ := managedFixture(t)
	w := managedDo(t, h, "POST", "/api/gul/login", "", map[string]any{"username": "Владелец", "password": publicTestPassword})
	if w.Code != 426 {
		t.Fatal("old managed login was not rejected")
	}
	guest := managedLogin(t, h, "")
	owner := managedLogin(t, h, key.Credential)
	if guest.Member.Role != "guest" || guest.Member.ID != nil || owner.Member.Role != "owner" || *owner.Member.ID != key.MemberID || owner.ServerID != key.ServerID {
		t.Fatal("persistent identity mismatch")
	}
	for _, path := range []string{"/api/gul/channels/create", "/api/gul/channels/update", "/api/gul/channels/delete", "/api/gul/channels/permissions", "/api/gul/invites/create"} {
		if managedDo(t, h, "POST", path, guest.SessionToken, map[string]any{}).Code != 403 {
			t.Fatal("guest administrator escalation")
		}
	}
	if managedDo(t, h, "GET", "/api/gul/members", guest.SessionToken, nil).Code != 403 {
		t.Fatal("member list leaked to guest")
	}
	w = managedDo(t, h, "POST", "/api/gul/login", "", map[string]any{"username": "Владелец", "password": publicTestPassword, "protocolVersion": 2, "memberCredential": catalog.RandomCredential()})
	if w.Code != 401 {
		t.Fatal("invalid credential downgraded to guest")
	}
}

func TestManagedInviteAtomicIdentityAndRestrictedAdmission(t *testing.T) {
	h, key, _ := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	guest := managedLogin(t, h, "")
	w := managedDo(t, h, "POST", "/api/gul/invites/create", owner.SessionToken, map[string]any{})
	var invitation struct {
		InviteToken string `json:"inviteToken"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &invitation) != nil {
		t.Fatal("invite failed")
	}
	credential := catalog.RandomCredential()
	body := map[string]any{"protocolVersion": 2, "username": "member", "password": publicTestPassword, "inviteToken": invitation.InviteToken, "memberCredential": credential}
	w = managedDo(t, h, "POST", "/api/gul/invites/redeem", "", body)
	if w.Code != 200 || strings.Contains(w.Body.String(), credential) {
		t.Fatal("redemption failed or exposed personal key")
	}
	if managedDo(t, h, "POST", "/api/gul/invites/redeem", "", body).Code != 200 {
		t.Fatal("lost-response retry not idempotent")
	}
	body["memberCredential"] = catalog.RandomCredential()
	if managedDo(t, h, "POST", "/api/gul/invites/redeem", "", body).Code != 403 {
		t.Fatal("invite reused by other key")
	}
	member := managedLogin(t, h, credential)
	state := managedState(t, managedDo(t, h, "GET", "/api/gul/state", owner.SessionToken, nil))
	state = managedState(t, managedDo(t, h, "POST", "/api/gul/channels/create", owner.SessionToken, map[string]any{"name": "Private", "access": "restricted", "allowedMemberIds": []string{*member.Member.ID}, "catalogVersion": state.CatalogVersion}))
	channel := state.Tree.Children[len(state.Tree.Children)-1]
	if managedDo(t, h, "POST", "/api/gul/channel", guest.SessionToken, map[string]any{"channelId": channel.ID}).Code != 403 {
		t.Fatal("guest joined restricted channel")
	}
	if managedDo(t, h, "POST", "/api/gul/channel", member.SessionToken, map[string]any{"channelId": channel.ID}).Code != 200 {
		t.Fatal("allowed member denied")
	}
	guestState := managedState(t, managedDo(t, h, "GET", "/api/gul/state", guest.SessionToken, nil))
	for _, c := range guestState.Tree.Children {
		if c.ID == channel.ID && (c.CanJoin == nil || *c.CanJoin || len(c.Users) != 0) {
			t.Fatal("forbidden channel leaked roster")
		}
	}
	w = managedDo(t, h, "POST", "/api/gul/channels/update", owner.SessionToken, map[string]any{"channelId": channel.ID, "version": *channel.Version, "name": "Private renamed", "access": "restricted", "allowedMemberIds": []string{}})
	if w.Code != 200 {
		t.Fatal("ACL update failed")
	}
	if managedDo(t, h, "GET", "/api/gul/state", member.SessionToken, nil).Code != 401 {
		t.Fatal("revoked ACL session survived")
	}
}

func TestManagedDeleteRequiresEmptySFUAndCASNeverReusesIDs(t *testing.T) {
	h, key, m := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	input := map[string]any{"name": "new", "access": "open", "allowedMemberIds": []string{}, "catalogVersion": uint64(1)}
	s := managedState(t, managedDo(t, h, "POST", "/api/gul/channels/create", owner.SessionToken, input))
	c := s.Tree.Children[len(s.Tree.Children)-1]
	if managedDo(t, h, "POST", "/api/gul/channels/create", owner.SessionToken, input).Code != 409 {
		t.Fatal("stale catalogue write overwrote state")
	}
	m.participants = []string{"orphan"}
	del := map[string]any{"channelId": c.ID, "version": *c.Version}
	if managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, del).Code != 409 {
		t.Fatal("nonempty SFU deleted")
	}
	m.participants = nil
	s = managedState(t, managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, del))
	input["catalogVersion"] = s.CatalogVersion
	s = managedState(t, managedDo(t, h, "POST", "/api/gul/channels/create", owner.SessionToken, input))
	if s.Tree.Children[len(s.Tree.Children)-1].ID <= c.ID {
		t.Fatal("deleted ID reused")
	}
	for _, id := range []uint32{0, 1} {
		if managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, map[string]any{"channelId": id, "version": 1}).Code != 403 {
			t.Fatal("protected channel deleted")
		}
	}
}

func TestManagedRevocationFailureRemainsDeniedAndReportsCleanupPending(t *testing.T) {
	h, key, m := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	guest := managedLogin(t, h, "")
	if managedDo(t, h, "POST", "/api/gul/channel", guest.SessionToken, map[string]any{"channelId": 2}).Code != 200 {
		t.Fatal("open channel move failed")
	}
	m.fail = true
	w := managedDo(t, h, "POST", "/api/gul/channels/update", owner.SessionToken, map[string]any{"channelId": 2, "version": 1, "name": "closed", "access": "restricted", "allowedMemberIds": []string{}})
	if w.Code != 503 || !strings.Contains(w.Body.String(), "media_cleanup_pending") {
		t.Fatal("cleanup failure was falsely reported complete")
	}
	if managedDo(t, h, "GET", "/api/gul/state", guest.SessionToken, nil).Code != 401 {
		t.Fatal("failed cleanup restored forbidden access")
	}
	m.fail = false
	h.cleanupExpired(context.Background())
	if managedDo(t, h, "POST", "/api/gul/channels/delete", owner.SessionToken, map[string]any{"channelId": 2, "version": 2}).Code != 200 {
		t.Fatal("cleanup retry did not release empty channel")
	}
}

func TestManagedCatalogSurvivesRestartWithoutRevivingOldSessions(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	cfg := publicTestConfig()
	cfg.StatePath = filepath.Join(dir, "catalog.json")
	key, err := catalog.Bootstrap(cfg.StatePath)
	if err != nil {
		t.Fatal("bootstrap failed")
	}
	h, err := NewPublicHandler(cfg, &managedRemover{})
	if err != nil {
		t.Fatal("broker failed")
	}
	owner := managedLogin(t, h, key.Credential)
	s := managedState(t, managedDo(t, h, "POST", "/api/gul/channels/create", owner.SessionToken, map[string]any{"catalogVersion": 1, "name": "persistent private", "access": "restricted", "allowedMemberIds": []string{}}))
	if h.Close() != nil {
		t.Fatal("close failed")
	}
	restart, err := NewPublicHandler(cfg, &managedRemover{})
	if err != nil {
		t.Fatal("restart failed")
	}
	defer restart.Close()
	login := managedLogin(t, restart, key.Credential)
	state := managedState(t, managedDo(t, restart, "GET", "/api/gul/state", login.SessionToken, nil))
	if state.ServerID != key.ServerID || state.CatalogVersion != s.CatalogVersion || len(state.Tree.Children) != len(s.Tree.Children) {
		t.Fatal("catalogue lost on restart")
	}
	if managedDo(t, restart, "GET", "/api/gul/state", owner.SessionToken, nil).Code != 401 {
		t.Fatal("old bearer survived restart")
	}
	restart.broker.mu.Lock()
	oldGrant := restart.broker.admitLocked(owner.Grant.Token) != nil
	restart.broker.mu.Unlock()
	if oldGrant {
		t.Fatal("old media nonce survived restart")
	}
}

func TestManagedUnhealthyStorageDeniesEveryAuthorityBoundary(t *testing.T) {
	h, key, _ := managedFixture(t)
	owner := managedLogin(t, h, key.Credential)
	h.broker.mu.Lock()
	_ = h.broker.store.Close()
	err := h.broker.updateCatalogLocked(func(s *catalog.State) error { s.CatalogVersion++; return nil })
	accepted := h.broker.admitLocked(owner.Grant.Token) != nil
	h.broker.mu.Unlock()
	if err == nil || accepted {
		t.Fatal("unhealthy store authorized media")
	}
	for _, path := range []string{"/api/gul/state", "/api/gul/members"} {
		if managedDo(t, h, "GET", path, owner.SessionToken, nil).Code != 401 {
			t.Fatal("unhealthy store authorized state/admin")
		}
	}
	if managedDo(t, h, "POST", "/api/gul/login", "", map[string]any{"username": "guest", "password": publicTestPassword, "protocolVersion": 2}).Code != 503 {
		t.Fatal("unhealthy store issued guest grant")
	}
	for _, path := range []string{"/api/gul/channel", "/api/gul/screen"} {
		body := map[string]any{"channelId": 1}
		if path == "/api/gul/screen" {
			body["revision"] = 1
		}
		if managedDo(t, h, "POST", path, owner.SessionToken, body).Code != 401 {
			t.Fatal("unhealthy store issued channel/screen grant")
		}
	}
}
