package broker

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
	"github.com/LywwKkA-aD/Gul/server/internal/catalog"
)

func gulCode(w http.ResponseWriter, status int, code string) {
	gulWrite(w, status, map[string]string{"code": code})
}
func (b *gulBroker) info(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodGet) {
		return
	}
	var id *string
	if b.store != nil {
		value := b.store.Snapshot().ServerID
		id = &value
	}
	gulWrite(w, 200, struct {
		ProtocolVersion      int     `json:"protocolVersion"`
		ServerID             *string `json:"serverId"`
		ChannelManagement    bool    `json:"channelManagement"`
		MemberAuthentication bool    `json:"memberAuthentication"`
		MaxChannels          int     `json:"maxChannels"`
	}{2, id, b.store != nil, b.store != nil, catalog.MaxChannels})
}
func (b *gulBroker) loginMemberLocked(input api.LoginRequest, name string) (string, uint64, int) {
	if b.store == nil {
		return "", 0, 0
	}
	if !b.store.Healthy() {
		return "", 0, 503
	}
	if input.ProtocolVersion != 2 {
		return "", 0, 426
	}
	if input.MemberCredential == "" {
		return "", 0, 0
	}
	member, ok := b.store.Snapshot().Authenticate(input.MemberCredential)
	if !ok {
		return "", 0, 401
	}
	if member.Name != name {
		if b.updateCatalogLocked(func(s *catalog.State) error {
			for i := range s.Members {
				if s.Members[i].ID == member.ID {
					s.Members[i].Name = name
				}
			}
			return nil
		}) != nil {
			return "", 0, 503
		}
	}
	return member.ID, member.AuthVersion, 0
}
func (b *gulBroker) memberInfoLocked(state catalog.State, session *gulSession) *api.MemberInfo {
	if member, ok := state.Member(session.MemberID); ok {
		return &api.MemberInfo{ID: &member.ID, Role: member.Role}
	}
	return &api.MemberInfo{Role: "guest"}
}
func (b *gulBroker) sessionAccessLocked(session *gulSession) bool {
	if b.closed || session.Moving {
		return false
	}
	if b.store == nil {
		return true
	}
	if !b.store.Healthy() {
		return false
	}
	s := b.store.Snapshot()
	if session.MemberID != "" {
		member, ok := s.Member(session.MemberID)
		if !ok || member.Revoked || member.AuthVersion != session.AuthVersion {
			return false
		}
	}
	return !b.closing[session.ChannelID] && s.CanJoin(session.MemberID, session.ChannelID)
}
func (b *gulBroker) destinationLocked(session *gulSession, id uint32) bool {
	if b.closed {
		return false
	}
	if b.store == nil {
		return true
	}
	if !b.store.Healthy() {
		return false
	}
	state := b.store.Snapshot()
	if session.MemberID != "" {
		member, ok := state.Member(session.MemberID)
		if !ok || member.Revoked || member.AuthVersion != session.AuthVersion {
			return false
		}
	}
	return !b.closing[id] && state.CanJoin(session.MemberID, id)
}
func (b *gulBroker) managedStateLocked(self *gulSession) api.State {
	s := b.store.Snapshot()
	nodes := make(map[uint32]*api.ChannelNode)
	for _, channel := range s.Channels {
		allowed := s.CanJoin(self.MemberID, channel.ID)
		version := channel.Version
		nodes[channel.ID] = &api.ChannelNode{ID: channel.ID, Name: channel.Name, Position: channel.Position, Users: []api.UserInfo{}, Children: []api.ChannelNode{}, Version: &version, Access: channel.Access, CanJoin: &allowed}
	}
	for _, session := range b.sessions {
		node := nodes[session.ChannelID]
		if session.Revoked || session.Moving || node == nil || !*node.CanJoin {
			continue
		}
		node.Users = append(node.Users, api.UserInfo{Session: session.ID, Key: "s:livekit:" + strconv.FormatUint(uint64(session.ID), 10), Name: session.Name, ChannelID: session.ChannelID, SelfMute: session.Audio.Muted, SelfDeaf: session.Audio.Deafened, IsSelf: session == self})
	}
	root := nodes[0]
	for id, node := range nodes {
		slices.SortFunc(node.Users, func(a, b api.UserInfo) int {
			if a.Session < b.Session {
				return -1
			}
			if a.Session > b.Session {
				return 1
			}
			return 0
		})
		if id != 0 {
			root.Children = append(root.Children, *node)
		}
	}
	slices.SortFunc(root.Children, func(a, b api.ChannelNode) int {
		if a.Position != b.Position {
			return int(a.Position) - int(b.Position)
		}
		return int(a.ID) - int(b.ID)
	})
	return api.State{Tree: *root, SelfSession: self.ID, SelfChannel: self.ChannelID, Revision: self.Revision, ServerID: s.ServerID, Member: b.memberInfoLocked(s, self), CatalogVersion: s.CatalogVersion}
}

// Owner authorization is always resolved from the persisted identity, never a nickname.
func (b *gulBroker) withOwner(w http.ResponseWriter, r *http.Request, fn func(*gulSession) (int, any)) {
	b.withSession(w, r, func(session *gulSession, _ string, _ time.Time) (int, any) {
		member, ok := b.store.Snapshot().Member(session.MemberID)
		if !ok || member.Role != "owner" {
			return 403, map[string]string{"code": "owner_required"}
		}
		return fn(session)
	})
}

func (b *gulBroker) requireOwner(w http.ResponseWriter, r *http.Request) *gulSession {
	token, prefix := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	key, valid := tokenKey(token)
	b.mu.Lock()
	b.expireLocked(b.now())
	s := b.sessions[key]
	if !prefix || !valid || s == nil || s.Revoked || !b.sessionAccessLocked(s) {
		b.mu.Unlock()
		gulCode(w, 401, "session_unavailable")
		return nil
	}
	member, ok := b.store.Snapshot().Member(s.MemberID)
	if !ok || member.Role != "owner" {
		b.mu.Unlock()
		gulCode(w, 403, "owner_required")
		return nil
	}
	b.mu.Unlock()
	return s
}

func (b *gulBroker) ownerLiveLocked(owner *gulSession) bool {
	if owner == nil || owner.Revoked || !b.now().Before(owner.ExpiresAt) || !b.sessionAccessLocked(owner) {
		return false
	}
	found := false
	for _, session := range b.sessions {
		if session == owner {
			found = true
			break
		}
	}
	member, ok := b.store.Snapshot().Member(owner.MemberID)
	return found && ok && member.Role == "owner"
}

func (b *gulBroker) updateCatalogLocked(change func(*catalog.State) error) error {
	err := b.store.Update(change)
	if !b.store.Healthy() {
		for _, session := range b.sessions {
			session.Revoked = true
			b.cancelFlowsLocked(session)
		}
	}
	return err
}
