package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const maxDocument = 512 * 1024

var unavailable = errors.New("private catalogue storage unavailable")
var errCommitUncertain = errors.New("catalogue commit durability uncertain")

type Store struct {
	mu        sync.Mutex
	path      string
	lock      *os.File
	state     State
	closed    bool
	unhealthy bool
	persist   func(string, State) error
}

func lockPath(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." {
		return nil, unavailable
	}
	directory, err := os.Lstat(filepath.Dir(path))
	if err != nil || !directory.IsDir() || directory.Mode().Perm() != 0700 {
		return nil, unavailable
	}
	return acquireFileLock(path + ".lock")
}
func Open(path string) (*Store, error) {
	lock, err := lockPath(path)
	if err != nil {
		return nil, unavailable
	}
	release := func() { _ = lock.Close() }
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maxDocument {
		release()
		return nil, unavailable
	}
	file, err := os.Open(path)
	if err != nil {
		release()
		return nil, unavailable
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		release()
		return nil, unavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDocument+1))
	_ = file.Close()
	if err != nil || len(data) > maxDocument {
		release()
		return nil, unavailable
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || state.Validate() != nil {
		release()
		return nil, unavailable
	}
	return &Store{path: path, lock: lock, state: state, persist: writeAtomic}, nil
}
func (s *Store) Snapshot() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state.clone() }
func (s *Store) Update(change func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.unhealthy {
		return unavailable
	}
	next := s.state.clone()
	if err := change(&next); err != nil {
		return err
	}
	if next.Validate() != nil {
		return unavailable
	}
	if err := s.persist(s.path, next); err != nil {
		if errors.Is(err, errCommitUncertain) {
			s.unhealthy = true
		}
		return unavailable
	}
	s.state = next
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Close()
}
func writeAtomic(path string, state State) error {
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxDocument {
		return unavailable
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gul-catalog-*")
	if err != nil {
		return unavailable
	}
	name := file.Name()
	defer os.Remove(name)
	if file.Chmod(0600) != nil {
		_ = file.Close()
		return unavailable
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return unavailable
	}
	if file.Sync() != nil {
		_ = file.Close()
		return unavailable
	}
	if file.Close() != nil || os.Rename(name, path) != nil {
		return unavailable
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errCommitUncertain
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return errCommitUncertain
	}
	return nil
}

// Bootstrap is an explicit operator action. Opening an empty server never assigns ownership.
func Bootstrap(path string) (OwnerKey, error) {
	key := OwnerKey{Format: "gul-member-key-v1", ServerID: RandomID(), MemberID: RandomID(), Credential: RandomCredential()}
	lock, err := lockPath(path)
	if err != nil {
		return OwnerKey{}, unavailable
	}
	defer lock.Close()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return OwnerKey{}, unavailable
	}
	state := initialState(key)
	if state.Validate() != nil || writeAtomic(path, state) != nil {
		return OwnerKey{}, unavailable
	}
	return key, nil
}

func initialState(key OwnerKey) State {
	return State{SchemaVersion: 1, ServerID: key.ServerID, CatalogVersion: 1, NextChannelID: 4,
		Members:  []Member{{ID: key.MemberID, Name: "Владелец", Role: "owner", CredentialHash: Digest("member", key.ServerID, key.Credential), AuthVersion: 1}},
		Channels: []Channel{{ID: 0, Name: "Gul LiveKit", Version: 1, Access: "open", AllowedMemberIDs: []string{}}, {ID: 1, Name: "Общая", Version: 1, Access: "open", AllowedMemberIDs: []string{}}, {ID: 2, Name: "Игра", Position: 1, Version: 1, Access: "open", AllowedMemberIDs: []string{}}, {ID: 3, Name: "AFK", Position: 2, Version: 1, Access: "open", AllowedMemberIDs: []string{}}}, Invites: []Invite{}}
}

// Healthy is required before issuing authority from the cached snapshot. An
// uncertain post-rename fsync disables further authority until a fresh Open.
func (s *Store) Healthy() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.closed && !s.unhealthy }
