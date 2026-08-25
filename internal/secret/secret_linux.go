//go:build linux

package secret

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

// The freedesktop Secret Service API, as implemented by gnome-keyring-daemon
// and kwalletd. Everything here is one D-Bus name plus a handful of method
// names; there is no client library to pin.
const (
	secretService    = "org.freedesktop.secrets"
	servicePath      = dbus.ObjectPath("/org/freedesktop/secrets")
	collectionPath   = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	noPrompt         = dbus.ObjectPath("/")
	methodOpenSess   = "org.freedesktop.Secret.Service.OpenSession"
	methodSearch     = "org.freedesktop.Secret.Service.SearchItems"
	methodUnlock     = "org.freedesktop.Secret.Service.Unlock"
	methodCreateItem = "org.freedesktop.Secret.Collection.CreateItem"
	methodGetSecret  = "org.freedesktop.Secret.Item.GetSecret"
	methodDeleteItem = "org.freedesktop.Secret.Item.Delete"
	propLabel        = "org.freedesktop.Secret.Item.Label"
	propAttributes   = "org.freedesktop.Secret.Item.Attributes"

	// "plain" transports the secret unencrypted over the session bus socket.
	// That socket is a unix socket owned by this user, on this machine, and
	// the daemon on the other end already holds the password in the clear;
	// the DH ("dh-ietf1024-sha256-aes128-cbc-pkcs7") algorithm defends
	// against nothing in that picture and adds a key exchange this code
	// cannot verify on a machine without a session bus.
	plainAlgorithm = "plain"

	// contentType is what a reader of the item is told the bytes are.
	contentType = "text/plain; charset=utf8"
)

// dbusSecret is the Secret structure of the API: (oayays). Field order is the
// wire order.
type dbusSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// linuxStore talks to whichever Secret Service owns org.freedesktop.secrets.
//
// The collection is the default alias, which on a normal desktop session is
// the "login" keyring - the one the display manager unlocks with the user's
// password. Addressing it by alias rather than by the literal
// .../collection/login path is what makes this work on a session whose
// keyring is named something else.
type linuxStore struct {
	service string

	mu      sync.Mutex
	conn    *dbus.Conn
	session dbus.ObjectPath
}

func newStore(service string) Store { return &linuxStore{service: service} }

func (s *linuxStore) Set(account, value string) error {
	key, err := accountKey(account)
	if err != nil {
		return err
	}
	if err := checkSecret(value); err != nil {
		return err
	}
	conn, session, err := s.open()
	if err != nil {
		return err
	}

	props := map[string]dbus.Variant{
		propLabel:      dbus.MakeVariant(s.service + ": " + key),
		propAttributes: dbus.MakeVariant(s.attributes(key)),
	}
	secret := dbusSecret{
		Session:     session,
		Parameters:  []byte{},
		Value:       []byte(value),
		ContentType: contentType,
	}

	var item, prompt dbus.ObjectPath
	// replace=true: one account has one password, and a second item with the
	// same attributes would make Get's answer depend on search order.
	call := conn.Object(secretService, collectionPath).
		Call(methodCreateItem, 0, props, secret, true)
	if err := call.Store(&item, &prompt); err != nil {
		return fmt.Errorf("secret service: create item: %w", err)
	}
	if item == noPrompt || item == "" {
		return promptError("store the password", prompt)
	}
	return nil
}

func (s *linuxStore) Get(account string) (string, bool, error) {
	key, err := accountKey(account)
	if err != nil {
		return "", false, err
	}
	conn, session, err := s.open()
	if err != nil {
		return "", false, err
	}

	item, found, err := s.find(conn, key)
	if err != nil || !found {
		return "", false, err
	}

	var secret dbusSecret
	if err := conn.Object(secretService, item).Call(methodGetSecret, 0, session).Store(&secret); err != nil {
		return "", false, fmt.Errorf("secret service: read item: %w", err)
	}
	if len(secret.Value) == 0 {
		// An item with an empty payload is not a password; Set never writes
		// one, so it can only have come from elsewhere.
		return "", false, nil
	}
	return string(secret.Value), true, nil
}

func (s *linuxStore) Delete(account string) error {
	key, err := accountKey(account)
	if err != nil {
		return err
	}
	conn, _, err := s.open()
	if err != nil {
		return err
	}

	unlocked, locked, err := s.search(conn, key)
	if err != nil {
		return err
	}
	// Locked items are deleted too: leaving a password behind because the
	// keyring happened to be locked is exactly the failure "forget this
	// server" is supposed to prevent. Removing an item needs no unlock.
	for _, item := range append(append([]dbus.ObjectPath{}, unlocked...), locked...) {
		var prompt dbus.ObjectPath
		if err := conn.Object(secretService, item).Call(methodDeleteItem, 0).Store(&prompt); err != nil {
			return fmt.Errorf("secret service: delete item: %w", err)
		}
		if prompt != noPrompt && prompt != "" {
			return promptError("delete the password", prompt)
		}
	}
	return nil
}

// Available reports whether a Secret Service answers on the session bus.
//
// The probe is not cached: a Secret Service is D-Bus activatable, so one that
// was not running a moment ago may be running now, and the check is a single
// round trip on a unix socket. A session with no bus at all fails at the
// first step and never reaches it.
func (s *linuxStore) Available() bool {
	_, _, err := s.open()
	return err == nil
}

// open returns a live connection and session, reusing the ones it has. The
// session is bound to the connection, so a connection that went away
// invalidates it and both are re-established together.
func (s *linuxStore) open() (*dbus.Conn, dbus.ObjectPath, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil && s.conn.Connected() && s.session != "" {
		return s.conn, s.session, nil
	}
	s.conn, s.session = nil, ""

	// The shared session bus: godbus reconnects it if it dropped, and it is
	// never closed here because it is not ours to close.
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, "", fmt.Errorf("%w: session bus: %w", ErrUnavailable, err)
	}

	var (
		output  dbus.Variant
		session dbus.ObjectPath
	)
	call := conn.Object(secretService, servicePath).
		Call(methodOpenSess, 0, plainAlgorithm, dbus.MakeVariant(""))
	if err := call.Store(&output, &session); err != nil {
		// No owner for org.freedesktop.secrets, activation failed, or the
		// implementation refused a plain session. All three are "this
		// machine has no store", not a fault of the caller.
		return nil, "", fmt.Errorf("%w: secret service: %w", ErrUnavailable, err)
	}

	s.conn, s.session = conn, session
	return conn, session, nil
}

// find resolves one account to a single item, unlocking the keyring if that
// is all that stands in the way.
func (s *linuxStore) find(conn *dbus.Conn, key string) (dbus.ObjectPath, bool, error) {
	unlocked, locked, err := s.search(conn, key)
	if err != nil {
		return "", false, err
	}
	if len(unlocked) > 0 {
		return unlocked[0], true, nil
	}
	if len(locked) == 0 {
		return "", false, nil
	}

	var (
		opened []dbus.ObjectPath
		prompt dbus.ObjectPath
	)
	if err := conn.Object(secretService, servicePath).Call(methodUnlock, 0, locked).Store(&opened, &prompt); err != nil {
		return "", false, fmt.Errorf("secret service: unlock: %w", err)
	}
	if len(opened) == 0 {
		return "", false, promptError("read the password", prompt)
	}
	return opened[0], true, nil
}

func (s *linuxStore) search(conn *dbus.Conn, key string) (unlocked, locked []dbus.ObjectPath, err error) {
	call := conn.Object(secretService, servicePath).Call(methodSearch, 0, s.attributes(key))
	if err := call.Store(&unlocked, &locked); err != nil {
		return nil, nil, fmt.Errorf("secret service: search: %w", err)
	}
	return unlocked, locked, nil
}

// attributes are what an item is found by. They are searchable metadata, not
// secret: the service name and the account (a server address) are already in
// config.json, and only the value is confidential.
func (s *linuxStore) attributes(key string) map[string]string {
	return map[string]string{"service": s.service, "account": key}
}

// promptError reports an operation the service will only complete after the
// user answers a dialog of its own.
//
// Driving that dialog means calling Prompt.Prompt and waiting for its
// Completed signal, which is a code path that cannot be exercised anywhere in
// this project's CI or on the machine this was written on. Reporting it is
// honest; a caller that degrades on error behaves correctly either way - the
// server is remembered, the password is typed.
func promptError(what string, prompt dbus.ObjectPath) error {
	if prompt == noPrompt || prompt == "" {
		return errors.New("secret service: could not " + what)
	}
	return fmt.Errorf("secret service: could not %s without unlocking the keyring first", what)
}
