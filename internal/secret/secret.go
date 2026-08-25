// Package secret keeps small secrets in the credential store the operating
// system already runs: the macOS keychain, the Windows Credential Manager, or
// a freedesktop Secret Service (gnome-keyring, kwallet) over D-Bus.
//
// Gul stores exactly one kind of secret here - the password of a remembered
// Mumble server, keyed by its address. It deliberately does not go into
// config.json: that file is a plain document the user is expected to read and
// copy between machines, and a password in it would be readable by every
// process running as the user, would survive in backups, and would land in a
// diagnostics archive the moment someone shared one.
//
// A machine without a usable store is not an error case. Available reports it,
// and the caller is expected to degrade: the server is remembered without its
// password and the user types it on the next connect. Every method still
// returns a clear error wrapping ErrUnavailable, so a caller that skips the
// check is told what happened rather than silently losing the secret.
//
// A Store is safe for concurrent use.
package secret

import (
	"errors"
	"fmt"
	"strings"
)

// Store is the credential store of one machine, scoped to one service name.
type Store interface {
	Set(account, secret string) error
	Get(account string) (secret string, found bool, err error)
	Delete(account string) error
	// Available reports whether this machine has a usable store. A
	// machine without one must still run: the caller saves the server
	// without its password and the user types it.
	Available() bool
}

// New returns the credential store of this machine, scoped to service.
//
// service names the application, not the item: every account lives under it,
// and it is what the user sees in Keychain Access or seahorse. An empty
// service name would put Gul's items in an unnamed bucket shared with whoever
// else made the same mistake, so it yields an unavailable store instead.
func New(service string) Store {
	service = strings.TrimSpace(service)
	if service == "" {
		return unavailable{reason: "no service name"}
	}
	return newStore(service)
}

var (
	// ErrUnavailable reports that this machine has no usable credential
	// store. Callers degrade rather than fail: see the package comment.
	ErrUnavailable = errors.New("secret: no usable credential store")

	// ErrInvalidAccount reports an account name no store can key on.
	ErrInvalidAccount = errors.New("secret: invalid account")

	// ErrInvalidSecret reports a value no store will accept.
	ErrInvalidSecret = errors.New("secret: invalid secret")
)

const (
	// maxAccountLen bounds the key. It is comfortably above the longest
	// server address the settings document accepts (config.MaxAddressLen),
	// and is kept here rather than imported so this package stays free of
	// the rest of the application.
	maxAccountLen = 512

	// maxSecretLen bounds the value. The binding constraint is Windows:
	// CredWriteW rejects a blob over CRED_MAX_CREDENTIAL_BLOB_SIZE, which is
	// 2560 bytes. Staying under it on every platform means a password that
	// round-trips on one machine round-trips on all of them.
	maxSecretLen = 2048
)

// accountKey is the one spelling of an account name every backend keys on.
//
// Trimming matches what the settings document does to an address, so the
// entry and its password cannot end up under two different keys. A NUL is
// rejected rather than trimmed: both the macOS and the Windows backend hand
// this string to a C API that terminates at the first NUL, so "host\x00evil"
// and "host" would silently become the same key.
func accountKey(account string) (string, error) {
	key := strings.TrimSpace(account)
	switch {
	case key == "":
		return "", fmt.Errorf("%w: empty", ErrInvalidAccount)
	case strings.ContainsRune(key, 0):
		return "", fmt.Errorf("%w: contains a NUL byte", ErrInvalidAccount)
	case len(key) > maxAccountLen:
		return "", fmt.Errorf("%w: longer than %d bytes", ErrInvalidAccount, maxAccountLen)
	}
	return key, nil
}

// checkSecret rejects values no store should be asked to hold. An empty
// secret is refused on purpose: "there is no password" is what Delete says,
// and letting Set mean it too would give the caller two spellings of one
// state and the backends two behaviours (a zero-length blob is legal on one
// platform and rejected on another).
func checkSecret(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: empty (use Delete)", ErrInvalidSecret)
	case len(value) > maxSecretLen:
		return fmt.Errorf("%w: longer than %d bytes", ErrInvalidSecret, maxSecretLen)
	}
	return nil
}

// unavailable is what a machine with no credential store gets: it reports
// itself unusable and says so on every call, so a caller that ignored
// Available reads why in the error instead of losing a password quietly.
type unavailable struct{ reason string }

func (u unavailable) Set(string, string) error { return u.err() }

func (u unavailable) Get(string) (string, bool, error) { return "", false, u.err() }

func (u unavailable) Delete(string) error { return u.err() }

func (u unavailable) Available() bool { return false }

func (u unavailable) err() error {
	if u.reason == "" {
		return ErrUnavailable
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, u.reason)
}

// Unavailable returns a Store for a machine that has none, naming why. It is
// what a caller uses in place of a nil Store, so nothing has to special-case
// the absence of one.
func Unavailable(reason string) Store { return unavailable{reason: reason} }
