package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateWriteFailureAndClosedStoreNeverCommit(t *testing.T) {
	path, _ := storeFixture(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	original := store.Snapshot().CatalogVersion
	dir := filepath.Dir(path)
	moved := dir + "-moved"
	if os.Rename(dir, moved) != nil {
		t.Fatal("fixture move failed")
	}
	defer os.Rename(moved, dir)
	if store.Update(func(s *State) error { s.CatalogVersion++; return nil }) == nil || store.Snapshot().CatalogVersion != original {
		t.Fatal("failed durable write published state")
	}
	_ = store.Close()
	if store.Update(func(s *State) error { return nil }) == nil {
		t.Fatal("closed store accepted commit")
	}
	if store.Close() != nil {
		t.Fatal("close not idempotent")
	}
}
func TestCatalogIdentityACLAndBoundValidation(t *testing.T) {
	path, key := storeFixture(t)
	store, _ := Open(path)
	defer store.Close()
	base := store.Snapshot()
	member := Member{ID: RandomID(), Name: "member", Role: "member", CredentialHash: Digest("member", base.ServerID, RandomCredential()), AuthVersion: 1}
	base.Members = append(base.Members, member)
	base.Channels[2].Access = "restricted"
	base.Channels[2].AllowedMemberIDs = []string{member.ID}
	if base.Validate() != nil || !base.CanJoin(key.MemberID, 2) || !base.CanJoin(member.ID, 2) || base.CanJoin("", 2) || base.CanJoin(member.ID, 999) {
		t.Fatal("valid whitelist semantics failed")
	}
	base.Members[1].Revoked = true
	if base.CanJoin(member.ID, 2) || base.CanJoin(member.ID, 1) {
		t.Fatal("revoked member admitted")
	}
	base.Members[1].Revoked = false
	for _, change := range []func(*State){
		func(s *State) { s.SchemaVersion = 2 }, func(s *State) { s.CatalogVersion = MaxVersion + 1 }, func(s *State) { s.Members[0].Revoked = true }, func(s *State) { s.Members[1].ID = s.Members[0].ID }, func(s *State) { s.Members[1].CredentialHash = s.Members[0].CredentialHash }, func(s *State) { s.Members[1].AuthVersion = 0 }, func(s *State) { s.Members[1].Name = " bad" }, func(s *State) { s.Channels[2].AllowedMemberIDs = []string{member.ID, member.ID} }, func(s *State) { s.Channels[2].Access = "invalid" }, func(s *State) { s.Channels[2].Position = -1 }, func(s *State) { s.Channels[2].Version = 0 }, func(s *State) { s.Channels[2].ID = 1 }, func(s *State) { s.NextChannelID = 3 }, func(s *State) { s.NextChannelID = 0x80000001 }, func(s *State) { s.Invites = []Invite{{Digest: "invalid", ExpiresAt: 1}} }, func(s *State) {
			s.Invites = []Invite{{Digest: Digest("invite", s.ServerID, "fixture"), ExpiresAt: 1, ConsumedBy: RandomID()}}
		}, func(s *State) { s.Invites = []Invite{{Digest: Digest("invite", s.ServerID, "fixture"), ExpiresAt: 0}} }, func(s *State) { s.Invites = make([]Invite, MaxInvites+1) }, func(s *State) { s.Members = make([]Member, MaxMembers+1) }, func(s *State) { s.Channels = make([]Channel, MaxChannels+1) },
	} {
		s := base.clone()
		change(&s)
		if s.Validate() == nil {
			t.Fatal("invalid private schema accepted")
		}
	}
	if ValidID(strings.ToUpper(key.MemberID)) || ValidCredential(key.Credential+"=") || ValidName(strings.Repeat("x", 65)) || ValidName(string([]byte{0xff})) {
		t.Fatal("noncanonical identifier/name accepted")
	}
}
func TestCatalogOpenRejectsMissingOversizedAndUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	for _, path := range []string{"relative.json", filepath.Join(dir, "missing.json")} {
		if _, err := Open(path); err == nil {
			t.Fatal("unsafe/missing state opened")
		}
	}
	path := filepath.Join(dir, "large.json")
	_ = os.WriteFile(path, make([]byte, maxDocument+1), 0600)
	if _, err := Open(path); err == nil {
		t.Fatal("oversized state opened")
	}
	_ = os.Chmod(dir, 0755)
	if _, err := Bootstrap(filepath.Join(dir, "new.json")); err == nil {
		t.Fatal("public parent accepted")
	}
}

func TestPostRenameDurabilityFailureMakesStoreUnhealthyUntilRestart(t *testing.T) {
	path, _ := storeFixture(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal("open failed")
	}
	original := store.Snapshot().CatalogVersion
	store.persist = func(path string, next State) error {
		if writeAtomic(path, next) != nil {
			t.Fatal("fixture commit failed")
		}
		return errCommitUncertain
	}
	if store.Update(func(s *State) error { s.Channels[2].Access = "restricted"; s.CatalogVersion++; return nil }) == nil || store.Healthy() {
		t.Fatal("uncertain durability was acknowledged as healthy")
	}
	if store.Update(func(s *State) error { s.CatalogVersion++; return nil }) == nil {
		t.Fatal("split snapshot accepted another transaction")
	}
	if store.Snapshot().CatalogVersion != original {
		t.Fatal("failed commit claimed durability")
	}
	_ = store.Close()
	restarted, err := Open(path)
	if err != nil {
		t.Fatal("durable restart unavailable")
	}
	defer restarted.Close()
	if !restarted.Healthy() || restarted.Snapshot().CatalogVersion != original+1 || restarted.Snapshot().CanJoin("", 2) {
		t.Fatal("restart did not reconcile committed ACL")
	}
}
