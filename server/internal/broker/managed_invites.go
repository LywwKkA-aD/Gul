package broker

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
	"github.com/LywwKkA-aD/Gul/server/internal/catalog"
)

func (b *gulBroker) createInvite(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) || b.requireOwner(w, r) == nil {
		return
	}
	var input struct{}
	if !gulJSON(w, r, &input) {
		return
	}
	b.withOwner(w, r, func(_ *gulSession) (int, any) {
		token := catalog.RandomCredential()
		expires := b.now().Add(24 * time.Hour).Unix()
		err := b.updateCatalogLocked(func(s *catalog.State) error {
			s.Invites = slices.DeleteFunc(s.Invites, func(invite catalog.Invite) bool { return invite.ExpiresAt <= b.now().Unix() })
			if len(s.Invites) >= catalog.MaxInvites {
				return errInvalid
			}
			s.Invites = append(s.Invites, catalog.Invite{Digest: catalog.Digest("invite", s.ServerID, token), ExpiresAt: expires})
			return nil
		})
		if err != nil {
			return operationError(err)
		}
		return 200, struct {
			InviteToken string `json:"inviteToken"`
			ExpiresAt   int64  `json:"expiresAtUnixSeconds"`
		}{token, expires}
	})
}
func (b *gulBroker) redeemInvite(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	var input struct {
		ProtocolVersion  int    `json:"protocolVersion"`
		Username         string `json:"username"`
		Password         string `json:"password"`
		InviteToken      string `json:"inviteToken"`
		MemberCredential string `json:"memberCredential"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	if input.ProtocolVersion != 2 {
		gulCode(w, 426, "upgrade_required")
		return
	}
	hash := sha256.Sum256([]byte(input.Password))
	if len(input.Password) < 16 || len(input.Password) > 256 || subtle.ConstantTimeCompare(hash[:], b.passwordHash[:]) != 1 {
		gulCode(w, 401, "authentication_failed")
		return
	}
	name := strings.TrimSpace(input.Username)
	if !catalog.ValidName(name) || !catalog.ValidCredential(input.InviteToken) || !catalog.ValidCredential(input.MemberCredential) {
		gulCode(w, 400, "invalid_request")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		gulCode(w, 503, "storage_unavailable")
		return
	}
	var member catalog.Member
	existing := b.store.Snapshot()
	digest := catalog.Digest("invite", existing.ServerID, input.InviteToken)
	err := b.updateCatalogLocked(func(s *catalog.State) error {
		for i, invite := range s.Invites {
			if subtle.ConstantTimeCompare([]byte(invite.Digest), []byte(digest)) != 1 {
				continue
			}
			if invite.ExpiresAt <= b.now().Unix() {
				return errAccess
			}
			if invite.ConsumedBy != "" {
				known, ok := s.Authenticate(input.MemberCredential)
				if !ok || known.ID != invite.ConsumedBy {
					return errAccess
				}
				member = known
				return nil
			}
			if len(s.Members) >= catalog.MaxMembers {
				return errInvalid
			}
			if _, ok := s.Authenticate(input.MemberCredential); ok {
				return errAccess
			}
			member = catalog.Member{ID: catalog.RandomID(), Name: name, Role: "member", CredentialHash: catalog.Digest("member", s.ServerID, input.MemberCredential), AuthVersion: 1}
			s.Members = append(s.Members, member)
			s.Invites[i].ConsumedBy = member.ID
			s.CatalogVersion++
			return nil
		}
		return errAccess
	})
	if err != nil {
		status, response := operationError(err)
		gulWrite(w, status, response)
		return
	}
	gulWrite(w, 200, struct {
		ServerID string         `json:"serverId"`
		Member   api.MemberInfo `json:"member"`
	}{existing.ServerID, api.MemberInfo{ID: &member.ID, Role: "member"}})
}
