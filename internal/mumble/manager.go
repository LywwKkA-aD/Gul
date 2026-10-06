package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/hysteria"
	"github.com/LywwKkA-aD/Gul/internal/identity"
)

// ErrNotConnected is returned by actions that need a live session.
var ErrNotConnected = errors.New("mumble: not connected")

const statsPollInterval = 5 * time.Second

// roundTripGrace is how long a fresh session has to prove that our packets
// reach the server and come back.
//
// A completed handshake proves nothing. Verified live on 2026-08-26: a user
// authenticated fifty times in a row over a link whose outgoing direction died
// the moment the TLS handshake finished. They heard everyone; nothing of
// theirs arrived; the server dropped them on its own ping deadline every
// twenty seconds and the loop started over. The only honest evidence a
// transport works is a packet of ours coming back, which is exactly what a
// ping reply is - gumble counts nothing else in tcpPacketsReceived, and murmur
// answers a ping only if it received one.
//
// gumble sends its first ping the moment the loop starts and repeats every 5s,
// so this leaves room for one lost ping before we give up on the transport.
const roundTripGrace = 12 * time.Second

type credentials struct {
	address string
	// key retains the caller's spelling for saved-server settings; address
	// carries the normalized endpoint shown in connection status.
	key      string
	kind     endpointKind
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
	// identitySeed is the master secret this user is known by. It never
	// leaves the machine: the derived certificate is presented inside Mumble TLS.
	identitySeed []byte
	// outerRoots is a seam: nil means the system trust store. A live test
	// supplies the CA for a local Hysteria server.
	outerRoots *tls.Config

	// Network timing seams keep the lifecycle deterministic in tests;
	// NewManager wires the production implementations.
	dialFn          func(DialConfig, sessionHooks) (*Session, error)
	backoffFn       func(int) time.Duration
	statsInterval   time.Duration
	sampleLatencyFn func(*gumble.Client) // seam for tests; nil means sampleLatency
	roundTripFn     func(*gumble.Client) bool
	roundTripGrace  time.Duration

	// Seams for the self audio writer, along with selfAudioBudget below. The
	// writer goroutine reads them only after a wake-up, so a test that sets
	// them before the first intent is ordered behind that channel send and
	// needs no lock; setting them once the writer is running would be a race.
	writeSelfAudioFn func(*gumble.Client, bool, bool)
	selfAudioWoke    func() // called after every wake-up

	// voice is the transport for raw Opus. It outlives sessions: the audio
	// pipeline holds its channels while connections come and go.
	voice *voiceIO

	// The self audio writer and its stop signal, both for the lifetime of the
	// Manager rather than of a session (selfaudio.go).
	selfAudioWake   chan struct{}
	selfAudioDone   chan struct{}
	selfAudioBudget *sendBudget
	// transports remembers which saved servers completed a Hysteria round trip.
	transports *transportChooser

	mu         sync.Mutex
	status     domain.ConnectionStatus
	client     *gumble.Client
	session    *Session
	stop       chan struct{}
	cancel     context.CancelFunc
	done       chan struct{}
	accept     chan struct{}
	pending    *tofuPending
	restore    uint32
	hasRestore bool

	// Desired self mute/deafen, remembered so a reconnect can re-publish it.
	// One goroutine writes it (selfaudio.go).
	selfMuted    bool
	selfDeafened bool
	hasSelfAudio bool
	// dirty and writing say an intent has not reached the socket; sent, sentAt
	// and awaiting say one has, and is still waiting for the room to echo it.
	// Together they are what SelfAudioSettled answers with, and the second half
	// is the one that matters: a written packet is not an acknowledged one.
	selfAudioDirty    bool
	selfAudioWriting  bool
	selfAudioSent     selfAudioPair
	selfAudioSentAt   time.Time
	selfAudioAwaiting bool
	// retried marks that this intent has already been sent a second time after
	// going unanswered. One retry, then the room wins.
	selfAudioRetried bool

	closed bool
}

// NewManager loads the TOFU store and the client certificate (generating it
// on first run) from cfgDir and returns a ready-to-use Manager.
func NewManager(cfgDir string, log *slog.Logger, cb Callbacks) (*Manager, error) {
	tofu := NewTOFUStore(cfgDir, log)
	seed, err := identity.Load(cfgDir, log)
	if err != nil {
		return nil, err
	}
	cert, err := ClientCertificate(cfgDir, log)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		log:             log,
		cb:              cb,
		tofu:            tofu,
		cert:            cert,
		identitySeed:    seed,
		backoffFn:       defaultBackoff,
		statsInterval:   statsPollInterval,
		accept:          make(chan struct{}, 1),
		status:          domain.ConnectionStatus{State: domain.StateDisconnected},
		voice:           newVoiceIO(log),
		selfAudioWake:   make(chan struct{}, 1),
		selfAudioDone:   make(chan struct{}),
		selfAudioBudget: newSendBudget(selfAudioBurst, selfAudioInterval),
		roundTripGrace:  roundTripGrace,
		transports:      newTransportChooser(),
	}
	m.roundTripFn = func(client *gumble.Client) bool {
		_, _, ok := client.TCPPing()
		return ok
	}
	m.writeSelfAudioFn = func(client *gumble.Client, muted, deafened bool) {
		client.Do(func() { writeSelfAudio(client, muted, deafened) })
	}
	go m.selfAudioLoop()
	m.dialFn = func(cfg DialConfig, hooks sessionHooks) (*Session, error) {
		return dial(cfg, m.tofu, hooks, m.log)
	}
	return m, nil
}

// Connect starts an asynchronous connection attempt, replacing any session or
// reconnect loop already in flight.
func (m *Manager) Connect(address, username, password string) {
	ep, err := parseEndpoint(address)
	if err != nil {
		m.emitStatus(domain.ConnectionStatus{
			State: domain.StateDisconnected, Error: err.Error(),
		})
		return
	}
	addr := ep.address
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
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.stop, m.done = stop, done
	m.mu.Unlock()

	go func() {
		defer cancel()
		m.run(ctx, credentials{
			address:  addr,
			key:      address,
			kind:     ep.kind,
			username: username,
			password: password,
		}, stop, done)
	}()
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
	// Replace cannot fail the acceptance: a store that cannot be written
	// degrades to session-scoped pins (tofu.go) instead of refusing the trust
	// decision the user just made.
	m.tofu.Replace(pending.host, pending.prompt.NewFingerprint)
	m.log.Info("accepted new server fingerprint")

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
	alreadyClosed := m.closed
	m.closed = true
	m.pending = nil
	m.mu.Unlock()

	if !alreadyClosed {
		close(m.selfAudioDone)
	}
	m.voice.close()
}

// logVitals writes one line describing what the connection has actually
// carried (vitals.go). It runs on the session ticker, so a session that fails
// leaves a trail rather than a single "connection lost".
//
// Debug, and the client logs at debug: this is written to be read later, out
// of a diagnostics archive, by someone asking which of three things happened -
// a write of ours that never returned, a write that returned while nothing
// crossed the network, or no write attempted at all. Nothing here can name the
// server or the user; the tallies are packet types and the counters are bytes.
func (m *Manager) logVitals(session *Session, transport Transport) {
	vitals, ok := session.vitals()
	if !ok {
		return
	}
	// The session knows the address it dialled, which is what the redaction
	// needs; address here is the caller's key for the road memory, not a
	// spelling to redact against.
	// The voice counters ride the same line. They are the boundary between
	// this layer and the audio engine, and reading them beside the socket
	// counters is what separates "my voice stopped going out" from "my
	// connection stopped" - two reports that looked identical for the whole
	// of the last incident, because nothing read these.
	m.log.Debug("session vitals",
		"transport", string(transport),
		"vitals", vitals.redact(session.addr),
		"voice", m.voice.stats())
}

// requestSelfStats samples the round-trip time the client measured itself and
// publishes it.
//
// It replaced a UserStats request to the server. UserStats.TCPPingAverage is
// the server's average over the whole session and never decays, so a single
// stall - a tunnel, a train, a lost minute of signal - left the number high
// for the rest of the session and told the user nothing about the link they
// have now. gumble times its own pings and keeps a sliding window; the newest
// sample is what a person means by "the ping".
func (m *Manager) sampleLatency(client *gumble.Client) {
	if client == nil || m.cb.OnLatency == nil {
		return
	}
	if activeClient := m.currentClient(); activeClient != client {
		return
	}
	last, _, ok := client.TCPPing()
	if !ok {
		return
	}
	pingMS := float64(last)
	if math.IsNaN(pingMS) || math.IsInf(pingMS, 0) || pingMS < 0 {
		return
	}
	m.cb.OnLatency(domain.ConnectionLatency{PingMS: pingMS})
}

func (m *Manager) dialOnce(
	ctx context.Context,
	c credentials,
	transport Transport,
	dropped chan<- *gumble.DisconnectEvent,
) (*Session, error) {
	hooks := sessionHooks{
		connect: func(e *gumble.ConnectEvent) {
			// Fires from handleServerSync, i.e. state is fully synced here and
			// dial has not returned yet.
			m.restoreChannel(e.Client)
			m.restoreSelfAudio(e.Client)
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
		// One listener per session; it feeds the manager-wide voice buffer.
		audio: m.voice.newListener(),
	}

	cert := m.cert
	return m.dialFn(DialConfig{
		Context:      ctx,
		Address:      c.address,
		Username:     c.username,
		Password:     c.password,
		Certificate:  &cert,
		IdentitySeed: m.identitySeed,
		OuterRoots:   m.outerRoots,
		Transport:    transport,
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

// PreferTransport restores a verified Hysteria hint for one server. Anything the
// chooser does not recognise is ignored; Hysteria remains the only transport.
func (m *Manager) PreferTransport(address, transport string) {
	ep, err := parseEndpoint(address)
	if err != nil {
		return
	}
	m.transports.prefer(ep.kind, address, Transport(transport))
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
	if e == nil {
		return
	}
	if e.Type == gumble.UserChangeStats {
		// Stats alone say nothing the tree needs, and the latency now comes
		// from the client's own ping (sampleLatency).
		return
	}

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
	stop, done, cancel := m.stop, m.done, m.cancel
	session := m.session
	m.stop, m.done = nil, nil
	m.cancel = nil
	m.mu.Unlock()

	if stop == nil {
		return
	}
	close(stop)
	if cancel != nil {
		cancel()
	}
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

	// An intent recorded while there was no session has been waiting for one.
	m.wakeSelfAudio()
	m.voice.bind(session.client, session.addr)
}

func (m *Manager) clearSession() {
	m.mu.Lock()
	m.session = nil
	m.client = nil
	// Nothing is in flight any more: the packet went down with the session, and
	// the next one's trees speak for themselves.
	m.selfAudioAwaiting = false
	m.mu.Unlock()

	m.voice.unbind()
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

// reasonUplinkStalled is what the user is told when the connection is only
// broken one way. It says what to do about it, because the state itself is
// invisible from inside the window: everyone is still audible, and the only
// symptom is that nobody answers.
const reasonUplinkStalled = "исходящий трафик не проходит — вас не слышно, хотя вы слышите остальных"

// reasonNoRoundTrip is the session that never carried a packet of ours back.
// It is not the same failure as a stalled uplink: there the connection was
// working and stopped, here it never worked at all, and the difference is what
// tells a blocked network apart from one that merely broke.
const reasonNoRoundTrip = "сервер не отвечает на наши пакеты — этот способ подключения не работает в вашей сети"

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
	if errors.Is(err, hysteria.ErrAuthentication) || errors.Is(err, hysteria.ErrPasswordRequired) {
		return true
	}
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
