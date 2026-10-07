package broker

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/twitchtv/twirp"
)

// ParticipantRemover removes active media connections, not issued JWTs.
// Self-hosted LiveKit may refresh JWTs while connected; an already issued
// token can be replayed until it expires. These friend rooms are not a tenant
// isolation boundary. The broker bearer is revoked immediately on logout.
type ParticipantRemover interface {
	RemoveParticipant(context.Context, string, string) error
}

type liveKitRemover struct{ client *lksdk.RoomServiceClient }

func newLiveKitRemover(cfg PublicConfig) ParticipantRemover {
	return &liveKitRemover{client: lksdk.NewRoomServiceClient(cfg.LiveKitInternalURL, cfg.APIKey, cfg.APISecret)}
}

func (r *liveKitRemover) RemoveParticipant(ctx context.Context, room, identity string) error {
	_, err := r.client.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{Room: room, Identity: identity})
	var rpcErr twirp.Error
	if errors.As(err, &rpcErr) && rpcErr.Code() == twirp.NotFound {
		return nil
	}
	return err
}

func (b *gulBroker) removeMedia(ctx context.Context, id, channelID uint32) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	room := "gul-channel-" + strconv.FormatUint(uint64(channelID), 10)
	voice := voiceIdentity(id)
	voiceErr := b.remover.RemoveParticipant(ctx, room, voice)
	screenErr := b.remover.RemoveParticipant(ctx, room, strings.Replace(voice, "voice.", "screen.", 1))
	return errors.Join(voiceErr, screenErr)
}

// Public transitions serialize only this session's mutations. The global
// mutex is never held across a RoomService request, so polling other users
// does not wait for the SFU. A failed move never issues a destination grant.
func (b *gulBroker) publicTransition(w http.ResponseWriter, r *http.Request, channelID *uint32) {
	token, prefix := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	key, valid := tokenKey(token)
	if !prefix || !valid {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	b.mu.Lock()
	b.expireLocked(b.now())
	session := b.sessions[key]
	active := session != nil && !session.Revoked
	b.mu.Unlock()
	if !active {
		http.Error(w, "session unavailable", http.StatusUnauthorized)
		return
	}
	session.opMu.Lock()
	defer session.opMu.Unlock()
	b.mu.Lock()
	b.expireLocked(b.now())
	if b.sessions[key] != session || session.Revoked {
		b.mu.Unlock()
		http.Error(w, "session unavailable", http.StatusUnauthorized)
		return
	}
	if channelID != nil && session.ChannelID == *channelID {
		response := b.responseLocked(session, token, b.now())
		b.mu.Unlock()
		gulWrite(w, http.StatusOK, response)
		return
	}
	id, oldChannel := session.ID, session.ChannelID
	if channelID == nil {
		session.Revoked = true
	}
	b.mu.Unlock()
	err := b.removeMedia(r.Context(), id, oldChannel)
	b.mu.Lock()
	if err != nil {
		b.mu.Unlock()
		http.Error(w, "media cleanup unavailable; retry", http.StatusServiceUnavailable)
		return
	}
	if channelID == nil {
		delete(b.sessions, key)
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b.expireLocked(b.now())
	if b.sessions[key] != session || session.Revoked {
		b.mu.Unlock()
		http.Error(w, "session unavailable", http.StatusUnauthorized)
		return
	}
	session.ChannelID, session.Revision = *channelID, session.Revision+1
	response := b.responseLocked(session, token, b.now())
	b.mu.Unlock()
	gulWrite(w, http.StatusOK, response)
}

// RunMaintenance removes expired media and retries failed logout cleanup.
// Revoked records count towards the session limit until removed, keeping the
// retry set bounded even while the SFU is unavailable.
func (h *PublicHandler) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.cleanupExpired(ctx)
		}
	}
}

func (h *PublicHandler) cleanupExpired(ctx context.Context) {
	b := h.broker
	b.mu.Lock()
	b.expireLocked(b.now())
	candidates := make(map[[32]byte]*gulSession)
	for key, session := range b.sessions {
		if session.Revoked {
			candidates[key] = session
		}
	}
	b.mu.Unlock()
	for key, session := range candidates {
		if ctx.Err() != nil {
			return
		}
		session.opMu.Lock()
		b.mu.Lock()
		current := b.sessions[key] == session && session.Revoked
		id, channel := session.ID, session.ChannelID
		b.mu.Unlock()
		if current && b.removeMedia(ctx, id, channel) == nil {
			b.mu.Lock()
			if b.sessions[key] == session && session.Revoked {
				delete(b.sessions, key)
			}
			b.mu.Unlock()
		}
		session.opMu.Unlock()
	}
}
