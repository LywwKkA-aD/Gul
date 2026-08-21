package core

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// Version is the application version reported in diagnostics and the about
// screen. Keep it aligned with build/config.yml package metadata.
const Version = "0.2.0"

const (
	// historyPerChannel caps the in-memory session transcript per channel.
	// Older messages are evicted; nothing is persisted (PLAN.md §7 M1).
	historyPerChannel = 500
	// maxOutgoingText matches the Mumble server default message length.
	maxOutgoingText = 5000
	maxAddressLen   = 255
	maxUsernameLen  = 64
)

var (
	ErrNotConnected  = errors.New("not connected")
	ErrEmptyUsername = errors.New("username is required")
	ErrEmptyAddress  = errors.New("server address is required")
	ErrEmptyMessage  = errors.New("message is empty")
	ErrMessageTooBig = errors.New("message is too long")
)

// App owns the application state and orchestrates the layers beneath the Wails
// services. Services stay thin and delegate here (PLAN.md §10.4).
//
// Locking: mu guards the state fields and is never held across an Emit, so a
// synchronous listener on the emitter cannot deadlock us. notifyMu serializes
// the Mumble callbacks end to end, which keeps the order of emitted events
// identical to the order of the state transitions they describe.
type App struct {
	log     *slog.Logger
	emitter domain.Emitter

	notifyMu sync.Mutex

	mu       sync.Mutex
	ctrl     mumble.Controller
	status   domain.ConnectionStatus
	tree     domain.ChannelNode
	history  map[uint32][]domain.ChatMessage
	seq      uint64
	username string

	voice                 VoiceEngine
	captureID, playbackID string
}

// New builds the application core. The Mumble controller is injected later
// with SetController, because it is constructed in main.go with callbacks that
// point back at this App.
func New(log *slog.Logger, emitter domain.Emitter) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{
		log:     log,
		emitter: emitter,
		status:  domain.ConnectionStatus{State: domain.StateDisconnected},
		history: make(map[uint32][]domain.ChatMessage),
	}
}

// SetController injects the Mumble controller. Call once, before the UI runs.
func (a *App) SetController(c mumble.Controller) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctrl = c
}

// Callbacks bundles the handler methods for the Mumble layer, so main.go can
// wire the controller without knowing which method maps to which hook.
func (a *App) Callbacks() mumble.Callbacks {
	return mumble.Callbacks{
		OnStatus:  a.HandleStatus,
		OnLatency: a.HandleLatency,
		OnTree:    a.HandleTree,
		OnMessage: a.HandleMessage,
		OnTofu:    a.HandleTofu,
	}
}

func (a *App) controller() (mumble.Controller, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctrl == nil {
		return nil, ErrNotConnected
	}
	return a.ctrl, nil
}

// ----------------------------------------------------------------------------
// Commands (driven by services)
// ----------------------------------------------------------------------------

// Connect validates the form input and starts an asynchronous connection.
// Progress arrives through HandleStatus, not through this return value.
func (a *App) Connect(address, username, password string) error {
	address = strings.TrimSpace(address)
	username = strings.TrimSpace(username)

	switch {
	case address == "":
		return ErrEmptyAddress
	case len(address) > maxAddressLen:
		return fmt.Errorf("%w: address longer than %d bytes", ErrEmptyAddress, maxAddressLen)
	case username == "":
		return ErrEmptyUsername
	case utf8.RuneCountInString(username) > maxUsernameLen:
		return fmt.Errorf("%w: nickname longer than %d characters", ErrEmptyUsername, maxUsernameLen)
	}

	ctrl, err := a.controller()
	if err != nil {
		return err
	}

	// A user-initiated connect starts a new session, so the transcript resets.
	// Reconnects do not pass through here and keep their history.
	a.mu.Lock()
	a.history = make(map[uint32][]domain.ChatMessage)
	a.username = username
	a.mu.Unlock()

	a.log.Info("connect requested", "address", address, "username", username)
	ctrl.Connect(address, username, password)
	return nil
}

// Disconnect stops the session and any reconnect loop.
func (a *App) Disconnect() {
	ctrl, err := a.controller()
	if err != nil {
		return
	}
	a.log.Info("disconnect requested")
	ctrl.Disconnect()
}

// Join moves self into the channel.
func (a *App) Join(channelID uint32) error {
	ctrl, err := a.controller()
	if err != nil {
		return err
	}
	return ctrl.Join(channelID)
}

// SendMessage sends plain text to a channel. The Mumble layer escapes it into
// HTML; core only rejects what is not worth sending.
func (a *App) SendMessage(channelID uint32, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrEmptyMessage
	}
	if utf8.RuneCountInString(text) > maxOutgoingText {
		return fmt.Errorf("%w: limit is %d characters", ErrMessageTooBig, maxOutgoingText)
	}
	ctrl, err := a.controller()
	if err != nil {
		return err
	}
	if err := ctrl.SendMessage(channelID, text); err != nil {
		return err
	}

	// Local echo: Mumble servers deliver a text message to the other channel
	// members only, never back to the sender - without this our own messages
	// would exist for everyone except us.
	a.mu.Lock()
	sender := a.username
	a.mu.Unlock()
	a.HandleMessage(mumble.RawMessage{
		ChannelID: channelID,
		Sender:    sender,
		HTML:      EscapePlain(text),
	})
	return nil
}

// AcceptFingerprint confirms a pending TOFU mismatch and retries the connection.
func (a *App) AcceptFingerprint() {
	ctrl, err := a.controller()
	if err != nil {
		return
	}
	a.log.Warn("tofu fingerprint accepted by user")
	ctrl.AcceptFingerprint()
}

// ----------------------------------------------------------------------------
// Queries (snapshots for a freshly mounted UI)
// ----------------------------------------------------------------------------

// Status returns the last known connection status.
func (a *App) Status() domain.ConnectionStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Tree returns the last channel tree snapshot pushed by the Mumble layer.
func (a *App) Tree() domain.ChannelNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tree
}

// History returns a copy of the session transcript for one channel, oldest
// message first. The copy keeps the UI from observing later evictions.
func (a *App) History(channelID uint32) []domain.ChatMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored := a.history[channelID]
	out := make([]domain.ChatMessage, len(stored))
	copy(out, stored)
	return out
}

// ----------------------------------------------------------------------------
// Mumble callbacks (invoked from session goroutines: fast, no blocking I/O)
// ----------------------------------------------------------------------------

// HandleStatus records a lifecycle change and pushes it to the UI.
func (a *App) HandleStatus(s domain.ConnectionStatus) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	a.mu.Lock()
	prev := a.status.State
	a.status = s
	a.mu.Unlock()

	a.log.Debug("connection state", "state", string(s.State), "server", s.Server, "error", s.Error)
	a.emit(domain.EventConnectionState, s)

	// The voice engine lives while the session does; reconnects keep it
	// running (the transport channel survives them).
	switch {
	case s.State == domain.StateConnected && prev != domain.StateReconnecting:
		a.startVoice()
	case s.State == domain.StateDisconnected:
		a.stopVoice()
	}
}

// HandleLatency forwards session telemetry separately from lifecycle status.
// A periodic sample must not look like a fresh connection and restart audio.
func (a *App) HandleLatency(latency domain.ConnectionLatency) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	a.emit(domain.EventConnectionLatency, latency)
}

// HandleTree records a channel tree snapshot and pushes it to the UI.
func (a *App) HandleTree(root domain.ChannelNode) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	a.mu.Lock()
	a.tree = root
	a.mu.Unlock()

	a.emit(domain.EventChannelsTree, root)
}

// HandleMessage sanitizes an incoming chat message, appends it to the session
// history and pushes it to the UI.
func (a *App) HandleMessage(raw mumble.RawMessage) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	at := time.Now().UTC()

	a.mu.Lock()
	msg := domain.ChatMessage{
		ID:         a.nextID(at),
		ChannelID:  raw.ChannelID,
		Sender:     raw.Sender,
		SenderHash: raw.SenderHash,
		HTML:       SanitizeHTML(raw.HTML),
		At:         at,
	}
	a.appendLocked(msg)
	a.mu.Unlock()

	a.emit(domain.EventChatMessage, msg)
}

// HandleTofu pushes a certificate mismatch prompt to the UI. The connection
// stays blocked until the user calls AcceptFingerprint.
func (a *App) HandleTofu(p domain.TofuPrompt) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	a.log.Warn("server fingerprint changed", "server", p.Server)
	a.emit(domain.EventTofuMismatch, p)
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

func (a *App) emit(name string, payload any) {
	if a.emitter == nil {
		return
	}
	a.emitter.Emit(name, payload)
}

// appendLocked stores a message and evicts the oldest once the cap is reached.
// Caller holds a.mu.
func (a *App) appendLocked(msg domain.ChatMessage) {
	if a.history == nil {
		a.history = make(map[uint32][]domain.ChatMessage)
	}
	h := append(a.history[msg.ChannelID], msg)
	if over := len(h) - historyPerChannel; over > 0 {
		// Shift in place: the backing array is reused, so a long-lived channel
		// does not grow its allocation without bound.
		h = h[:copy(h, h[over:])]
	}
	a.history[msg.ChannelID] = h
}

// nextID builds a sortable, dependency-free message id: base36 milliseconds
// plus a monotonic per-process counter, so ids stay unique even inside the
// same millisecond. Caller holds a.mu.
func (a *App) nextID(at time.Time) string {
	a.seq++
	return strconv.FormatInt(at.UnixMilli(), 36) + "-" + strconv.FormatUint(a.seq, 36)
}

// Collect writes a diagnostics bundle into the application config directory and
// returns its path.
func (a *App) Collect() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	path, err := Collect(dir)
	if err != nil {
		a.log.Error("collect diagnostics", "error", err)
		return "", err
	}
	a.log.Info("diagnostics collected", "path", path)
	return path, nil
}
