package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/hotkey"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
	"github.com/LywwKkA-aD/Gul/internal/notify"
	"github.com/LywwKkA-aD/Gul/internal/secret"
)

// Version is the application version reported in diagnostics and the about
// screen. Its numeric base must match build/config.yml; platform metadata omits
// the prerelease suffix where the native format requires numeric components.
const Version = "0.4.0-alpha.2"

const (
	// historyPerChannel caps the in-memory session transcript per channel.
	// Older messages are evicted; nothing is persisted (PLAN.md §7 M1).
	historyPerChannel = 500
	// maxOutgoingText matches the Mumble server default message length.
	maxOutgoingText = 5000
)

var (
	ErrNotConnected  = errors.New("not connected")
	ErrEmptyUsername = errors.New("username is required")
	ErrEmptyAddress  = errors.New("server address is required")
	ErrEmptyMessage  = errors.New("message is empty")
	ErrMessageTooBig = errors.New("message is too long")
	ErrUnknownServer = errors.New("server is not remembered")
	// ErrPasswordUnreadable reports a credential store that could not
	// answer - typically a locked keyring. It is not "there is no
	// password": the connect stops rather than dialling without one
	// (servers.go).
	ErrPasswordUnreadable = errors.New("stored password could not be read")
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

	// hotkeyMu serializes the global push-to-talk watch. It is the outer
	// lock: mu may be taken under it, never the other way round (hotkey.go).
	hotkeyMu   sync.Mutex
	watching   bool
	watchedKey string

	mu       sync.Mutex
	ctrl     mumble.Controller
	status   domain.ConnectionStatus
	tree     domain.ChannelNode
	history  map[uint32][]domain.ChatMessage
	seq      uint64
	username string
	// address of the attempt in flight, remembered only once the server has
	// accepted it (settings.go).
	address string
	// pendingPassword is the password of the attempt in flight. It is held
	// only until the server accepts the attempt, so that a password typed
	// wrongly is never written to the credential store, and is dropped the
	// moment it is committed or the session ends (servers.go).
	pendingPassword string

	// secrets is the credential store remembered passwords live in. Nil
	// until SetSecrets, which is what keeps a core built for a test from
	// touching the machine's keychain (servers.go).
	secrets secret.Store

	voice                 VoiceEngine
	captureID, playbackID string

	// Self audio state and its tray observers (selfaudio.go).
	selfMuted, selfDeafened bool
	// selfAudioGen orders the self audio transitions. Only the newest one
	// is allowed to reach the UI and the tray, so a gesture that lost the
	// race cannot repaint the icons after the winner already did.
	selfAudioGen  uint64
	trayObservers []func(TrayState)

	// connectionCommitted keeps the accepted-connect commit to once per
	// session: StateConnected is re-emitted on every self channel change
	// (settings.go).
	connectionCommitted bool

	// pttMu guards pttHeld AND is held across its event, so a press can
	// never be published after the release that followed it (voice.go). It
	// takes no other lock, so it cannot deadlock against a.mu.
	pttMu   sync.Mutex
	pttHeld bool

	// Baseline of the channel we are in, for the join and leave cues
	// (cues.go).
	cueChannel  uint32
	cueMembers  map[uint32]string
	cueBaseline bool

	// Global push-to-talk (hotkey.go). The monitor and the last watch error
	// are guarded by mu; hotkeyMu guards the watch itself.
	keys      hotkey.Monitor
	hotkeyErr string

	// System notifications for a window nobody is looking at (notify.go).
	// The decider always exists; the notifier is nil until main.go injects
	// one, which is what keeps every test off the notification centre.
	notifier      Notifier
	notifications *notify.Decider

	// Startup version check (update.go). The endpoint and client are empty
	// in production, where the package defaults apply; only tests point them
	// somewhere else.
	updateEndpoint string
	updateClient   *http.Client
	updateCancel   context.CancelFunc
	pendingUpdate  domain.UpdateAvailable

	// Persisted settings (settings.go). cfgDir is empty until UseSettings,
	// which is what keeps a core built for a test from writing anything.
	cfgDir          string
	cfg             config.Config
	persistSettings bool
	saver           *settingsSaver
}

// New builds the application core. The Mumble controller is injected later
// with SetController, because it is constructed in main.go with callbacks that
// point back at this App.
func New(log *slog.Logger, emitter domain.Emitter) *App {
	if log == nil {
		log = slog.Default()
	}
	a := &App{
		log:           log,
		emitter:       emitter,
		status:        domain.ConnectionStatus{State: domain.StateDisconnected},
		history:       make(map[uint32][]domain.ChatMessage),
		cfg:           config.Defaults(),
		notifications: notify.New(0, 0),
	}
	a.saver = newSettingsSaver(settingsSaveWindow, a.saveSettings)
	return a
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
		OnStatus:    a.HandleStatus,
		OnLatency:   a.HandleLatency,
		OnTree:      a.HandleTree,
		OnMessage:   a.HandleMessage,
		OnTofu:      a.HandleTofu,
		OnTransport: a.HandleTransport,
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
	case len(address) > config.MaxAddressLen:
		return fmt.Errorf("%w: address longer than %d bytes", ErrEmptyAddress, config.MaxAddressLen)
	case username == "":
		return ErrEmptyUsername
	case utf8.RuneCountInString(username) > config.MaxUsernameLen:
		return fmt.Errorf("%w: nickname longer than %d characters", ErrEmptyUsername, config.MaxUsernameLen)
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
	a.address = address
	a.pendingPassword = password
	a.connectionCommitted = false
	a.mu.Unlock()

	// Address and username are deliberately omitted: malformed WSS URLs may
	// contain credentials, and diagnostics logs are intended to be shareable.
	a.log.Info("connect requested")
	// What worked last time for this server, so a client whose usual road is
	// blocked does not pay the round-trip gate again on every launch
	// (internal/mumble/transport.go).
	ctrl.PreferTransport(address, a.rememberedTransport(address))
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
	a.handleMessage(mumble.RawMessage{
		ChannelID: channelID,
		Sender:    sender,
		HTML:      EscapePlain(text),
	}, true)
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

	// The address and everything derived from it (network errors embed
	// host:port) stays out of the record: gul.log travels in diagnostics
	// archives the user shares (PLAN.md §10.7).
	a.log.Debug("connection state", "state", string(s.State),
		"error", mumble.RedactServer(s.Error, s.Server))
	a.emit(domain.EventConnectionState, s)

	// The voice engine lives while the session does; reconnects keep it
	// running (the transport channel survives them).
	switch {
	case s.State == domain.StateConnected && prev != domain.StateReconnecting:
		a.commitConnection()
		a.startVoice()
	case s.State == domain.StateDisconnected:
		a.mu.Lock()
		a.connectionCommitted = false
		a.mu.Unlock()
		a.stopVoice()
		// A connect the server refused ends here, never at commitConnection,
		// so this is where a password that was never accepted stops being
		// held.
		a.dropPendingPassword()
	}

	// A session that is not up has no channel to compare against: the next
	// tree starts a new baseline instead of announcing everybody again.
	if s.State != domain.StateConnected {
		a.resetChannelCues()
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
	a.reconcileSelfAudio(root)

	if cue, who, ok := a.channelCue(root); ok {
		a.playCue(cue)
		// The cue is the half the user hears; a notification is the other
		// half, for a window they are not looking at (notify.go).
		a.notifyChannelCue(cue, who)
	}
}

// HandleMessage sanitizes an incoming chat message, appends it to the session
// history and pushes it to the UI.
func (a *App) HandleMessage(raw mumble.RawMessage) {
	a.handleMessage(raw, false)
}

// handleMessage is HandleMessage plus the one fact only the caller knows:
// whether this is our own message, echoed locally because Mumble servers never
// deliver a text message back to its sender. Our own words must not notify us.
func (a *App) handleMessage(raw mumble.RawMessage, local bool) {
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
	selfChannel, connected := a.status.SelfChannel, a.status.State == domain.StateConnected
	a.mu.Unlock()

	a.emit(domain.EventChatMessage, msg)

	// Only the channel we are in, and only somebody else's words. The server
	// delivers text for our own channel anyway; the check is what keeps a
	// message replayed for another channel out of the notification centre
	// (notify.go).
	if !local && connected && msg.ChannelID == selfChannel {
		a.notifyMessage(msg.Sender, msg.HTML)
	}
}

// HandleTofu pushes a certificate mismatch prompt to the UI. The connection
// stays blocked until the user calls AcceptFingerprint.
func (a *App) HandleTofu(p domain.TofuPrompt) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()

	a.log.Warn("server fingerprint changed")
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

// Shutdown releases what must not outlive the process: the global key watch
// (and any transmission it opened), a version check still waiting on GitHub,
// and a settings change still inside the debounce window. Runs on the way out,
// before the services are stopped.
func (a *App) Shutdown() {
	a.StopGlobalPTT()
	a.stopUpdateCheck()
	a.FlushSettings()
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
