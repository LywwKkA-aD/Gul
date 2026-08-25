package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(unix int64) time.Time { return time.Unix(unix, 0) }

func addresses(list []Server) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Address)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRememberServerPutsTheNewestFirst(t *testing.T) {
	t.Parallel()

	var list []Server
	list = RememberServer(list, "one.example:64738", "gul", at(100))
	list = RememberServer(list, "two.example:64738", "gul", at(200))
	list = RememberServer(list, "wss://three.example/mumble", "gul", at(300))

	want := []string{"wss://three.example/mumble", "two.example:64738", "one.example:64738"}
	if got := addresses(list); !equalStrings(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// A repeat connect refreshes the row it already has instead of adding a
// second one for the same server.
func TestRememberServerDeduplicatesByAddress(t *testing.T) {
	t.Parallel()

	var list []Server
	list = RememberServer(list, "one.example:64738", "old-nick", at(100))
	list = RememberServer(list, "two.example:64738", "gul", at(200))
	list = RememberServer(list, " one.example:64738 ", "new-nick", at(300))

	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2 entries", list)
	}
	if list[0].Address != "one.example:64738" {
		t.Errorf("refreshed entry is not first: %v", addresses(list))
	}
	if list[0].Username != "new-nick" {
		t.Errorf("username = %q, want the one just used", list[0].Username)
	}
	if list[0].LastUsed != 300 {
		t.Errorf("last_used = %d, want 300", list[0].LastUsed)
	}
}

func TestRememberServerIsCapped(t *testing.T) {
	t.Parallel()

	var list []Server
	for i := range MaxServers + 4 {
		list = RememberServer(list, "host"+string(rune('a'+i))+".example:64738", "gul", at(int64(100+i)))
	}
	if len(list) != MaxServers {
		t.Fatalf("len = %d, want %d", len(list), MaxServers)
	}
	// The oldest fall off, not the newest.
	if list[0].Address != "hostl.example:64738" {
		t.Errorf("newest = %q", list[0].Address)
	}
	for _, s := range list {
		if s.Address == "hosta.example:64738" {
			t.Errorf("the oldest entry survived the cap")
		}
	}
}

// last_used only orders the picker. A clock that went backwards must not bury
// the server the user is connected to right now.
func TestRememberServerSurvivesABackwardsClock(t *testing.T) {
	t.Parallel()

	var list []Server
	list = RememberServer(list, "old.example:64738", "gul", at(5000))
	list = RememberServer(list, "now.example:64738", "gul", at(100))

	if list[0].Address != "now.example:64738" {
		t.Fatalf("order = %v, want the just-used server first", addresses(list))
	}
}

func TestRememberServerIgnoresWhatCannotBeDialled(t *testing.T) {
	t.Parallel()

	base := RememberServer(nil, "one.example:64738", "gul", at(100))
	for name, tc := range map[string]struct{ address, username string }{
		"no address":         {"", "gul"},
		"blank address":      {"   ", "gul"},
		"no username":        {"two.example:64738", ""},
		"address too long":   {strings.Repeat("a", MaxAddressLen+1), "gul"},
		"username too long":  {"two.example:64738", strings.Repeat("u", MaxUsernameLen+1)},
		"NUL in the address": {"two.example:64738\x00evil", "gul"},
	} {
		t.Run(name, func(t *testing.T) {
			got := RememberServer(base, tc.address, tc.username, at(200))
			if len(got) != 1 || got[0].Address != "one.example:64738" {
				t.Fatalf("list = %+v, want the untouched original", got)
			}
		})
	}
}

func TestForgetServerRemovesOnlyTheOneNamed(t *testing.T) {
	t.Parallel()

	var list []Server
	list = RememberServer(list, "one.example:64738", "gul", at(100))
	list = RememberServer(list, "two.example:64738", "gul", at(200))

	list = ForgetServer(list, " two.example:64738 ")
	if got := addresses(list); !equalStrings(got, []string{"one.example:64738"}) {
		t.Fatalf("after forget = %v", got)
	}

	// Forgetting what is not there is the state the caller asked for.
	list = ForgetServer(list, "never.stored:64738")
	if got := addresses(list); !equalStrings(got, []string{"one.example:64738"}) {
		t.Fatalf("forgetting a stranger changed the list: %v", got)
	}
}

// Config is a value that callers hold snapshots of, so sanitizing must build a
// new slice rather than filter through the one it was given.
func TestSanitizeServersDoesNotWriteThroughItsInput(t *testing.T) {
	t.Parallel()

	input := []Server{
		{Address: "keep.example:64738", Username: "gul", LastUsed: 100},
		{Address: "", Username: "gul", LastUsed: 200},
		{Address: "later.example:64738", Username: "gul", LastUsed: 300},
	}
	out := sanitizeServers(input)

	if len(out) != 2 || out[0].Address != "later.example:64738" {
		t.Fatalf("sanitized = %+v", out)
	}
	if input[0].Address != "keep.example:64738" || input[1].Address != "" || input[2].Address != "later.example:64738" {
		t.Fatalf("input was rewritten: %+v", input)
	}
}

// A hand-edited or truncated entry costs its own row, not the whole document.
func TestLoadDropsUnusableServerEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	doc := map[string]any{
		"version": SchemaVersion,
		"servers": []any{
			map[string]any{"address": "good.example:64738", "username": "gul", "last_used": 300},
			map[string]any{"address": "", "username": "gul", "last_used": 200},
			map[string]any{"address": "nonick.example:64738", "username": "  ", "last_used": 100},
			map[string]any{"address": strings.Repeat("a", MaxAddressLen+1), "username": "gul", "last_used": 400},
			map[string]any{"address": "negative.example:64738", "username": "gul", "last_used": -5},
			map[string]any{"address": "good.example:64738", "username": "dup", "last_used": 50},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := addresses(cfg.Servers); !equalStrings(got, []string{"good.example:64738", "negative.example:64738"}) {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
	if cfg.Servers[0].Username != "gul" {
		t.Errorf("the duplicate row won: %+v", cfg.Servers[0])
	}
	if cfg.Servers[1].LastUsed != 0 {
		t.Errorf("negative last_used = %d, want clamped to 0", cfg.Servers[1].LastUsed)
	}
}

// An older v1 document has no "servers" key at all. Its absence means exactly
// what the default means, which is why this field needed no schema bump.
func TestLoadWithoutServersKeyGivesAnEmptyList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	doc := `{"version":1,"connection":{"last_address":"old.example:64738","last_username":"gul"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("servers = %+v, want empty", cfg.Servers)
	}
	// The last-used pair keeps working: the picker is additive.
	if cfg.Connection.LastAddress != "old.example:64738" {
		t.Errorf("last_address = %q", cfg.Connection.LastAddress)
	}
}

func TestSaveLoadServersRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := Defaults()
	cfg.Servers = RememberServer(cfg.Servers, "one.example:64738", "gul", at(100))
	cfg.Servers = RememberServer(cfg.Servers, "wss://two.example/mumble", "второй", at(200))

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("servers = %+v", got.Servers)
	}
	if got.Servers[0] != (Server{Address: "wss://two.example/mumble", Username: "второй", LastUsed: 200}) {
		t.Errorf("first = %+v", got.Servers[0])
	}
	if got.Servers[1] != (Server{Address: "one.example:64738", Username: "gul", LastUsed: 100}) {
		t.Errorf("second = %+v", got.Servers[1])
	}
}

// The written document carries the picker and nothing that could be a
// password. This is the assertion that keeps the two stores apart.
func TestSavedDocumentHasNoPasswordField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := Defaults()
	cfg.Servers = RememberServer(cfg.Servers, "one.example:64738", "gul", at(100))
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "password") {
		t.Fatalf("the settings document mentions a password:\n%s", data)
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
