package core

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := make(map[string]string, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func TestCollectWritesInfoAndLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("gul.log", "current run\n")
	write("gul.log.1", "previous run\n")
	write("gul.log.2", "older run\n")

	path, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("archive %q is not inside %q", path, dir)
	}
	if !strings.HasSuffix(path, ".zip") {
		t.Fatalf("archive %q is not a zip", path)
	}

	files := readZip(t, path)

	info, ok := files["info.txt"]
	if !ok {
		t.Fatalf("info.txt missing, got %v", keys(files))
	}
	for _, want := range []string{Version, runtime.GOOS, runtime.GOARCH, runtime.Version(), "collected:"} {
		if !strings.Contains(info, want) {
			t.Errorf("info.txt missing %q:\n%s", want, info)
		}
	}

	for name, want := range map[string]string{
		"logs/gul.log":   "current run\n",
		"logs/gul.log.1": "previous run\n",
		"logs/gul.log.2": "older run\n",
	} {
		if got, ok := files[name]; !ok {
			t.Errorf("%s missing, got %v", name, keys(files))
		} else if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestCollectRemovesBindingPayloadsAndConfigPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	secretMarker := "test-binding-secret-marker"

	records := []map[string]any{
		{
			"time":   "2026-08-22T17:08:00Z",
			"level":  "DEBUG",
			"msg":    "Binding call complete:",
			"method": "ConnectionService.Connect",
			"args":   `["example.invalid:64738","alice","` + secretMarker + `"]`,
			"result": "null",
		},
		{
			"time":  "2026-08-22T17:08:01Z",
			"level": "DEBUG",
			"msg":   "Runtime call:",
			"args":  secretMarker,
		},
		{
			"time":     "2026-08-22T17:08:01Z",
			"level":    "INFO",
			"msg":      "connect requested",
			"address":  "wss://user:" + secretMarker + "@example.invalid/mumble",
			"username": secretMarker,
		},
		{
			"time":       "2026-08-22T17:08:02Z",
			"level":      "INFO",
			"msg":        "gul starting",
			"config_dir": dir,
		},
		{
			"time":  "2026-08-22T17:08:03Z",
			"level": "WARN",
			"msg":   "ordinary diagnostic",
			"error": "failed to open " + filepath.Join(dir, "state.json"),
		},
	}

	var log strings.Builder
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		log.Write(line)
		log.WriteByte('\n')
	}
	// Older/plain-text handlers must be filtered too. This line deliberately
	// covers the old core connect record; the final binding line deliberately
	// has no trailing newline to exercise the final partial record.
	log.WriteString(`time=2026-08-22T17:08:04Z level=INFO msg="connect requested" address=` + secretMarker + "\n")
	log.WriteString(`time=2026-08-22T17:08:05Z level=DEBUG msg="Binding call complete:" args=` + secretMarker)
	if err := os.WriteFile(filepath.Join(dir, "gul.log"), []byte(log.String()), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	path, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	files := readZip(t, path)
	bundle := files["info.txt"] + files["logs/gul.log"]

	for label, forbidden := range map[string]string{
		"binding argument": secretMarker,
		"binding trace":    "Binding call complete:",
		"runtime trace":    "Runtime call:",
		"config path":      dir,
	} {
		if strings.Contains(bundle, forbidden) {
			t.Errorf("bundle still contains %s", label)
		}
	}
	if encodedDir, err := json.Marshal(dir); err != nil {
		t.Fatalf("marshal config path: %v", err)
	} else if strings.Contains(bundle, string(encodedDir[1:len(encodedDir)-1])) {
		t.Error("bundle still contains the JSON-encoded config path")
	}
	if !strings.Contains(files["info.txt"], "config_dir: <redacted>") {
		t.Error("info.txt does not mark the config directory as redacted")
	}
	if !strings.Contains(files["logs/gul.log"], "ordinary diagnostic") {
		t.Error("ordinary diagnostic line was removed")
	}
	if !strings.Contains(files["logs/gul.log"], "<config-dir>") ||
		!strings.Contains(files["logs/gul.log"], "state.json") {
		t.Error("config path inside an ordinary log record was not redacted")
	}
}

func TestCollectWithoutLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	path, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect on empty dir: %v", err)
	}
	files := readZip(t, path)
	if len(files) != 1 {
		t.Fatalf("archive holds %v, want only info.txt", keys(files))
	}
	if _, ok := files["info.txt"]; !ok {
		t.Fatalf("info.txt missing")
	}
}

// A bundle must never archive an earlier bundle, otherwise repeated support
// requests grow quadratically.
func TestCollectExcludesOtherBundlesAndSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	for _, name := range []string{
		"gul-diagnostics-20250101-000000.zip",
		"client.pem",
		"client.key",
		"tofu.json",
		"config.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("secret"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gul.log"), []byte("log"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	path, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	files := readZip(t, path)
	if len(files) != 2 {
		t.Fatalf("archive holds %v, want info.txt + logs/gul.log", keys(files))
	}
	for name := range files {
		if name != "info.txt" && name != "logs/gul.log" {
			t.Errorf("unexpected entry %q", name)
		}
	}
}

func TestCollectRejectsEmptyDir(t *testing.T) {
	t.Parallel()
	if _, err := Collect(""); err == nil {
		t.Fatal("Collect(\"\") succeeded, want error")
	}
}

func TestCollectSkipsDirectoriesNamedLikeLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "gul.log.d"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if files := readZip(t, path); len(files) != 1 {
		t.Fatalf("archive holds %v, want only info.txt", keys(files))
	}
}

func TestCollectFailsWhenArchiveCannotBeCreated(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	if _, err := Collect(missing); err == nil {
		t.Fatal("Collect into a missing directory succeeded, want error")
	}
}

// A log we cannot read must fail the whole bundle loudly and leave no partial
// archive behind, rather than shipping a silently incomplete one.
func TestCollectRemovesPartialArchiveOnError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "gul.log")
	if err := os.WriteFile(unreadable, []byte("log"), 0o000); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := Collect(dir); err == nil {
		t.Fatal("Collect succeeded despite an unreadable log")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, diagnosticsPrefix+"*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("partial archives left behind: %v", leftovers)
	}
}

// App.Collect resolves the config directory itself, so the environment is
// redirected to a temp dir to keep the real one untouched.
func TestAppCollectWritesIntoConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)

	app := New(nil, nil)
	path, err := app.Collect()
	if err != nil {
		t.Fatalf("App.Collect: %v", err)
	}
	if !strings.HasPrefix(path, home) {
		t.Fatalf("archive %q is outside the redirected config dir %q", path, home)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if files := readZip(t, path); len(files) == 0 {
		t.Fatal("archive is empty")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
