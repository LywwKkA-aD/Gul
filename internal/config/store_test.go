package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The settings document is the one piece of state that outlives the process,
// so every way it can be wrong on disk has to end in a running application:
// defaults in memory, the user's file never destroyed silently.

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("seed %s: %v", FileName, err)
	}
}

func read(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read %s: %v", FileName, err)
	}
	return string(data)
}

// document reads the file back as a generic document, which is how the tests
// look at fields no struct claims.
func document(t *testing.T, dir string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, dir)), &doc); err != nil {
		t.Fatalf("decode %s: %v", FileName, err)
	}
	return doc
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Defaults()
	if cfg.Version != want.Version || cfg.Gate != want.Gate || cfg.Audio != want.Audio || cfg.Connection != want.Connection {
		t.Fatalf("Load = %+v, want %+v", cfg, want)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Load created a file; nothing was asked to be written")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := Defaults()
	cfg.Connection = Connection{LastAddress: "wss://murmur.example.test/mumble", LastUsername: "gul"}
	cfg.Audio = Audio{CaptureID: "a1b2", PlaybackID: "c3d4"}
	cfg.Gate = Gate{Mode: GateModePTT, OpenThreshold: 0.75, HangoverMs: 500, PTTKey: "KeyF", GlobalPTT: true}

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Connection != cfg.Connection || got.Audio != cfg.Audio || got.Gate != cfg.Gate {
		t.Fatalf("round trip = %+v, want %+v", got, cfg)
	}
	if got.Version != SchemaVersion {
		t.Fatalf("version = %d, want %d", got.Version, SchemaVersion)
	}
}

// A build that knows more fields of this schema version than we do must not
// lose them just because an older client wrote the file once.
func TestUnknownFieldsSurviveARoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `{
	  "version": 1,
	  "connection": {"last_address": "host:64738", "last_username": "gul", "last_channel": 7},
	  "gate": {"mode": "ptt", "ptt_key": "KeyF"},
	  "appearance": {"accent": "#2F52DE"}
	}`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Audio.CaptureID = "beef"
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	doc := document(t, dir)
	appearance, ok := doc["appearance"].(map[string]any)
	if !ok || appearance["accent"] != "#2F52DE" {
		t.Errorf("unknown top-level section lost: %v", doc["appearance"])
	}
	connection, ok := doc["connection"].(map[string]any)
	if !ok || connection["last_channel"] != float64(7) {
		t.Errorf("unknown nested field lost: %v", doc["connection"])
	}
	if connection["last_username"] != "gul" || doc["version"] != float64(SchemaVersion) {
		t.Errorf("known fields not written back: %v", doc)
	}
}

// A document that is not JSON at all cannot be merged into, and re-reading it
// on every start would keep the user on defaults forever without a trace.
func TestDamagedDocumentIsQuarantined(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "{not json at all")

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("Load reported no problem for a damaged document")
	}
	if cfg.Gate != Defaults().Gate {
		t.Errorf("Load = %+v, want defaults", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the damaged document is still in place")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	kept := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), FileName+".broken-") {
			kept = filepath.Join(dir, e.Name())
		}
	}
	if kept == "" {
		t.Fatalf("nothing was kept aside: %v", entries)
	}
	body, err := os.ReadFile(kept)
	if err != nil || string(body) != "{not json at all" {
		t.Fatalf("quarantined copy = %q, %v", body, err)
	}
}

// A JSON array parses, but not into a document: same treatment.
func TestNonObjectDocumentIsQuarantined(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `["settings"]`)

	if _, err := Load(dir); err == nil {
		t.Fatal("Load reported no problem for a document that is not an object")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unusable document is still in place")
	}
}

func TestNewerVersionIsRefusedAndKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `{"version": 99, "gate": {"mode": "ptt", "ptt_key": "KeyQ"}}`
	write(t, dir, body)

	cfg, err := Load(dir)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want %v", err, ErrUnsupportedVersion)
	}
	if cfg.Gate != Defaults().Gate {
		t.Errorf("Load = %+v, want defaults", cfg.Gate)
	}
	if got := read(t, dir); got != body {
		t.Errorf("the file was touched:\n got %s\nwant %s", got, body)
	}
}

func TestUnreadableVersionIsRefusedAndKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `{"version": "one", "gate": {"mode": "ptt"}}`
	write(t, dir, body)

	cfg, err := Load(dir)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want %v", err, ErrUnsupportedVersion)
	}
	if cfg.Gate.Mode != GateModeVAD {
		t.Errorf("gate = %+v, want defaults", cfg.Gate)
	}
	if got := read(t, dir); got != body {
		t.Errorf("the file was touched: %s", got)
	}
}

// A document without a version field is version 0: what it carries still
// means the same thing, and what it lacks comes from the defaults.
func TestVersionlessDocumentMigratesToCurrent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `{"connection": {"last_username": "gul"}, "gate": {"mode": "ptt"}}`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", cfg.Version, SchemaVersion)
	}
	if cfg.Connection.LastUsername != "gul" || cfg.Gate.Mode != GateModePTT {
		t.Errorf("carried fields lost: %+v", cfg)
	}
	if cfg.Gate.OpenThreshold != DefaultOpenThreshold || cfg.Gate.PTTKey != DefaultPTTKey {
		t.Errorf("missing fields not filled from defaults: %+v", cfg.Gate)
	}

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if doc := document(t, dir); doc["version"] != float64(SchemaVersion) {
		t.Errorf("saved version = %v, want %d", doc["version"], SchemaVersion)
	}
}

func TestLoadClampsHandEditedValues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `{
	  "version": 1,
	  "connection": {"last_address": "  host:64738  ", "last_username": "  gul  "},
	  "gate": {"mode": "shout", "open_threshold": 7.5, "hangover_ms": 999999, "ptt_key": "Прбл"}
	}`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Gate{Mode: GateModeVAD, OpenThreshold: 1, HangoverMs: MaxHangoverMs, PTTKey: DefaultPTTKey}
	if cfg.Gate != want {
		t.Errorf("gate = %+v, want %+v", cfg.Gate, want)
	}
	if cfg.Connection != (Connection{LastAddress: "host:64738", LastUsername: "gul"}) {
		t.Errorf("connection = %+v, want the trimmed pair", cfg.Connection)
	}
}

func TestLoadDropsAnImpossibleRememberedConnection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Connection = Connection{
		LastAddress:  strings.Repeat("h", MaxAddressLen+1),
		LastUsername: strings.Repeat("n", MaxUsernameLen+1),
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Connection != (Connection{}) {
		t.Fatalf("connection = %+v, want it forgotten rather than truncated", got.Connection)
	}
}

// A field of the wrong type costs that field, not the document around it.
func TestDamagedFieldKeepsTheRestOfTheDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `{
	  "version": 1,
	  "connection": {"last_address": "host:64738", "last_username": "gul"},
	  "gate": {"mode": "ptt", "open_threshold": "loud", "ptt_key": "KeyF"}
	}`)

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("Load reported no problem for a field of the wrong type")
	}
	if cfg.Connection.LastUsername != "gul" || cfg.Gate.Mode != GateModePTT || cfg.Gate.PTTKey != "KeyF" {
		t.Errorf("surrounding fields lost: %+v", cfg)
	}
	if cfg.Gate.OpenThreshold != DefaultOpenThreshold {
		t.Errorf("open threshold = %v, want the default", cfg.Gate.OpenThreshold)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "gul")

	if err := Save(dir, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Fatalf("directory holds %v, want only %s", entries, FileName)
	}
	if runtime.GOOS == "windows" {
		return
	}
	file, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := file.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %04o, want 0600", perm)
	}
	created, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := created.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %04o, want 0700", perm)
	}
}

// A configuration directory that cannot be written costs persistence, never
// the session: Save reports and leaves nothing behind.
func TestSaveOnReadOnlyDirectoryFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not gate writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := Save(dir, Defaults()); err == nil {
		t.Fatal("Save succeeded on a read-only directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Save left %v behind", entries)
	}
}

// A snapshot must not be able to reach the preserved document of the value it
// was copied from: two saves of two snapshots may not bleed into each other.
func TestSnapshotsDoNotShareTheirWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, `{"version": 1, "appearance": {"accent": "#2F52DE"}}`)

	base, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	first := base
	first.Gate.PTTKey = "KeyF"
	if _, err := first.document(); err != nil {
		t.Fatalf("document: %v", err)
	}

	doc, err := base.document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gate, _ := decoded["gate"].(map[string]any)
	if gate["ptt_key"] != DefaultPTTKey {
		t.Fatalf("the second snapshot carries the first one's edit: %v", gate)
	}
}
