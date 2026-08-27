package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/secret"
)

// ---------------------------------------------------------------------------
// doubles
// ---------------------------------------------------------------------------

// fakeSecrets is an in-memory credential store. It can be switched off, to
// stand for a machine that has none, and made to fail, to stand for a locked
// keyring.
type fakeSecrets struct {
	mu        sync.Mutex
	items     map[string]string
	available bool
	setErr    error
	deleteErr error
	getErr    error
}

func newFakeSecrets() *fakeSecrets {
	return &fakeSecrets{items: map[string]string{}, available: true}
}

func (f *fakeSecrets) Set(account, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.items[account] = value
	return nil
}

func (f *fakeSecrets) Get(account string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", false, f.getErr
	}
	value, ok := f.items[account]
	return value, ok, nil
}

func (f *fakeSecrets) Delete(account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.items, account)
	return nil
}

func (f *fakeSecrets) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.available
}

func (f *fakeSecrets) stored(account string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.items[account]
	return value, ok
}

func (f *fakeSecrets) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

var _ secret.Store = (*fakeSecrets)(nil)

// serversFixture is an App that persists into a temp dir, whose debounce
// window the test closes by hand, with a credential store it can inspect.
type serversFixture struct {
	app   *App
	ctrl  *fakeController
	keys  *fakeSecrets
	clock *fakeClock
	dir   string
}

func newServersApp(t *testing.T) *serversFixture {
	t.Helper()
	dir := t.TempDir()
	ctrl := &fakeController{}
	keys := newFakeSecrets()

	app := New(discardLogger(), nil)
	app.SetController(ctrl)
	app.SetSecrets(keys)
	clock := newFakeClock()
	app.saver.after = clock.after
	cfg, err := config.Load(dir)
	app.UseSettings(dir, cfg, err)

	return &serversFixture{app: app, ctrl: ctrl, keys: keys, clock: clock, dir: dir}
}

// connected drives one complete, accepted connect. The server entry is
// committed synchronously; the password write is not (the credential store may
// block and the accepted-connect path runs on the gumble read loop), so tests
// that assert on it go through storedPassword.
func (f *serversFixture) connected(t *testing.T, address, username, password string) {
	t.Helper()
	if err := f.app.Connect(address, username, password); err != nil {
		t.Fatalf("Connect(%q): %v", address, err)
	}
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
}

// storedPassword waits for the off-goroutine credential write and returns what
// landed. Tests whose store cannot hold a password assert through the store
// itself instead.
func (f *serversFixture) storedPassword(t *testing.T, address string) (string, bool) {
	t.Helper()
	var (
		got string
		ok  bool
	)
	waitFor(t, func() bool {
		got, ok = f.app.PasswordFor(address)
		return ok
	}, "the password write to land")
	return got, ok
}

// document reads the settings file back as it was written.
func (f *serversFixture) document(t *testing.T) []byte {
	t.Helper()
	f.clock.fire()
	data, err := os.ReadFile(filepath.Join(f.dir, config.FileName))
	if err != nil {
		t.Fatalf("read %s: %v", config.FileName, err)
	}
	return data
}

func serverAddresses(list []domain.SavedServer) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Address)
	}
	return out
}

// ---------------------------------------------------------------------------
// remembering
// ---------------------------------------------------------------------------

func TestSuccessfulConnectRemembersTheServerAndItsPassword(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "mumble.example:64738", "gul", "hunter2")
	f.storedPassword(t, "mumble.example:64738")

	got := f.app.Servers()
	if len(got) != 1 {
		t.Fatalf("servers = %+v, want one", got)
	}
	if got[0].Address != "mumble.example:64738" || got[0].Username != "gul" || !got[0].HasPassword {
		t.Fatalf("server = %+v", got[0])
	}
	if value, ok := f.keys.stored("mumble.example:64738"); !ok || value != "hunter2" {
		t.Fatalf("stored password = %q, %v", value, ok)
	}

	// The picker is additive: the last-used pair the connect form starts on
	// keeps working exactly as before.
	cfg := f.app.Settings()
	if cfg.Connection.LastAddress != "mumble.example:64738" || cfg.Connection.LastUsername != "gul" {
		t.Errorf("connection = %+v", cfg.Connection)
	}
}

// The commit happens when the server accepts, not when the user presses
// connect: a wrong password or a dead host must leave no trace.
func TestFailedConnectRemembersNothing(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	if err := f.app.Connect("wrong.example:64738", "gul", "not-the-password"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	f.app.HandleStatus(domain.ConnectionStatus{
		State: domain.StateDisconnected,
		Error: "wrong server password",
	})

	if got := f.app.Servers(); len(got) != 0 {
		t.Fatalf("servers = %+v, want none", got)
	}
	if f.keys.count() != 0 {
		t.Fatalf("a password was stored for a connect that never succeeded")
	}
	// And the password of the refused attempt is not held any more.
	f.app.mu.Lock()
	pending := f.app.pendingPassword
	f.app.mu.Unlock()
	if pending != "" {
		t.Errorf("pending password = %q, want dropped", pending)
	}
}

func TestRememberingIsCappedAndDeduplicated(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	for i := range config.MaxServers + 3 {
		f.connected(t, fmt.Sprintf("host%02d.example:64738", i), "gul", "")
	}
	// The very first server, connected to again, comes back to the top
	// instead of appearing twice.
	f.connected(t, "host00.example:64738", "renamed", "")

	got := f.app.Servers()
	if len(got) != config.MaxServers {
		t.Fatalf("servers = %d, want %d", len(got), config.MaxServers)
	}
	if got[0].Address != "host00.example:64738" || got[0].Username != "renamed" {
		t.Fatalf("first = %+v, want the one just used", got[0])
	}
	seen := map[string]int{}
	for _, s := range got {
		seen[s.Address]++
	}
	for address, n := range seen {
		if n != 1 {
			t.Errorf("%s appears %d times", address, n)
		}
	}
}

// A machine with no credential store still remembers servers; it just cannot
// remember what to type into the password field.
func TestUnavailableStoreStillRemembersTheServer(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)
	f.keys.available = false

	f.connected(t, "mumble.example:64738", "gul", "hunter2")

	got := f.app.Servers()
	if len(got) != 1 || got[0].Address != "mumble.example:64738" {
		t.Fatalf("servers = %+v", got)
	}
	if got[0].HasPassword {
		t.Errorf("HasPassword is true on a machine with no store")
	}
	if f.keys.count() != 0 {
		t.Errorf("the store was written despite reporting itself unavailable")
	}
	if _, ok := f.app.PasswordFor("mumble.example:64738"); ok {
		t.Errorf("PasswordFor answered from a store that is not available")
	}
}

// A store that is there but refuses costs the password, not the server.
func TestStoreFailureStillRemembersTheServer(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)
	f.keys.setErr = errors.New("keyring is locked")

	f.connected(t, "mumble.example:64738", "gul", "hunter2")

	got := f.app.Servers()
	if len(got) != 1 || got[0].HasPassword {
		t.Fatalf("servers = %+v, want one entry without a password", got)
	}
}

// Connecting to a remembered server without a password means the password is
// no longer wanted: the picker must not keep offering a stale one.
func TestConnectingWithoutAPasswordClearsTheStoredOne(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "mumble.example:64738", "gul", "hunter2")
	f.storedPassword(t, "mumble.example:64738")
	if _, ok := f.keys.stored("mumble.example:64738"); !ok {
		t.Fatal("setup: the password was not stored")
	}

	// Clearing is the same off-goroutine write, so it needs the same wait.
	f.connected(t, "mumble.example:64738", "gul", "")
	waitFor(t, func() bool {
		_, ok := f.keys.stored("mumble.example:64738")
		return !ok
	}, "the stored password to be cleared")

	if got := f.app.Servers(); len(got) != 1 || got[0].HasPassword {
		t.Fatalf("servers = %+v", got)
	}
}

// A reconnect is the same session continuing, not a new attempt to commit.
func TestReconnectDoesNotRecommit(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "mumble.example:64738", "gul", "hunter2")
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateReconnecting})
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})

	if got := f.app.Servers(); len(got) != 1 {
		t.Fatalf("servers = %+v, want one", got)
	}
}

// ---------------------------------------------------------------------------
// forgetting
// ---------------------------------------------------------------------------

func TestForgetServerClearsBothPlaces(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "one.example:64738", "gul", "first")
	f.storedPassword(t, "one.example:64738")
	f.connected(t, "two.example:64738", "gul", "second")
	f.storedPassword(t, "two.example:64738")

	if err := f.app.ForgetServer("one.example:64738"); err != nil {
		t.Fatalf("ForgetServer: %v", err)
	}

	if got := serverAddresses(f.app.Servers()); len(got) != 1 || got[0] != "two.example:64738" {
		t.Fatalf("servers = %v", got)
	}
	if _, ok := f.keys.stored("one.example:64738"); ok {
		t.Errorf("the password of a forgotten server survived")
	}
	if _, ok := f.keys.stored("two.example:64738"); !ok {
		t.Errorf("forgetting one server took another's password with it")
	}

	// The written document has lost the entry too.
	if strings.Contains(string(f.document(t)), "one.example:64738") {
		t.Errorf("the forgotten server is still in the settings file")
	}
}

// The entry goes whatever the store does, and the password that survived is
// reported rather than swallowed.
func TestForgetServerReportsAPasswordItCouldNotRemove(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "one.example:64738", "gul", "first")
	f.keys.deleteErr = errors.New("keyring is locked")

	err := f.app.ForgetServer("one.example:64738")
	if err == nil {
		t.Fatal("ForgetServer reported success while the password stayed")
	}
	if got := f.app.Servers(); len(got) != 0 {
		t.Fatalf("servers = %+v, want the entry gone regardless", got)
	}
}

// ---------------------------------------------------------------------------
// connecting from the picker
// ---------------------------------------------------------------------------

func TestConnectSavedDialsWithTheStoredPassword(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "mumble.example:64738", "gul", "hunter2")
	f.storedPassword(t, "mumble.example:64738")
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})

	if _, err := f.app.connectSaved(" mumble.example:64738 "); err != nil {
		t.Fatalf("connectSaved: %v", err)
	}

	calls := f.ctrl.snapshot().connects
	if len(calls) != 2 {
		t.Fatalf("connects = %+v", calls)
	}
	if calls[1] != (connectCall{"mumble.example:64738", "gul", "hunter2"}) {
		t.Fatalf("second connect = %+v", calls[1])
	}
}

func TestConnectSavedWithoutAStoredPasswordDialsWithoutOne(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)
	f.connected(t, "mumble.example:64738", "gul", "")

	if _, err := f.app.connectSaved("mumble.example:64738"); err != nil {
		t.Fatalf("connectSaved: %v", err)
	}
	calls := f.ctrl.snapshot().connects
	if calls[len(calls)-1].password != "" {
		t.Fatalf("connect = %+v, want no password", calls[len(calls)-1])
	}
}

// A keyring that refuses to answer must not look like a user who chose to
// have no password: dialling without one would make the next successful
// connect drop the stored password for good.
func TestConnectSavedStopsWhenThePasswordCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "mumble.example:64738", "gul", "hunter2")
	f.storedPassword(t, "mumble.example:64738")
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	f.keys.getErr = errors.New("keyring is locked")

	if _, err := f.app.connectSaved("mumble.example:64738"); !errors.Is(err, ErrPasswordUnreadable) {
		t.Fatalf("connectSaved = %v, want ErrPasswordUnreadable", err)
	}
	if calls := f.ctrl.snapshot().connects; len(calls) != 1 {
		t.Fatalf("connects = %+v, want only the original", calls)
	}

	// And the password is still there once the keyring answers again.
	f.keys.getErr = nil
	if value, ok := f.keys.stored("mumble.example:64738"); !ok || value != "hunter2" {
		t.Fatalf("stored password = %q, %v", value, ok)
	}
}

func TestConnectSavedRejectsAnUnknownServer(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	_, err := f.app.connectSaved("stranger.example:64738")
	if !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("error = %v, want ErrUnknownServer", err)
	}
	if calls := f.ctrl.snapshot().connects; len(calls) != 0 {
		t.Fatalf("an unknown server was dialled: %+v", calls)
	}
}

// The two failures the connect screen has to tell apart, in the form it tells
// them apart by. A reason, not a sentence: rewording a message must not break
// a screen.
func TestConnectSavedReportsWhyItCouldNotStart(t *testing.T) {
	t.Parallel()

	t.Run("a server that is no longer remembered", func(t *testing.T) {
		t.Parallel()
		f := newServersApp(t)

		got, err := f.app.ConnectSaved("stranger.example:64738")
		if err != nil {
			t.Fatalf("ConnectSaved: %v", err)
		}
		if got.Reason != domain.SavedConnectUnknown {
			t.Fatalf("reason = %q, want %q", got.Reason, domain.SavedConnectUnknown)
		}
		if got.Address != "stranger.example:64738" || got.Message == "" {
			t.Errorf("result = %+v", got)
		}
	})

	// The one that must not be a dead end: the password is still there, the
	// store simply would not open. The form takes over with the address and
	// the nickname already filled in.
	t.Run("a locked credential store", func(t *testing.T) {
		t.Parallel()
		f := newServersApp(t)
		f.connected(t, "mumble.example:64738", "gul", "hunter2")
		f.storedPassword(t, "mumble.example:64738")
		f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
		f.keys.getErr = errors.New("keyring is locked")

		got, err := f.app.ConnectSaved("mumble.example:64738")
		if err != nil {
			t.Fatalf("ConnectSaved: %v", err)
		}
		if got.Reason != domain.SavedConnectPassword {
			t.Fatalf("reason = %q, want %q", got.Reason, domain.SavedConnectPassword)
		}
		if got.Address != "mumble.example:64738" || got.Username != "gul" {
			t.Errorf("the fallback form would start on %+v", got)
		}
		if got.Message == "" {
			t.Error("no explanation for the user")
		}
		if calls := f.ctrl.snapshot().connects; len(calls) != 1 {
			t.Fatalf("connects = %+v, want only the original", calls)
		}
	})

	t.Run("a connect that starts", func(t *testing.T) {
		t.Parallel()
		f := newServersApp(t)
		f.connected(t, "mumble.example:64738", "gul", "hunter2")
		f.storedPassword(t, "mumble.example:64738")

		got, err := f.app.ConnectSaved("mumble.example:64738")
		if err != nil {
			t.Fatalf("ConnectSaved: %v", err)
		}
		if got.Reason != domain.SavedConnectStarted || got.Message != "" {
			t.Fatalf("result = %+v, want a started connect", got)
		}
	})
}

// ---------------------------------------------------------------------------
// the document
// ---------------------------------------------------------------------------

// The one assertion that keeps the two stores apart: whatever else changes,
// the password must not be in the file.
func TestPasswordNeverReachesTheSettingsDocument(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	const password = "correct-horse-battery-staple"
	f.connected(t, "mumble.example:64738", "gul", password)

	data := f.document(t)
	if strings.Contains(string(data), password) {
		t.Fatalf("the settings document contains the password:\n%s", data)
	}
	if strings.Contains(strings.ToLower(string(data)), "password") {
		t.Fatalf("the settings document has a password-shaped field:\n%s", data)
	}

	var doc struct {
		Servers []map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(doc.Servers) != 1 {
		t.Fatalf("servers = %+v", doc.Servers)
	}
	for key := range doc.Servers[0] {
		switch key {
		case "address", "username", "last_used":
		default:
			t.Errorf("unexpected key %q in a remembered server", key)
		}
	}
}

// A core built without SetSecrets must behave like a machine with no store,
// not panic on a nil interface.
func TestCoreWithoutASecretStoreDegrades(t *testing.T) {
	t.Parallel()
	app := New(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), nil)
	app.SetController(&fakeController{})

	app.RememberServer("mumble.example:64738", "gul", "hunter2")
	got := app.Servers()
	if len(got) != 1 || got[0].HasPassword {
		t.Fatalf("servers = %+v, want one entry without a password", got)
	}
	if _, ok := app.PasswordFor("mumble.example:64738"); ok {
		t.Errorf("PasswordFor answered without a store")
	}
	if err := app.ForgetServer("mumble.example:64738"); err != nil {
		t.Errorf("ForgetServer: %v", err)
	}
}

// StateConnected is re-emitted whenever self changes channel, so the commit
// has to be once per accepted connect: a second run arrives with the password
// already spent and would replace the stored one with nothing.
func TestRepeatedConnectedStateKeepsTheStoredPassword(t *testing.T) {
	t.Parallel()
	f := newServersApp(t)

	f.connected(t, "voice.example:64738", "matvej", "hunter2")
	f.storedPassword(t, "voice.example:64738")

	// The same state again, the way a channel move republishes it. A second
	// commit would arrive with the password already spent and clear the stored
	// one, so the assertion is that it STAYS - checked over a window, because
	// a single early read would see the value the first write left behind.
	f.app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got, ok := f.keys.stored("voice.example:64738"); !ok || got != "hunter2" {
			t.Fatalf("the repeated connected state cleared the password: %q, %v", got, ok)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := len(f.app.Servers()); got != 1 {
		t.Fatalf("servers = %d, want the repeat to update one entry", got)
	}
}

// waitFor gives the off-callback credential write a moment to land: it runs on
// its own goroutine so the gumble read loop is never held by the keychain.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The road that proved itself is stored with the server, and offered back the
// next time that server is dialled - which is the whole point of storing it.
func TestRememberedRoadIsOfferedOnTheNextConnect(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)

	app.RememberServer("wss://murmur.example.test", "gul", "")
	app.HandleTransport("wss://murmur.example.test", "quic")

	if err := app.Connect("wss://murmur.example.test", "gul", "secret"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	ctrl.mu.Lock()
	preferred := append([]string(nil), ctrl.preferred...)
	ctrl.mu.Unlock()
	want := "wss://murmur.example.test=quic"
	if len(preferred) != 1 || preferred[0] != want {
		t.Fatalf("preferred = %v, want [%s]", preferred, want)
	}
}

// A server nothing is known about is dialled with no hint at all, which leaves
// the ordinary search in place.
func TestConnectWithoutAKnownRoadOffersNoHint(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)

	if err := app.Connect("wss://unknown.example.test", "gul", "secret"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	ctrl.mu.Lock()
	preferred := append([]string(nil), ctrl.preferred...)
	ctrl.mu.Unlock()
	if len(preferred) != 1 || preferred[0] != "wss://unknown.example.test=" {
		t.Fatalf("preferred = %v, want one empty hint", preferred)
	}
}
