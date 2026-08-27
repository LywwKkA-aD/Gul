package mumble

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/relayproto"
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
	// key identifies this server the way the CALLER spells it, which is not
	// how address spells it: Connect normalizes, so a saved "wss://host/mumble"
	// becomes "wss://host" here. Anything the caller has to match up with -
	// the road remembered for a server, which core stores beside its own list
	// keyed by its own string - has to use the caller's spelling, or the two
	// sides key the same server differently and the memory silently never
	// applies. address stays the normalized one: it is what the user is shown.
	key      string
	username string
	password string
	// bearer is the derived WSS relay credential. PBKDF2 makes derivation cost
	// about 50 ms, so it is computed once per Connect and reused by every
	// reconnect attempt. It is a secret: never log it, never put it in status.
	bearer relayproto.Credential
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

	// Network timing seams keep the lifecycle deterministic in tests;
	// NewManager wires the production implementations.
	dialFn          func(DialConfig, sessionHooks) (*Session, error)
	backoffFn       func(int) time.Duration
	deriveFn        func([]byte) relayproto.Credential
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
	// transports picks the road to the relay and remembers what worked
	// (transport.go).
	transports *transportChooser

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

	closed bool
}

// NewManager loads the TOFU store and the client certificate (generating it
// on first run) from cfgDir and returns a ready-to-use Manager.
func NewManager(cfgDir string, log *slog.Logger, cb Callbacks) (*Manager, error) {
	tofu := NewTOFUStore(cfgDir, log)
	cert, err := ClientCertificate(cfgDir, log)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		log:             log,
		cb:              cb,
		tofu:            tofu,
		cert:            cert,
		backoffFn:       defaultBackoff,
		deriveFn:        relayproto.Derive,
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
	m.stop, m.done = stop, done
	m.mu.Unlock()

	go m.run(credentials{
		address:  addr,
		key:      address,
		username: username,
		password: password,
	}, stop, done)
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

// run is the connect/reconnect loop. Exactly one runs per Connect.
//
// First attempt failing is terminal: the user is looking at a connect form and
// needs the error, not a silent retry. The exception is a relay that answers
// "not now, come back in N seconds" (429, 503): that attempt keeps retrying,
// and keeps the user on the connect form while it does. Once a session has
// been established, every unexpected drop is retried with 1s, 2s, 4s ... capped
// at 30s, until Disconnect, Close, or a terminal condition (kick, ban,
// rejected credentials).
func (m *Manager) run(c credentials, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	c.bearer = relayBearer(c, m.deriveFn)
	attempt := 0
	reconnecting := false
	// roadsLeft bounds one immediate search across the roads, so a server that
	// is simply down cannot become a loop of instant retries.
	roadsLeft := len(m.transports.roads(c.key)) - 1
	// Moving to the next road continues the same attempt rather than starting
	// a new one, so it must not announce itself again.
	searching := false

	for {
		if isStopped(stop) {
			return
		}
		// A fresh wave - anything but continuing an in-flight road search - may
		// search every road once again. Without replenishing here the budget is
		// spent on the first wave and never restored, and the chooser pins to
		// whichever road it stopped on even after another one recovers: a link
		// that loses both roads and gets one back would never reconnect.
		if !searching {
			roadsLeft = len(m.transports.roads(c.key)) - 1
		}
		if !reconnecting && !searching {
			m.emitStatus(domain.ConnectionStatus{State: domain.StateConnecting, Server: c.address})
		}
		searching = false

		dropped := make(chan *gumble.DisconnectEvent, 1)
		transport := m.transports.next(c.key)
		session, err := m.dialOnce(c, transport, dropped)
		if err != nil {
			var mismatch *MismatchError
			if errors.As(err, &mismatch) {
				if !m.awaitFingerprint(c.address, mismatch, stop) {
					return
				}
				// Accepted: retry immediately, the backoff is untouched.
				continue
			}

			// The address never reaches gul.log: network errors embed
			// host:port on their own (PLAN.md §10.7).
			m.log.Warn("connect attempt failed", "error", RedactServer(err.Error(), c.address))

			// A relay that turns the attempt away for now states how long it
			// wants to be left alone. Retrying earlier only extends the
			// refusal, so this is never terminal and never rushed - not even
			// on the very first attempt. What it must not do on a first
			// attempt is take the user off the connect form: until a session
			// has existed, the form is where the message and the wait belong,
			// and "reconnecting" would strand the user behind a locked screen.
			if wait, message, refused := relayRefusal(err, m.backoffFn(attempt)); refused {
				state := domain.StateConnecting
				if reconnecting {
					state = domain.StateReconnecting
				}
				m.emitStatus(domain.ConnectionStatus{
					State: state, Server: c.address, Error: message,
				})
				if !sleepOrStop(wait, stop) {
					return
				}
				attempt++
				continue
			}

			// A road that would not open is a fact about the road, not about
			// the user. Try the next one straight away - no backoff, nothing
			// said yet: on a first connect this is the difference between
			// "cannot reach the server" and simply arriving by the other road.
			if roadsLeft > 0 && isRoadFailure(err) {
				roadsLeft--
				searching = true
				m.transports.failed(c.key)
				m.log.Info("road did not open, trying another", "transport", string(transport))
				continue
			}

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

		event, stopped, silent := m.waitSession(session.client, c.key, transport, dropped, stop)
		m.clearSession()
		if stopped {
			_ = session.Disconnect()
			return
		}

		reason, terminal := disconnectReason(event)
		// note is the diagnostic the user actually sees on the reconnect
		// banner. Only the two cases below carry one: an ordinary drop leaves
		// the banner as it was rather than flashing "connection lost" at every
		// blip. Both constants are addressless, so neither is redacted.
		note := ""
		switch {
		case silent:
			// Nothing of ours ever came back. The session looks perfect from
			// the inside, so it has to be taken down here - and the road it
			// took is the one to stop using.
			_ = session.Disconnect()
			m.transports.failed(c.key)
			m.log.Info("giving up on this road, trying another",
				"transport", string(transport))
			reason, terminal = reasonNoRoundTrip, false
			note = reasonNoRoundTrip
		case session.stalledUplink():
			reason = reasonUplinkStalled
			note = reasonUplinkStalled
		}
		if terminal {
			m.emitStatus(domain.ConnectionStatus{
				State: domain.StateDisconnected, Server: c.address, Error: reason,
			})
			return
		}
		m.log.Warn("connection lost", "reason", RedactServer(reason, c.address))
		reconnecting = true
		m.emitStatus(domain.ConnectionStatus{State: domain.StateReconnecting, Server: c.address, Error: note})
		if !sleepOrStop(m.backoffFn(attempt), stop) {
			return
		}
		attempt++
	}
}

// waitSession owns the stats ticker for exactly one live session. Returning
// stops new requests before reconnecting; publishLatency rejects any response
// that was already in flight from the old client.
// It also holds the round-trip gate: silent reports a session that never
// proved our packets reach the server (roundTripGrace).
func (m *Manager) waitSession(
	client *gumble.Client,
	address string,
	transport Transport,
	dropped <-chan *gumble.DisconnectEvent,
	stop <-chan struct{},
) (event *gumble.DisconnectEvent, stopped, silent bool) {
	interval := m.statsInterval
	if interval <= 0 {
		interval = statsPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sample := m.sampleLatencyFn
	if sample == nil {
		sample = m.sampleLatency
	}
	sample(client)

	verify := time.NewTimer(m.roundTripGrace)
	defer verify.Stop()

	for {
		select {
		case <-stop:
			return nil, true, false
		case event := <-dropped:
			return event, false, false
		case <-verify.C:
			if m.roundTripFn != nil && !m.roundTripFn(client) {
				return nil, false, true
			}
			// Packets of ours go there and come back on this road. That is the
			// only thing worth remembering about it (transport.go).
			if m.transports.succeeded(address, transport) {
				m.log.Info("road proved itself", "transport", string(transport))
				if cb := m.cb.OnTransport; cb != nil {
					cb(address, string(transport))
				}
			}
		case <-ticker.C:
			sample(client)
		}
	}
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
		Address:         c.address,
		Username:        c.username,
		Password:        c.password,
		Certificate:     &cert,
		RelayCredential: c.bearer,
		Transport:       transport,
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

// PreferTransport seeds the road to try first for one server. Anything the
// chooser does not recognise is ignored, which simply leaves the search alone.
func (m *Manager) PreferTransport(address, transport string) {
	m.transports.prefer(address, Transport(transport))
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

// isRoadFailure reports whether a failed dial says anything about the road it
// took. It does only when nothing answered: a server that rejected us, a relay
// that refused the credential or asked us to come back later, all answered,
// and taking a different road to the same answer would only spend the user's
// time twice.
func isRoadFailure(err error) bool {
	var reject *gumble.RejectError
	if errors.As(err, &reject) {
		return false
	}
	return !isTerminalDialError(err) && !isTerminalRelayError(err)
}

// isTerminalDialError reports whether retrying can only fail the same way.
//
// DECISION: "username in use" and "server full" are treated as transient - the
// first is the common race where the server has not yet reaped our previous
// session after a drop, the second clears on its own.
func isTerminalDialError(err error) bool {
	if errors.Is(err, ErrRelayPasswordRequired) || errors.Is(err, ErrRelayAuthentication) {
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

// relayBearer derives the relay credential once per Connect. Direct Mumble
// endpoints never reach a relay, so they do not pay the PBKDF2 cost.
func relayBearer(c credentials, derive func([]byte) relayproto.Credential) relayproto.Credential {
	if c.password == "" {
		return ""
	}
	ep, err := parseEndpoint(c.address)
	if err != nil || ep.kind != endpointWSS {
		return ""
	}
	return derive([]byte(c.password))
}

// relayRefusal recognizes a relay answer that rejects this attempt but says
// when to come back: 429 (this client is asking too often) and 503 (the relay
// is full). Both are transient, and both carry a Retry-After the client must
// honour - the backoff ladder is only the floor, never a way to come earlier.
func relayRefusal(err error, ladder time.Duration) (wait time.Duration, message string, ok bool) {
	var limited *RateLimitedError
	if errors.As(err, &limited) {
		wait = max(limited.RetryAfter, ladder)
		return wait, rateLimitedMessage(wait), true
	}
	var full *RelayFullError
	if errors.As(err, &full) {
		wait = max(full.RetryAfter, ladder)
		return wait, relayFullMessage(wait), true
	}
	return 0, "", false
}

// rateLimitedMessage is user-visible: the connect form and the reconnect
// banner show it verbatim.
func rateLimitedMessage(wait time.Duration) string {
	return fmt.Sprintf("Сервер временно отклоняет подключения. Следующая попытка через %s.", humanSeconds(wait))
}

// relayFullMessage is user-visible: a relay at capacity is not a failure of
// the address, the password or the nickname, so it must not read like one.
func relayFullMessage(wait time.Duration) string {
	return fmt.Sprintf("Сервер переполнен: нет свободных мест. Следующая попытка через %s.", humanSeconds(wait))
}

// humanSeconds renders a wait in whole seconds with the Russian plural form.
func humanSeconds(d time.Duration) string {
	seconds := int64(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10) + " " + pluralSeconds(seconds)
}

func pluralSeconds(n int64) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "секунд"
	}
	switch n % 10 {
	case 1:
		return "секунду"
	case 2, 3, 4:
		return "секунды"
	default:
		return "секунд"
	}
}
