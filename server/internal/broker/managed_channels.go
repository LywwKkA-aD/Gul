package broker

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/catalog"
)

var errConflict = errors.New("conflict")
var errAccess = errors.New("access_denied")
var errInvalid = errors.New("invalid_request")

func operationError(err error) (int, any) {
	if err == nil {
		return 200, nil
	}
	code, status := "storage_unavailable", 503
	switch {
	case errors.Is(err, errConflict):
		code, status = "conflict", 409
	case errors.Is(err, errAccess):
		code, status = "access_denied", 403
	case errors.Is(err, errInvalid):
		code, status = "invalid_request", 400
	}
	return status, map[string]string{"code": code}
}
func (b *gulBroker) registerManagement(mux *http.ServeMux) {
	mux.HandleFunc("/api/gul/members", b.members)
	mux.HandleFunc("/api/gul/channels/create", b.createChannel)
	mux.HandleFunc("/api/gul/channels/update", b.updateChannel)
	mux.HandleFunc("/api/gul/channels/delete", b.deleteChannel)
	mux.HandleFunc("/api/gul/channels/permissions", b.channelPermissions)
	mux.HandleFunc("/api/gul/invites/create", b.createInvite)
	mux.HandleFunc("/api/gul/invites/redeem", b.redeemInvite)
}
func (b *gulBroker) members(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodGet) {
		return
	}
	b.withOwner(w, r, func(_ *gulSession) (int, any) {
		s := b.store.Snapshot()
		type entry struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Role    string `json:"role"`
			Revoked bool   `json:"revoked"`
		}
		members := make([]entry, 0, len(s.Members))
		for _, m := range s.Members {
			members = append(members, entry{m.ID, m.Name, m.Role, m.Revoked})
		}
		return 200, struct {
			Members        []entry `json:"members"`
			CatalogVersion uint64  `json:"catalogVersion"`
		}{members, s.CatalogVersion}
	})
}

type channelInput struct {
	ChannelID        *uint32  `json:"channelId"`
	Version          *uint64  `json:"version"`
	Name             string   `json:"name"`
	Access           string   `json:"access"`
	AllowedMemberIDs []string `json:"allowedMemberIds"`
	CatalogVersion   *uint64  `json:"catalogVersion"`
}

func validPermissions(s catalog.State, name, access string, allowed []string) bool {
	if !catalog.ValidName(name) || (access != "open" && access != "restricted") || allowed == nil || len(allowed) > catalog.MaxAllowedMembers || (access == "open" && len(allowed) != 0) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range allowed {
		member, ok := s.Member(id)
		if !ok || member.Revoked || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (b *gulBroker) createChannel(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) || b.requireOwner(w, r) == nil {
		return
	}
	var input struct {
		Name             string   `json:"name"`
		Access           string   `json:"access"`
		AllowedMemberIDs []string `json:"allowedMemberIds"`
		CatalogVersion   *uint64  `json:"catalogVersion"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	b.withOwner(w, r, func(owner *gulSession) (int, any) {
		err := b.updateCatalogLocked(func(s *catalog.State) error {
			if input.CatalogVersion == nil || *input.CatalogVersion != s.CatalogVersion {
				return errConflict
			}
			if !validPermissions(*s, input.Name, input.Access, input.AllowedMemberIDs) || len(s.Channels) >= catalog.MaxChannels || s.NextChannelID > 0x7fffffff {
				return errInvalid
			}
			position := int32(0)
			for _, c := range s.Channels {
				if c.Position >= position {
					position = c.Position + 1
				}
			}
			s.Channels = append(s.Channels, catalog.Channel{ID: s.NextChannelID, Name: input.Name, Position: position, Version: 1, Access: input.Access, AllowedMemberIDs: slices.Clone(input.AllowedMemberIDs)})
			s.NextChannelID++
			s.CatalogVersion++
			return nil
		})
		if err != nil {
			return operationError(err)
		}
		return 200, b.stateLocked(owner)
	})
}
func (b *gulBroker) channelPermissions(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) || b.requireOwner(w, r) == nil {
		return
	}
	var input struct {
		ChannelID *uint32 `json:"channelId"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	b.withOwner(w, r, func(_ *gulSession) (int, any) {
		if input.ChannelID == nil {
			return operationError(errInvalid)
		}
		c, ok := b.store.Snapshot().Channel(*input.ChannelID)
		if !ok {
			return operationError(errInvalid)
		}
		return 200, struct {
			ChannelID        uint32   `json:"channelId"`
			Version          uint64   `json:"version"`
			Access           string   `json:"access"`
			AllowedMemberIDs []string `json:"allowedMemberIds"`
		}{c.ID, c.Version, c.Access, c.AllowedMemberIDs}
	})
}
func (b *gulBroker) updateChannel(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	owner := b.requireOwner(w, r)
	if owner == nil {
		return
	}
	var input struct {
		ChannelID        *uint32  `json:"channelId"`
		Version          *uint64  `json:"version"`
		Name             string   `json:"name"`
		Access           string   `json:"access"`
		AllowedMemberIDs []string `json:"allowedMemberIds"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	var revoked []*gulSession
	success := false
	b.mu.Lock()
	status, response := func() (int, any) {
		if !b.ownerLiveLocked(owner) {
			return 401, map[string]string{"code": "session_unavailable"}
		}
		err := b.updateCatalogLocked(func(s *catalog.State) error {
			if input.ChannelID == nil || input.Version == nil || !validPermissions(*s, input.Name, input.Access, input.AllowedMemberIDs) {
				return errInvalid
			}
			if *input.ChannelID <= 1 && input.Access != "open" {
				return errAccess
			}
			for i, c := range s.Channels {
				if c.ID != *input.ChannelID {
					continue
				}
				if c.Version != *input.Version || b.closing[c.ID] {
					return errConflict
				}
				s.Channels[i] = catalog.Channel{ID: c.ID, Name: input.Name, Position: c.Position, Version: c.Version + 1, Access: input.Access, AllowedMemberIDs: slices.Clone(input.AllowedMemberIDs)}
				s.CatalogVersion++
				return nil
			}
			return errInvalid
		})
		if err != nil {
			return operationError(err)
		}
		state := b.store.Snapshot()
		for _, session := range b.sessions {
			if !session.Revoked && !state.CanJoin(session.MemberID, session.ChannelID) {
				session.Revoked = true
				b.cancelFlowsLocked(session)
				revoked = append(revoked, session)
			}
		}
		success = true
		return 200, b.stateLocked(owner)
	}()
	b.mu.Unlock()
	// Admission is revoked under the same lock as the committed ACL. Cleanup
	// runs outside it; maintenance retries unavailable SFU removals.
	if success && len(revoked) != 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		results := make(chan error, len(revoked))
		for _, session := range revoked {
			go func(session *gulSession) {
				session.opMu.Lock()
				defer session.opMu.Unlock()
				err := b.cleanSessionMedia(ctx, session)
				if err == nil {
					b.mu.Lock()
					for key, current := range b.sessions {
						if current == session && session.Revoked {
							delete(b.sessions, key)
						}
					}
					b.mu.Unlock()
				}
				results <- err
			}(session)
		}
		for range revoked {
			if <-results != nil {
				status = 503
				response = map[string]string{"code": "media_cleanup_pending"}
			}
		}
	}

	gulWrite(w, status, response)
}

// ParticipantInspector is mandatory for empty-only deletion. Missing health
// or unknown SFU state cannot be treated as an empty room.
type ParticipantInspector interface {
	ListParticipants(context.Context, string) ([]string, error)
}

func (b *gulBroker) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	owner := b.requireOwner(w, r)
	if owner == nil {
		return
	}
	var input struct {
		ChannelID *uint32 `json:"channelId"`
		Version   *uint64 `json:"version"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	if input.ChannelID == nil || input.Version == nil {
		gulCode(w, 400, "invalid_request")
		return
	}
	id := *input.ChannelID
	if id <= 1 {
		gulCode(w, 403, "access_denied")
		return
	}
	b.mu.Lock()
	channel, exists := b.store.Snapshot().Channel(id)
	busy := b.closing[id]
	for _, session := range b.sessions {
		if session.ChannelID == id {
			busy = true
		}
	}
	for flow := range b.flows {
		if flow.channelID == id {
			busy = true
		}
	}
	if !exists || channel.Version != *input.Version {
		b.mu.Unlock()
		gulCode(w, 409, "conflict")
		return
	}
	if busy {
		b.mu.Unlock()
		gulCode(w, 409, "channel_busy")
		return
	}
	b.closing[id] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.closing, id); b.mu.Unlock() }()
	inspector, ok := b.remover.(ParticipantInspector)
	if !ok {
		gulCode(w, 503, "media_cleanup_pending")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	participants, err := inspector.ListParticipants(ctx, "gul-channel-"+strconv.FormatUint(uint64(id), 10))
	if err != nil {
		gulCode(w, 503, "media_cleanup_pending")
		return
	}
	if len(participants) != 0 {
		gulCode(w, 409, "channel_busy")
		return
	}
	b.mu.Lock()
	if !b.ownerLiveLocked(owner) {
		b.mu.Unlock()
		gulCode(w, 401, "session_unavailable")
		return
	}
	err = b.updateCatalogLocked(func(s *catalog.State) error {
		for i, c := range s.Channels {
			if c.ID == id {
				if c.Version != *input.Version {
					return errConflict
				}
				s.Channels = append(s.Channels[:i:i], s.Channels[i+1:]...)
				s.CatalogVersion++
				return nil
			}
		}
		return errConflict
	})
	status, response := operationError(err)
	if err == nil {
		response = b.stateLocked(owner)
	}
	b.mu.Unlock()
	gulWrite(w, status, response)
}
