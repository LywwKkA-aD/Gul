package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func storeFixture(t *testing.T) (string, OwnerKey) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "catalog.json")
	key, err := Bootstrap(path)
	if err != nil {
		t.Fatal("bootstrap failed")
	}
	return path, key
}

func TestExplicitBootstrapAndRestartKeepOwnerPrivate(t *testing.T) {
	path, key := storeFixture(t)
	if key.Format != "gul-member-key-v1" || !ValidID(key.ServerID) || !ValidID(key.MemberID) || !ValidCredential(key.Credential) {
		t.Fatal("invalid bootstrap key")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), key.Credential) {
		t.Fatal("plaintext credential persisted")
	}
	if _, err := Bootstrap(path); err == nil {
		t.Fatal("bootstrap replaced existing owner")
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal("open failed")
	}
	member, ok := store.Snapshot().Authenticate(key.Credential)
	if !ok || member.ID != key.MemberID || member.Role != "owner" {
		t.Fatal("owner identity lost")
	}
	if _, ok := store.Snapshot().Authenticate("wrong"); ok {
		t.Fatal("invalid credential authenticated")
	}
	if _, err := Open(path); err == nil {
		t.Fatal("second process obtained store lock")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal("restart failed")
	}
	defer restarted.Close()
	if restarted.Snapshot().ServerID != key.ServerID {
		t.Fatal("server identity changed")
	}
}

func TestAtomicUpdatesPreserveAcceptedSnapshotAndConcurrentWork(t *testing.T) {
	path, _ := storeFixture(t)
	store, _ := Open(path)
	defer store.Close()
	original := store.Snapshot()
	if err := store.Update(func(state *State) error { state.ServerID = "invalid"; return nil }); err == nil {
		t.Fatal("invalid state committed")
	}
	if store.Snapshot().ServerID != original.ServerID {
		t.Fatal("failed update leaked")
	}
	copy := store.Snapshot()
	copy.Channels[1].Name = "changed outside transaction"
	if store.Snapshot().Channels[1].Name == copy.Channels[1].Name {
		t.Fatal("mutable snapshot escaped")
	}
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			if err := store.Update(func(state *State) error { state.CatalogVersion++; return nil }); err != nil {
				t.Error("transaction failed")
			}
		})
	}
	group.Wait()
	if store.Snapshot().CatalogVersion != original.CatalogVersion+10 {
		t.Fatal("concurrent transaction lost")
	}
	data, _ := os.ReadFile(path)
	var disk State
	if json.Unmarshal(data, &disk) != nil || disk.CatalogVersion != store.Snapshot().CatalogVersion {
		t.Fatal("uncommitted response")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions changed")
	}
}

func TestStateRejectsSymlinksPublicPermissionsAndCorruptDocuments(t *testing.T) {
	path, _ := storeFixture(t)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("public state accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "linked.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("symlink state accepted")
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestCatalogLimitsProtectedChannelsAndCredentials(t *testing.T) {
	path, _ := storeFixture(t)
	store, _ := Open(path)
	defer store.Close()
	for _, mutate := range []func(*State){
		func(s *State) { s.Channels = s.Channels[1:] },
		func(s *State) { s.Channels[1].Access = "restricted" },
		func(s *State) { s.Channels[2].AllowedMemberIDs = []string{strings.Repeat("a", 32)} },
		func(s *State) { s.Members[0].Role = "member" },
		func(s *State) { s.Members[0].CredentialHash = "invalid" },
		func(s *State) { s.Channels[1].Name = "unsafe\x00title" },
	} {
		if store.Update(func(s *State) error { mutate(s); return nil }) == nil {
			t.Fatal("invalid catalogue mutation accepted")
		}
	}
}
