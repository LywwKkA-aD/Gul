package livekit

import (
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

func (m *Manager) runConnection(r *connectionRun, username, password string) {
	// Register before login so every exit closes the transport pool. Later
	// logout defers run first, closing their final keep-alive connection too.
	defer r.broker.close()
	login, err := r.broker.login(r.ctx, username, password)
	if err != nil {
		m.setStatus(r, domain.StateDisconnected, safeError(err).Error())
		return
	}
	defer logout(r.broker, login.SessionToken)
	if !validLogin(r.address, login) {
		m.setStatus(r, domain.StateDisconnected, ErrBroker.Error())
		return
	}
	var media mediaConnection
	defer func() {
		if media != nil {
			media.close()
		}
	}()
	m.mu.Lock()
	if m.run == r {
		m.login = login
	}
	m.mu.Unlock()
	retries := 0
	connectedOnce := false
	for m.active(r) {
		reconnect := make(chan struct{}, 1)
		m.mu.Lock()
		if m.run != r {
			m.mu.Unlock()
			return
		}
		m.epoch++
		epoch := m.epoch
		m.media = nil
		m.voice.flush()
		m.mu.Unlock()
		state := domain.StateConnecting
		if connectedOnce {
			state = domain.StateReconnecting
		}
		m.setStatus(r, state, "")
		hooks := mediaHooks{
			packet:  func(p session.VoicePacket) { m.receive(r, epoch, p) },
			message: func(message session.RawMessage) { m.message(r, epoch, message) },
			reconnect: func() {
				m.mu.Lock()
				valid := m.run == r && m.epoch == epoch
				m.mu.Unlock()
				if !valid {
					return
				}
				select {
				case reconnect <- struct{}{}:
				default:
				}
			},
		}
		media, err = m.dial(r.ctx, login.Grant, hooks)
		if !m.active(r) {
			if media != nil {
				media.close()
				media = nil
			}
			return
		}
		if err == nil {
			m.mu.Lock()
			if m.run == r {
				m.media = media
			}
			m.mu.Unlock()
			err = m.applyAudio(r, media, login.SessionToken)
		}
		if err == nil {
			connectedOnce = true
			m.setStatus(r, domain.StateConnected, "")
			next, switchRoom, loopErr := m.connected(r, media, login, reconnect)
			if switchRoom {
				login = next
				retries = 0
			} else {
				retries++
				err = loopErr
			}
		} else {
			retries++
		}
		m.mu.Lock()
		if m.run == r {
			m.media = nil
			m.epoch++
			m.status.State = domain.StateReconnecting
			m.voice.flush()
		}
		m.mu.Unlock()
		if media != nil {
			media.close()
			media = nil
		}
		if !m.active(r) {
			return
		}
		if retries == 0 {
			continue
		}
		retryState := domain.StateConnecting
		if connectedOnce {
			retryState = domain.StateReconnecting
		}
		m.setStatus(r, retryState, safeError(err).Error())
		delay := time.Duration(min(retries, 5)) * time.Second
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(delay):
		}
		// Renew the lease before obtaining a new JWT. A refresh never reuses a
		// dying PeerConnection or lets its callbacks publish into the new epoch.
		if _, err = r.broker.state(r.ctx, login.SessionToken); err != nil {
			m.setStatus(r, domain.StateDisconnected, safeError(err).Error())
			return
		}
		login, err = r.broker.channel(r.ctx, login.SessionToken, login.ChannelID)
		if err != nil || !validLogin(r.address, login) {
			m.setStatus(r, domain.StateDisconnected, ErrBroker.Error())
			return
		}
		m.mu.Lock()
		if m.run == r {
			m.login = login
		}
		m.mu.Unlock()
	}
}

func validLogin(base string, login api.LoginResponse) bool {
	return login.SessionToken != "" && validGrantForBroker(base, login.Grant, false) && login.SessionID == login.Grant.SessionID && login.Identity == login.Grant.Identity && login.ChannelID == login.Grant.ChannelID && login.Revision == login.Grant.Revision
}

func (m *Manager) connected(r *connectionRun, media mediaConnection, login api.LoginResponse, reconnect <-chan struct{}) (api.LoginResponse, bool, error) {
	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	if err := m.poll(r, login); err != nil {
		return login, false, err
	}
	failures := 0
	for {
		select {
		case <-r.ctx.Done():
			return login, false, r.ctx.Err()
		case <-reconnect:
			return login, false, ErrMedia
		case <-r.wake:
			if err := m.applyAudio(r, media, login.SessionToken); err != nil {
				return login, false, err
			}
		case <-poll.C:
			if err := m.poll(r, login); err != nil {
				failures++
				if failures >= 3 {
					return login, false, err
				}
			} else {
				failures = 0
			}
		case request := <-r.commands:
			if request.channel == nil {
				m.mu.Lock()
				valid := request.epoch == m.epoch && request.chatChannel == login.ChannelID
				m.mu.Unlock()
				if !valid {
					request.reply <- ErrStaleSession
					continue
				}
				request.reply <- media.chat(request.text)
				continue
			}
			if *request.channel == login.ChannelID {
				request.reply <- nil
				continue
			}
			next, err := r.broker.channel(r.ctx, login.SessionToken, *request.channel)
			if err != nil {
				request.reply <- safeError(err)
				continue
			}
			if !validLogin(r.address, next) {
				request.reply <- ErrBroker
				return login, false, ErrBroker
			}
			m.mu.Lock()
			if m.run == r {
				m.login = next
				m.media = nil
				m.epoch++
				m.status.State = domain.StateReconnecting
				m.voice.flush()
			}
			m.mu.Unlock()
			m.setStatus(r, domain.StateReconnecting, "")
			request.reply <- nil
			return next, true, nil
		}
	}
}

func (m *Manager) poll(r *connectionRun, login api.LoginResponse) error {
	state, err := r.broker.state(r.ctx, login.SessionToken)
	if err != nil {
		return err
	}
	if state.SelfSession != login.SessionID || state.SelfChannel != login.ChannelID || state.Revision != login.Revision {
		return ErrStaleSession
	}
	m.tree(r, state)
	return nil
}

func (m *Manager) applyAudio(r *connectionRun, media mediaConnection, token string) error {
	for {
		m.mu.Lock()
		want, gen := m.desired, m.audioGeneration
		m.mu.Unlock()
		media.mute(want.Muted)
		got, err := r.broker.audio(r.ctx, token, want)
		if err != nil {
			return err
		}
		m.mu.Lock()
		if m.run != r {
			m.mu.Unlock()
			return ErrNotConnected
		}
		if gen == m.audioGeneration {
			m.audioAcknowledged = gen
			m.audioAck = got
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
	}
}
