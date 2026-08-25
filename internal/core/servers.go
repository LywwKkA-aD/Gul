package core

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/secret"
)

// Remembered servers (PLAN.md §6, the connect screen). The list of servers
// lives in the settings document; their passwords live in the operating
// system's credential store, keyed by address. Nothing here ever puts a
// password in the document, and nothing puts one on the wire to the UI: a
// connect from the picker is made in Go, so the secret never crosses into the
// webview.
//
// A machine with no credential store is a supported machine. It remembers
// servers without their passwords, and the user types one per connect.

// SetSecrets injects the credential store. Call once, before the UI runs. A
// core without one behaves exactly like a machine without a store, which is
// what keeps every test that does not ask for it off the real keychain.
func (a *App) SetSecrets(store secret.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.secrets = store
}

// Servers returns the remembered servers, newest first, for the connect
// picker. HasPassword says whether one can be dialled without asking; the
// password itself stays here.
func (a *App) Servers() []domain.SavedServer {
	a.mu.Lock()
	// The slice header is copied under the lock and read outside it. Safe
	// because config.Sanitized never writes through a backing array it was
	// given: every mutation replaces the slice.
	stored := a.cfg.Servers
	a.mu.Unlock()

	store := a.secretStore()
	available := store.Available()

	out := make([]domain.SavedServer, 0, len(stored))
	for _, s := range stored {
		entry := domain.SavedServer{Address: s.Address, Username: s.Username}
		if available {
			_, found, err := store.Get(s.Address)
			if err != nil {
				// The address stays out of the record: gul.log travels in
				// diagnostics archives the user shares (PLAN.md §10.7).
				a.log.Warn("stored password could not be read", "error", err)
			}
			entry.HasPassword = found
		}
		out = append(out, entry)
	}
	return out
}

// RememberServer records a server the user has just connected to. It is
// called on a SUCCESSFUL connect only, so a wrong password is never stored.
//
// The entry is written whatever happens to the password: a machine with no
// credential store, or a store that refused, costs the password and not the
// server. Connecting with no password at all removes one that was stored -
// the picker must not offer a password the user has stopped using.
func (a *App) RememberServer(address, username, password string) {
	address, username = strings.TrimSpace(address), strings.TrimSpace(username)
	if address == "" || username == "" {
		return
	}

	// The list is ours and cheap: in memory now, on disk when the debounce
	// window closes. The password is the operating system's and may take its
	// time - or put a dialog on screen - so it is written off whatever
	// goroutine asked, and callers on a hot path (the gumble read loop, which
	// must never block) are not held by it.
	a.updateSettings(func(c *config.Config) {
		c.Servers = config.RememberServer(c.Servers, address, username, time.Now())
	})
	go a.rememberPassword(address, password)
}

// rememberPassword writes, or clears, the password of one remembered server.
func (a *App) rememberPassword(address, password string) {
	store := a.secretStore()
	if !store.Available() {
		if password != "" {
			a.log.Warn("no credential store on this machine, the server is remembered without its password")
		}
		return
	}
	if password == "" {
		if err := store.Delete(address); err != nil {
			a.log.Warn("stale password could not be removed", "error", err)
		}
		return
	}
	if err := store.Set(address, password); err != nil {
		a.log.Warn("password not stored, the server is remembered without it", "error", err)
	}
}

// ForgetServer drops a server from the picker and its password from the
// credential store.
//
// The entry goes first and unconditionally: "forget this server" must not
// leave a row on screen because the keyring was locked. A password that could
// not be removed is reported, because a leftover secret is exactly what the
// user asked to be rid of.
func (a *App) ForgetServer(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}

	a.updateSettings(func(c *config.Config) {
		c.Servers = config.ForgetServer(c.Servers, address)
	})

	store := a.secretStore()
	if !store.Available() {
		// Nothing could have been stored on a machine with no store.
		return nil
	}
	if err := store.Delete(address); err != nil {
		a.log.Warn("password not removed for a forgotten server", "error", err)
		return fmt.Errorf("the server was forgotten, but its password could not be removed: %w", err)
	}
	return nil
}

// PasswordFor returns the stored password of a remembered server, so a
// connect from the picker can dial without asking again. A store that is not
// available, or a lookup that failed, is simply "no password": the caller
// falls back to asking, which always works.
func (a *App) PasswordFor(address string) (string, bool) {
	value, found, err := a.passwordFor(strings.TrimSpace(address))
	if err != nil {
		return "", false
	}
	return value, found
}

// passwordFor is PasswordFor with the difference that matters internally:
// "there is no password" and "the password could not be read" are not the
// same answer. A missing store is the first, not the second - a machine
// without one has nothing to fail at.
func (a *App) passwordFor(address string) (string, bool, error) {
	if address == "" {
		return "", false, nil
	}
	store := a.secretStore()
	if !store.Available() {
		return "", false, nil
	}
	value, found, err := store.Get(address)
	if err != nil {
		a.log.Warn("stored password could not be read", "error", err)
		return "", false, err
	}
	return value, found, nil
}

// Russian, ready to render: the two ways a click on the picker can fail are
// the user's problem to solve, so they carry the sentence that says how. The
// UI switches on the reason, never on this text.
const (
	unknownServerMessage = "Этот сервер больше не в списке. Введите адрес вручную."
	lockedKeyringMessage = "Не удалось прочитать сохранённый пароль: хранилище ключей " +
		"недоступно или заблокировано. Введите пароль вручную — он не потерян."
)

// ConnectSaved dials a remembered server with the nickname it was last used
// with and, when one is stored, its password. The lookup happens in Go rather
// than in the UI: a click on the picker must not send a secret through the
// webview and back.
//
// The two ways this can fail are told apart by their reason, not by their
// text, because they need different screens: an address that is no longer
// remembered is a stale list, while a password that could not be read is a
// locked keyring and has to fall back to the manual form. Anything else - an
// address the validator refuses, no controller yet - comes back as an error,
// which is what the generic failure path already renders.
func (a *App) ConnectSaved(address string) (domain.SavedConnect, error) {
	entry, err := a.connectSaved(address)
	switch {
	case err == nil:
		return domain.SavedConnect{
			Address:  entry.Address,
			Username: entry.Username,
		}, nil
	case errors.Is(err, ErrUnknownServer):
		return domain.SavedConnect{
			Reason:  domain.SavedConnectUnknown,
			Address: strings.TrimSpace(address),
			Message: unknownServerMessage,
		}, nil
	case errors.Is(err, ErrPasswordUnreadable):
		return domain.SavedConnect{
			Reason:   domain.SavedConnectPassword,
			Address:  entry.Address,
			Username: entry.Username,
			Message:  lockedKeyringMessage,
		}, nil
	default:
		return domain.SavedConnect{}, err
	}
}

// connectSaved is ConnectSaved in the vocabulary of the rest of core: typed
// errors. It returns the entry it worked on even when it failed, so the
// fallback form can start on the nickname the user connected with last time.
//
// A password lookup that failed stops the connect instead of dialling without
// one. Dialling would be indistinguishable from the user choosing to have no
// password, and RememberServer would then drop the stored one on the next
// successful connect - a locked keyring would quietly destroy the password it
// merely could not read.
func (a *App) connectSaved(address string) (config.Server, error) {
	address = strings.TrimSpace(address)
	entry, ok := a.savedServer(address)
	if !ok {
		return config.Server{}, fmt.Errorf("%w: %q", ErrUnknownServer, address)
	}
	password, _, err := a.passwordFor(entry.Address)
	if err != nil {
		return entry, fmt.Errorf("%w: %w", ErrPasswordUnreadable, err)
	}
	return entry, a.Connect(entry.Address, entry.Username, password)
}

// savedServer looks one entry up by its exact (trimmed) address - the same
// key the credential store uses.
func (a *App) savedServer(address string) (config.Server, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.cfg.Servers {
		if s.Address == address {
			return s, true
		}
	}
	return config.Server{}, false
}

// secretStore is the injected store, or a stand-in that reports itself
// unavailable, so no caller has to special-case a core without one.
func (a *App) secretStore() secret.Store {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.secrets == nil {
		return secret.Unavailable("no credential store was configured")
	}
	return a.secrets
}

// dropPendingPassword forgets the password of an attempt that will not be
// committed. Called when the session ends, so a password the server refused
// does not sit in memory until the next connect replaces it.
func (a *App) dropPendingPassword() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingPassword = ""
}
