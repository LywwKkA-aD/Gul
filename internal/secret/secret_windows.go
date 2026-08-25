//go:build windows

package secret

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Credential Manager entry points. NewLazySystemDLL resolves out of the
// system directory, so the DLL-preloading hazard of a plain LoadLibrary does
// not reach them. x/sys/windows does not wrap the Cred* family, so the four
// calls and the CREDENTIALW layout are declared here.
var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric = 1 // CRED_TYPE_GENERIC
	// CRED_PERSIST_LOCAL_MACHINE: the credential outlives the logon session
	// but never leaves this machine. Roaming it (CRED_PERSIST_ENTERPRISE)
	// would push a Mumble password into a domain profile.
	credPersistLocalMachine = 2
)

// credentialW mirrors CREDENTIALW from wincred.h. Field order and types are
// load bearing: the struct is passed to and read back from the OS by address.
// Go's own alignment rules reproduce the C padding on both 386 and amd64,
// because every pointer field is naturally aligned in both layouts.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// windowsStore is the Windows Credential Manager, one generic credential per
// account, named "<service>:<account>".
type windowsStore struct {
	service string

	probeOnce sync.Once
	probeOK   bool
}

func newStore(service string) Store { return &windowsStore{service: service} }

func (s *windowsStore) Set(account, value string) error {
	key, err := accountKey(account)
	if err != nil {
		return err
	}
	if err := checkSecret(value); err != nil {
		return err
	}
	targetPtr, err := windows.UTF16PtrFromString(s.service + ":" + key)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAccount, err)
	}
	// UserName is what the Credential Manager UI shows next to the entry. It
	// is not part of the key: CredReadW matches on TargetName alone.
	userPtr, err := windows.UTF16PtrFromString(key)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAccount, err)
	}

	blob := []byte(value)
	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	// The syscall only pins the uintptr arguments; the pointers reached
	// through the struct have to be held across the call explicitly.
	runtime.KeepAlive(blob)
	runtime.KeepAlive(targetPtr)
	runtime.KeepAlive(userPtr)
	if ret == 0 {
		return fmt.Errorf("credential write: %w", callErr)
	}
	return nil
}

func (s *windowsStore) Get(account string) (string, bool, error) {
	target, err := s.target(account)
	if err != nil {
		return "", false, err
	}
	cred, found, err := read(target)
	if err != nil || !found {
		return "", false, err
	}
	defer func() { _, _, _ = procCredFree.Call(uintptr(unsafe.Pointer(cred))) }()

	if cred.CredentialBlob == nil || cred.CredentialBlobSize == 0 {
		// A credential with no blob is not a password. Set never writes one.
		return "", false, nil
	}
	// The blob is copied into Go memory before CredFree releases it.
	return string(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), true, nil
}

func (s *windowsStore) Delete(account string) error {
	target, err := s.target(account)
	if err != nil {
		return err
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAccount, err)
	}
	ret, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(targetPtr)), credTypeGeneric, 0)
	runtime.KeepAlive(targetPtr)
	if ret != 0 {
		return nil
	}
	if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		// Deleting what is not there is the state the caller asked for.
		return nil
	}
	return fmt.Errorf("credential delete: %w", callErr)
}

// Available probes once: the Credential Manager either answers on this
// machine or it does not, and the answer does not change while the client
// runs. A read of a target no account can produce is enough - ERROR_NOT_FOUND
// means the service replied.
func (s *windowsStore) Available() bool {
	s.probeOnce.Do(func() {
		if err := advapi32.Load(); err != nil {
			return
		}
		// target() rejects an empty account, so no Set can ever have written
		// this name; only the shape of the reply is being read here.
		_, _, err := read(s.service + ":")
		s.probeOK = err == nil
	})
	return s.probeOK
}

// target is the credential name of one account. The service prefix keeps
// Gul's credentials apart from every other application's in a namespace the
// OS does not partition for us.
func (s *windowsStore) target(account string) (string, error) {
	key, err := accountKey(account)
	if err != nil {
		return "", err
	}
	return s.service + ":" + key, nil
}

// read fetches one credential. A missing one is (nil, false, nil); the caller
// frees what it gets with CredFree.
func read(target string) (*credentialW, bool, error) {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrInvalidAccount, err)
	}
	var out *credentialW
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(targetPtr)
	if ret == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("credential read: %w", callErr)
	}
	if out == nil {
		return nil, false, nil
	}
	return out, true, nil
}
