package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapOwnerFilePrivateAndNeverOverwritesExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	state := filepath.Join(dir, "catalog.json")
	output := filepath.Join(dir, "owner.json")
	if BootstrapOwnerFile(state, output) != nil {
		t.Fatal("owner export failed")
	}
	bytes, err := os.ReadFile(output)
	var key OwnerKey
	if err != nil || json.Unmarshal(bytes, &key) != nil || key.Format != "gul-member-key-v1" {
		t.Fatal("invalid owner export")
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatal("personal key permissions")
	}
	store, err := Open(state)
	if err != nil {
		t.Fatal("owner state unavailable")
	}
	member, ok := store.Snapshot().Authenticate(key.Credential)
	_ = store.Close()
	if !ok || member.ID != key.MemberID || member.Role != "owner" {
		t.Fatal("export not bound to persisted owner")
	}
	if BootstrapOwnerFile(state, filepath.Join(dir, "other.json")) == nil {
		t.Fatal("existing identity overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, "other.json")); !os.IsNotExist(err) {
		t.Fatal("failed bootstrap left misleading key file")
	}
}
func TestBootstrapOwnerFileExportFailureDoesNotCreateUnrecoverableOwner(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	state := filepath.Join(dir, "catalog.json")
	output := filepath.Join(dir, "owner.json")
	_ = os.WriteFile(output, []byte("reserved"), 0600)
	if BootstrapOwnerFile(state, output) == nil {
		t.Fatal("existing export overwritten")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("owner created without recoverable export")
	}
}
