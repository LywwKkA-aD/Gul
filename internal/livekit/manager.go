// Package livekit adapts the local Gul broker and LiveKit SFU to native voice.
package livekit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

type connectionRun struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	commands chan command
	wake     chan struct{}
	broker   brokerAPI
	address  string
}
type command struct {
	channel     *uint32
	chatChannel uint32
	epoch       uint64
	text        string
	reply       chan error
}
type Manager struct {
	log               *slog.Logger
	cb                session.Callbacks
	mu                sync.Mutex
	notifyMu          sync.Mutex
	run               *connectionRun
	media             mediaConnection
	login             api.LoginResponse
	status            domain.ConnectionStatus
	epoch             uint64
	closed            bool
	runs              sync.WaitGroup
	desired           api.AudioState
	audioGeneration   uint64
	audioAcknowledged uint64
	audioAck          api.AudioState
	brokerFactory     func(string) brokerAPI
	dial              mediaDial
	voice             *voiceIO
}

var _ session.Controller = (*Manager)(nil)

func NewManager(log *slog.Logger, cb session.Callbacks) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{log: log, cb: cb, brokerFactory: func(base string) brokerAPI { return newBroker(base) }, dial: dialMedia, status: domain.ConnectionStatus{State: domain.StateDisconnected}}
	m.voice = newVoiceIO(m)
	return m
}

func (m *Manager) Connect(address, username, password string) {
	m.Disconnect()
	base, err := brokerAddress(address)
	if err != nil {
		m.detachedError(err)
		return
	}
	if strings.TrimSpace(username) == "" || utf8.RuneCountInString(username) > 64 {
		m.detachedError(ErrAuthentication)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &connectionRun{ctx: ctx, cancel: cancel, done: make(chan struct{}), commands: make(chan command, 16), wake: make(chan struct{}, 1), broker: m.brokerFactory(base), address: base}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		r.broker.close()
		return
	}
	if m.run != nil {
		m.run.cancel()
	}
	m.run = r
	m.runs.Add(1)
	m.mu.Unlock()
	m.setStatus(r, domain.StateConnecting, "")
	go func() {
		defer m.runs.Done()
		defer close(r.done)
		defer r.cancel()
		m.runConnection(r, strings.TrimSpace(username), password)
	}()
}

func (m *Manager) Disconnect() {
	m.notifyMu.Lock()
	m.mu.Lock()
	r := m.run
	m.run = nil
	m.media = nil
	m.login = api.LoginResponse{}
	m.epoch++
	status := domain.ConnectionStatus{State: domain.StateDisconnected, Epoch: m.epoch}
	m.status = status
	m.mu.Unlock()
	if r != nil {
		r.cancel()
	}
	m.voice.flush()
	if m.cb.OnStatus != nil {
		m.cb.OnStatus(status)
	}
	m.notifyMu.Unlock()
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.Disconnect()
	m.runs.Wait()
	m.voice.close()
}
func (m *Manager) Status() domain.ConnectionStatus { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) PreferTransport(string, string)  {}
func (m *Manager) AcceptFingerprint()              {}

func (m *Manager) Join(channelID uint32) error {
	if channelID > 3 {
		return ErrStaleSession
	}
	return m.command(command{channel: &channelID})
}
func (m *Manager) SendMessage(channelID uint32, text string) error {
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 5000 {
		return errors.New("LiveKit: сообщение пустое или слишком длинное")
	}
	m.mu.Lock()
	current := m.status.State == domain.StateConnected && m.login.ChannelID == channelID
	epoch := m.epoch
	m.mu.Unlock()
	if !current {
		return ErrNotConnected
	}
	return m.command(command{text: text, chatChannel: channelID, epoch: epoch})
}
func (m *Manager) command(c command) error {
	m.mu.Lock()
	r := m.run
	connected := m.status.State == domain.StateConnected
	m.mu.Unlock()
	if r == nil || !connected {
		return ErrNotConnected
	}
	c.reply = make(chan error, 1)
	select {
	case r.commands <- c:
	case <-r.ctx.Done():
		return ErrNotConnected
	}
	select {
	case err := <-c.reply:
		return err
	case <-r.ctx.Done():
		return ErrNotConnected
	}
}

func (m *Manager) SetSelfAudio(muted, deafened bool) {
	m.mu.Lock()
	m.desired = api.AudioState{Muted: muted || deafened, Deafened: deafened}
	m.audioGeneration++
	r := m.run
	m.mu.Unlock()
	m.voice.flushTX()
	if r != nil {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}
func (m *Manager) SelfAudioSettled(muted, deafened bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.audioGeneration == m.audioAcknowledged && m.audioAck == m.desired && m.audioAck == (api.AudioState{Muted: muted, Deafened: deafened})
}

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
	return domain.ScreenGrant{URL: grant.URL, Token: grant.Token, Identity: grant.Identity, Room: grant.Room, OwnerIdentity: grant.OwnerIdentity, ChannelID: channelID, Epoch: epoch}, nil
}

func (m *Manager) setStatus(r *connectionRun, state domain.ConnState, message string) bool {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	if m.run != r || r.ctx.Err() != nil {
		m.mu.Unlock()
		return false
	}
	status := domain.ConnectionStatus{State: state, Server: r.address, Error: message, SelfSession: m.login.SessionID, SelfChannel: m.login.ChannelID, Epoch: m.epoch}
	m.status = status
	m.mu.Unlock()
	if m.cb.OnStatus != nil {
		m.cb.OnStatus(status)
	}
	return true
}
func (m *Manager) detachedError(err error) {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	if m.closed || m.run != nil {
		m.mu.Unlock()
		return
	}
	status := domain.ConnectionStatus{State: domain.StateDisconnected, Error: safeError(err).Error(), Epoch: m.epoch}
	m.status = status
	m.mu.Unlock()
	if m.cb.OnStatus != nil {
		m.cb.OnStatus(status)
	}
}
func (m *Manager) tree(r *connectionRun, state api.State) {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	valid := m.run == r && r.ctx.Err() == nil && state.Revision == m.login.Revision && state.SelfChannel == m.login.ChannelID
	m.mu.Unlock()
	if valid && m.cb.OnTree != nil {
		m.cb.OnTree(state.Tree)
	}
}
func (m *Manager) message(r *connectionRun, epoch uint64, message session.RawMessage) {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	valid := m.run == r && m.epoch == epoch && m.status.State == domain.StateConnected
	m.mu.Unlock()
	if valid && m.cb.OnMessage != nil {
		m.cb.OnMessage(message)
	}
}
func (m *Manager) receive(r *connectionRun, epoch uint64, p session.VoicePacket) {
	m.mu.Lock()
	defer m.mu.Unlock()
	valid := m.run == r && m.epoch == epoch && m.status.State == domain.StateConnected
	if valid {
		m.voice.receive(p)
	}
}
func (m *Manager) active(r *connectionRun) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.run == r && r.ctx.Err() == nil
}
func safeError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidAddress):
		return ErrInvalidAddress
	case errors.Is(err, ErrAuthentication):
		return ErrAuthentication
	case errors.Is(err, ErrStaleSession):
		return ErrStaleSession
	case errors.Is(err, ErrNotConnected):
		return ErrNotConnected
	case errors.Is(err, ErrMedia):
		return ErrMedia
	default:
		return ErrBroker
	}
}

func logout(b brokerAPI, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.logout(ctx, token)
}
