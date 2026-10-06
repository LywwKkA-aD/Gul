package livekit

import (
	"context"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

func (m *Manager) ScreenGrant(ctx context.Context, epoch uint64, channelID uint32) (domain.ScreenGrant, error) {
	m.mu.Lock()
	r := m.run
	login := m.login
	valid := r != nil && m.status.State == domain.StateConnected && m.epoch == epoch && m.status.Epoch == epoch && login.ChannelID == channelID
	m.mu.Unlock()
	if !valid {
		return domain.ScreenGrant{}, ErrStaleSession
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	grant, err := r.broker.screen(ctx, login.SessionToken, api.ScreenRequest{ChannelID: channelID, Revision: login.Revision})
	m.mu.Lock()
	valid = m.run == r && m.status.State == domain.StateConnected && m.epoch == epoch && m.status.Epoch == epoch && m.login.ChannelID == channelID && m.login.Revision == login.Revision
	m.mu.Unlock()
	if !valid {
		return domain.ScreenGrant{}, ErrStaleSession
	}
	if err != nil {
		return domain.ScreenGrant{}, safeError(err)
	}
	if !validGrantForBroker(r.address, grant, true) || grant.Revision != login.Revision || grant.SessionID != login.SessionID || grant.ChannelID != channelID {
		return domain.ScreenGrant{}, ErrBroker
	}
	grant.URL, _ = mediaAddress(grant.URL)
	result := domain.ScreenGrant{URL: grant.URL, Token: grant.Token, Identity: grant.Identity, Room: grant.Room, OwnerIdentity: grant.OwnerIdentity, ChannelID: channelID, Epoch: epoch}
	if r.gateway != nil {
		result.URL, err = r.gateway.SignalURL(epoch, grant.Token)
		if err != nil {
			return domain.ScreenGrant{}, ErrStaleSession
		}
		result.Transport, result.RelayOnly = "reality", true
	}
	m.mu.Lock()
	valid = m.run == r && m.epoch == epoch && m.status.State == domain.StateConnected && r.ctx.Err() == nil
	m.mu.Unlock()
	if !valid {
		return domain.ScreenGrant{}, ErrStaleSession
	}
	return result, nil
}

// AllowScreenOrigin authorizes one exact loopback browser companion origin for
// this media epoch; ordinary HTTPS connections need no loopback gateway.
func (m *Manager) AllowScreenOrigin(epoch uint64, origin string) error {
	m.mu.Lock()
	r := m.run
	valid := r != nil && m.status.State == domain.StateConnected && m.epoch == epoch && r.ctx.Err() == nil
	m.mu.Unlock()
	if !valid {
		return ErrStaleSession
	}
	if r.gateway != nil {
		if err := r.gateway.AllowOrigin(epoch, origin); err != nil {
			return ErrStaleSession
		}
	}
	return nil
}
