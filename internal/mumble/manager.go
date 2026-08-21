package mumble

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"gul/internal/domain"
)

// ErrNotConnected is returned by actions that need a live session.
var ErrNotConnected = errors.New("mumble: not connected")

type credentials struct {
	address  string
	username string
	password string
}

// tofuPending is the certificate change awaiting the user's decision.
type tofuPending struct {
	host   string
	prompt domain.TofuPrompt
}

// Manager owns the connection lifecycle: dialing, reconnect with backoff,
// channel restore, client certificate identity and TOFU decisions.
//
// Locking rules, in order of importance:
//   - gumble's Client is thread-unsafe. It is touched only from listener hooks
//     (which run on the read loop) or inside Client.Do.
//   - m.mu is never held while calling into gumble or while invoking a
//     callback. Listener hooks take m.mu only for short field reads, so the
//     read loop can never wait on a goroutine that is waiting on the read loop.
type Manager struct {
	log  *slog.Logger
	cb   Callbacks
	tofu *TOFUStore
	cert tls.Certificate

	// dialFn and backoffFn are seams for tests; NewManager wires the real ones.
	dialFn    func(DialConfig, sessionHooks) (*Session, error)
	backoffFn func(int) time.Duration

	mu         sync.Mutex
	status     domain.ConnectionStatus
	client     *gumble.Client
	session    *Session
	stop       chan struct{}
	done       chan struct{}
	accept     chan struct{}
	pending    *tofuPending
	restore    uint32
	hasRestore bool
	closed     bool
}

// NewManager loads the TOFU store and the client certificate (generating it
// on first run) from cfgDir and returns a ready-to-use Manager.
func NewManager(cfgDir string, log *slog.Logger, cb Callbacks) (*Manager, error) {
	tofu, err := NewTOFUStore(cfgDir)
	if err != nil {
		return nil, err
	}
	cert, err := ClientCertificate(cfgDir)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		log:       log,
		cb:        cb,
		tofu:      tofu,
		cert:      cert,
		backoffFn: backoffDelay,
		accept:    make(chan struct{}, 1),
		status:    domain.ConnectionStatus{State: domain.StateDisconnected},
	}
	m.dialFn = func(cfg DialConfig, hooks sessionHooks) (*Session, error) {
		return dial(cfg, m.tofu, hooks, m.log)
	}
	return m, nil
}

// Connect starts an asynchronous connection attempt, replacing any session or
// reconnect loop already in flight.
func (m *Manager) Connect(address, username, password string) {
	addr, _ := normalizeAddress(address)

	if strings.TrimSpace(address) == "" {
		m.emitStatus(domain.ConnectionStatus{
			State: domain.StateDisconnected, Error: "server address is required",
		})
		return
	}
	if strings.TrimSpace(username) == "" {
		m.emitStatus(domain.ConnectionStatus{
			State: domain.StateDisconnected, Server: addr, Error: "username is required",
		})
		return
	}

	m.stopRun()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	// Channel restore is scoped to one Connect: reconnects keep the channel,
	// an explicit new connection starts wherever the server puts us.
	m.restore, m.hasRestore = 0, false
	m.pending = nil
	stop := make(chan struct{})
	done := make(chan struct{})
	m.stop, m.done = stop, done
	m.mu.Unlock()

	go m.run(credentials{address: addr, username: username, password: password}, stop, done)
}

// Disconnect stops the session and any reconnect loop.
func (m *Manager) Disconnect() {
	m.stopRun()

	m.mu.Lock()
	server := m.status.Server
	m.restore, m.hasRestore = 0, false
	m.pending = nil
	m.mu.Unlock()

	m.emitStatus(domain.ConnectionStatus{State: domain.StateDisconnected, Server: server})
}

// Join moves self to the channel and remembers it for reconnect restore.
func (m *Manager) Join(channelID uint32) error {
	client := m.currentClient()
	if client == nil {
		return ErrNotConnected
	}

	var joinErr error
	client.Do(func() {
		channel := client.Channels[channelID]
		if channel == nil {
			joinErr = fmt.Errorf("join: channel %d not found", channelID)
			return
		}
		if client.Self == nil {
			joinErr = ErrNotConnected
			return
		}
		client.Self.Move(channel)
	})
	if joinErr != nil {
		return joinErr
	}

	m.mu.Lock()
	m.restore, m.hasRestore = channelID, true
	m.mu.Unlock()
	return nil
}

// SendMessage sends plain text to the channel. Mumble chat is HTML, so the
// text is escaped here; the receiving side sanitizes what the server hands back.
func (m *Manager) SendMessage(channelID uint32, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("send: empty message")
	}
	client := m.currentClient()
	if client == nil {
		return ErrNotConnected
	}

	escaped := html.EscapeString(text)
	var sendErr error
	client.Do(func() {
		channel := client.Channels[channelID]
		if channel == nil {
			sendErr = fmt.Errorf("send: channel %d not found", channelID)
			return
		}
		// recursive=false: the message goes to this channel only, not its tree.
		channel.Send(escaped, false)
	})
	return sendErr
}

// AcceptFingerprint confirms the pending TOFU mismatch and lets the waiting
// connect loop retry with the new pin. Rejecting is simply never calling it.
func (m *Manager) AcceptFingerprint() {
	m.mu.Lock()
	pending := m.pending
	m.pending = nil
	accept := m.accept
	m.mu.Unlock()

	if pending == nil {
		return
	}
	if err := m.tofu.Replace(pending.host, pending.prompt.NewFingerprint); err != nil {
		m.log.Error("pin accepted fingerprint", "host", pending.host, "error", err)
		return
	}
	m.log.Info("accepted new server fingerprint", "host", pending.host)

	select {
	case accept <- struct{}{}:
	default:
	}
}

// Status returns the current connection status snapshot.
func (m *Manager) Status() domain.ConnectionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Close stops everything. It waits for the connect loop to finish so no
// callback can fire after it returns.
func (m *Manager) Close() {
	m.stopRun()
	m.mu.Lock()
	m.closed = true
	m.pending = nil
	m.mu.Unlock()
}

// run is the connect/reconnect loop. Exactly one runs per Connect.
//
// First attempt failing is terminal: the user is looking at a connect form and
// needs the error, not a silent retry. Once a session has been established,
// every unexpected drop is retried with 1s, 2s, 4s ... capped at 30s, until
// Disconnect, Close, or a terminal condition (kick, ban, rejected credentials).
func (m *Manager) run(c credentials, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	attempt := 0
	reconnecting := false

	for {
		if isStopped(stop) {
			return
		}
		if !reconnecting {
			m.emitStatus(domain.ConnectionStatus{State: domain.StateConnecting, Server: c.address})
		}

		dropped := make(chan *gumble.DisconnectEvent, 1)
		session, err := m.dialOnce(c, dropped)
		if err != nil {
			var mismatch *MismatchError
			if errors.As(err, &mismatch) {
				if !m.awaitFingerprint(c.address, mismatch, stop) {
					return
				}
				// Accepted: retry immediately, the backoff is untouched.
				continue
			}

			m.log.Warn("connect attempt failed", "server", c.address, "error", err)
			if !reconnecting || isTerminalDialError(err) {
				m.emitStatus(domain.ConnectionStatus{
					State: domain.StateDisconnected, Server: c.address, Error: err.Error(),
				})
				return
			}
			m.emitStatus(domain.ConnectionStatus{State: domain.StateReconnecting, Server: c.address})
			if !sleepOrStop(m.backoffFn(attempt), stop) {
				return
			}
			attempt++
			continue
		}

		attempt = 0
		m.setSession(session)
		m.publishConnected(session, c.address)

		select {
		case <-stop:
			m.clearSession()
			_ = session.Disconnect()
			return

		case event := <-dropped:
			m.clearSession()
			reason, terminal := disconnectReason(event)
			if terminal {
				m.emitStatus(domain.ConnectionStatus{
					State: domain.StateDisconnected, Server: c.address, Error: reason,
				})
				return
			}
			m.log.Warn("connection lost", "server", c.address, "reason", reason)
			reconnecting = true
			m.emitStatus(domain.ConnectionStatus{State: domain.StateReconnecting, Server: c.address})
			if !sleepOrStop(m.backoffFn(attempt), stop) {
				return
			}
			attempt++
		}
	}
}

func (m *Manager) dialOnce(c credentials, dropped chan<- *gumble.DisconnectEvent) (*Session, error) {
	hooks := sessionHooks{
		connect: func(e *gumble.ConnectEvent) {
			// Fires from handleServerSync, i.e. state is fully synced here and
			// dial has not returned yet.
			m.restoreChannel(e.Client)
		},
		disconnect: func(e *gumble.DisconnectEvent) {
			// Buffered by one and never blocking: the read loop must not wait.
			select {
			case dropped <- e:
			default:
			}
		},
		channelChange: func(e *gumble.ChannelChangeEvent) { m.pushTree(e.Client) },
		userChange:    func(e *gumble.UserChangeEvent) { m.onUserChange(e, c.address) },
		textMessage:   m.onTextMessage,
		permissionDenied: func(e *gumble.PermissionDeniedEvent) {
			m.log.Warn("permission denied", "type", int(e.Type), "reason", e.String)
		},
	}

	cert := m.cert
	return m.dialFn(DialConfig{
		Address:     c.address,
		Username:    c.username,
		Password:    c.password,
		Certificate: &cert,
	}, hooks)
}

// publishConnected emits the connected status and the first tree snapshot.
// Both reads happen inside a single Client.Do so the pair is consistent.
func (m *Manager) publishConnected(session *Session, server string) {
	client := session.client
	status := domain.ConnectionStatus{State: domain.StateConnected, Server: server}
	var tree domain.ChannelNode
	haveTree := false

	doClient(client, func() {
		status = m.connectedStatus(client, server)
		tree, haveTree = treeOf(client)
	})

	m.emitStatus(status)
	if haveTree && m.cb.OnTree != nil {
		m.cb.OnTree(tree)
	}
}

// restoreChannel moves self back to the channel that was joined before the
// drop. Runs on the read loop inside the connect hook.
func (m *Manager) restoreChannel(client *gumble.Client) {
	m.mu.Lock()
	channelID, ok := m.restore, m.hasRestore
	m.mu.Unlock()

	if !ok {
		return
	}
	channel := channelToRestore(client, channelID)
	if channel == nil {
		return
	}
	m.log.Info("restoring channel after reconnect", "channel", channelID)
	client.Self.Move(channel)
}

// channelToRestore decides where self should be moved after a reconnect, or
// nil when there is nothing to do: already there, or the channel is gone and we
// stay where the server put us (the root).
//
// Split out from restoreChannel so the decision is testable without a live
// connection - User.Move writes straight to the wire.
func channelToRestore(client *gumble.Client, channelID uint32) *gumble.Channel {
	if client == nil || client.Self == nil {
		return nil
	}
	if client.Self.Channel != nil && client.Self.Channel.ID == channelID {
		return nil
	}
	return client.Channels[channelID]
}

func (m *Manager) onUserChange(e *gumble.UserChangeEvent, server string) {
	m.pushTree(e.Client)

	if e.User == nil || e.Client == nil || e.Client.Self == nil ||
		e.User.Session != e.Client.Self.Session || !e.Type.Has(gumble.UserChangeChannel) {
		return
	}

	// Self landed somewhere. Track where for real rather than trusting the last
	// Join: the move may have been denied, or an admin may have moved us.
	if e.User.Channel != nil {
		m.mu.Lock()
		m.restore, m.hasRestore = e.User.Channel.ID, true
		m.mu.Unlock()
	}

	// The status carries SelfChannel, so refresh it.
	m.emitStatus(m.connectedStatus(e.Client, server))
}

func (m *Manager) onTextMessage(e *gumble.TextMessageEvent) {
	if m.cb.OnMessage == nil {
		return
	}

	var channelID uint32
	switch {
	case len(e.Channels) > 0 && e.Channels[0] != nil:
		channelID = e.Channels[0].ID
	case e.Client != nil && e.Client.Self != nil && e.Client.Self.Channel != nil:
		// Direct messages carry no channel; attribute them to where we are.
		channelID = e.Client.Self.Channel.ID
	}

	sender, senderHash := "", ""
	if e.Sender != nil {
		sender, senderHash = e.Sender.Name, e.Sender.Hash
	}

	m.cb.OnMessage(RawMessage{
		ChannelID:  channelID,
		Sender:     sender,
		SenderHash: senderHash,
		HTML:       e.Message,
	})
}

// pushTree builds and delivers a tree snapshot. Must be called from a listener
// hook or inside Client.Do.
func (m *Manager) pushTree(client *gumble.Client) {
	if m.cb.OnTree == nil {
		return
	}
	if tree, ok := treeOf(client); ok {
		m.cb.OnTree(tree)
	}
}

// connectedStatus reads self identity off the client. Must be called from a
// listener hook or inside Client.Do.
func (m *Manager) connectedStatus(client *gumble.Client, server string) domain.ConnectionStatus {
	status := domain.ConnectionStatus{State: domain.StateConnected, Server: server}
	if client == nil || client.Self == nil {
		return status
	}
	status.SelfSession = client.Self.Session
	if client.Self.Channel != nil {
		status.SelfChannel = client.Self.Channel.ID
	}
	return status
}

// awaitFingerprint publishes the TOFU prompt and blocks until the user accepts
// it or the loop is stopped. Returns true when the dial should be retried.
func (m *Manager) awaitFingerprint(server string, mismatch *MismatchError, stop <-chan struct{}) bool {
	prompt := domain.TofuPrompt{
		Server:         server,
		OldFingerprint: mismatch.Pinned,
		NewFingerprint: mismatch.Presented,
	}

	m.mu.Lock()
	m.pending = &tofuPending{host: mismatch.Host, prompt: prompt}
	accept := m.accept
	// Drop a stale acceptance so it cannot auto-answer this prompt.
	select {
	case <-accept:
	default:
	}
	m.mu.Unlock()

	m.emitStatus(domain.ConnectionStatus{
		State: domain.StateDisconnected, Server: server, Error: mismatch.Error(),
	})
	if m.cb.OnTofu != nil {
		m.cb.OnTofu(prompt)
	}

	select {
	case <-accept:
		return true
	case <-stop:
		m.mu.Lock()
		m.pending = nil
		m.mu.Unlock()
		return false
	}
}

// stopRun tears down the running loop and waits for it to exit. No lock is held
// while waiting, so the loop is free to take m.mu on its way out.
func (m *Manager) stopRun() {
	m.mu.Lock()
	stop, done := m.stop, m.done
	session := m.session
	m.stop, m.done = nil, nil
	m.mu.Unlock()

	if stop == nil {
		return
	}
	close(stop)
	// Unblock a loop parked on the disconnect event.
	_ = session.Disconnect()
	if done != nil {
		<-done
	}
}

func (m *Manager) setSession(session *Session) {
	m.mu.Lock()
	m.session = session
	m.client = session.client
	m.mu.Unlock()
}

func (m *Manager) clearSession() {
	m.mu.Lock()
	m.session = nil
	m.client = nil
	m.mu.Unlock()
}

func (m *Manager) currentClient() *gumble.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

func (m *Manager) emitStatus(status domain.ConnectionStatus) {
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()

	if m.cb.OnStatus != nil {
		m.cb.OnStatus(status)
	}
}

// treeOf snapshots the client's channel tree. Must be called from a listener
// hook or inside Client.Do.
func treeOf(client *gumble.Client) (domain.ChannelNode, bool) {
	if client == nil {
		return domain.ChannelNode{}, false
	}
	root := client.Channels[0]
	if root == nil {
		return domain.ChannelNode{}, false
	}
	var selfSession uint32
	if client.Self != nil {
		selfSession = client.Self.Session
	}
	return snapshotTree(root, selfSession), true
}

// doClient runs f under Client.Do, tolerating a nil client so the loop stays
// testable without a live connection.
func doClient(client *gumble.Client, f func()) {
	if client == nil {
		f()
		return
	}
	client.Do(f)
}

func isStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func sleepOrStop(d time.Duration, stop <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	}
}

// disconnectReason classifies a drop: terminal means do not reconnect.
func disconnectReason(e *gumble.DisconnectEvent) (reason string, terminal bool) {
	if e == nil {
		return "connection lost", false
	}
	switch e.Type {
	case gumble.DisconnectUser:
		return "disconnected", true
	case gumble.DisconnectKicked:
		return joinReason("kicked", e.String), true
	case gumble.DisconnectBanned:
		return joinReason("banned", e.String), true
	default:
		return joinReason("connection lost", e.String), false
	}
}

func joinReason(prefix, detail string) string {
	if detail == "" {
		return prefix
	}
	return prefix + ": " + detail
}

// isTerminalDialError reports whether retrying can only fail the same way.
//
// DECISION: "username in use" and "server full" are treated as transient - the
// first is the common race where the server has not yet reaped our previous
// session after a drop, the second clears on its own.
func isTerminalDialError(err error) bool {
	var reject *gumble.RejectError
	if !errors.As(err, &reject) {
		return false
	}
	switch reject.Type {
	case gumble.RejectUsernameInUse, gumble.RejectServerFull:
		return false
	default:
		return true
	}
}
