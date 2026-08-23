package core

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// logGlob matches gul.log plus the rotated generations written by
// internal/logging (gul.log.1 .. gul.log.3).
const logGlob = "gul.log*"

// diagnosticsPrefix names the bundles; it must not match logGlob, otherwise a
// bundle would try to archive its predecessors.
const diagnosticsPrefix = "gul-diagnostics-"

const (
	redactedConfigInfo = "<redacted>"
	redactedConfigLog  = "<config-dir>"
)

// Collect bundles the log files and a short environment report into a zip
// inside cfgDir and returns the path of the archive.
//
// Only logs are included. Certificates, the TOFU store and config.json stay
// out on purpose: a diagnostics bundle is meant to be shareable (PLAN.md §10.7).
func Collect(cfgDir string) (string, error) {
	if cfgDir == "" {
		return "", fmt.Errorf("diagnostics: empty config dir")
	}
	now := time.Now()
	path := filepath.Join(cfgDir, diagnosticsPrefix+now.Format("20060102-150405")+".zip")

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("diagnostics: create archive: %w", err)
	}
	zw := zip.NewWriter(f)

	if err := writeInfo(zw, now); err != nil {
		closeAll(zw, f)
		_ = os.Remove(path)
		return "", err
	}
	if err := writeLogs(zw, cfgDir); err != nil {
		closeAll(zw, f)
		_ = os.Remove(path)
		return "", err
	}

	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("diagnostics: finalize archive: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("diagnostics: close archive: %w", err)
	}
	return path, nil
}

func writeInfo(zw *zip.Writer, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "app:        Gul %s\n", Version)
	fmt.Fprintf(&b, "collected:  %s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "os/arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "go:         %s\n", runtime.Version())
	fmt.Fprintf(&b, "cpus:       %d\n", runtime.NumCPU())
	fmt.Fprintf(&b, "config_dir: %s\n", redactedConfigInfo)

	w, err := zw.Create("info.txt")
	if err != nil {
		return fmt.Errorf("diagnostics: create info.txt: %w", err)
	}
	if _, err := w.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("diagnostics: write info.txt: %w", err)
	}
	return nil
}

func writeLogs(zw *zip.Writer, cfgDir string) error {
	matches, err := filepath.Glob(filepath.Join(cfgDir, logGlob))
	if err != nil {
		return fmt.Errorf("diagnostics: scan logs: %w", err)
	}
	for _, src := range matches {
		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			// A log rotated away between Glob and Stat is not an error.
			continue
		}
		if err := copyLogInto(zw, src, "logs/"+filepath.Base(src), cfgDir); err != nil {
			return err
		}
	}
	return nil
}

func copyLogInto(zw *zip.Writer, src, name, cfgDir string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("diagnostics: open %s: %w", filepath.Base(src), err)
	}
	defer func() { _ = in.Close() }()

	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("diagnostics: create %s: %w", name, err)
	}

	reader := bufio.NewReader(in)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" && !isSensitiveLogRecord(line) {
			if _, err := io.WriteString(w, redactConfigPath(line, cfgDir)); err != nil {
				return fmt.Errorf("diagnostics: copy %s: %w", name, err)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("diagnostics: copy %s: %w", name, readErr)
		}
	}
	return nil
}

// The Wails records that must never enter a shareable bundle, matched by the
// exact message strings the framework logs. A version bump can rename them, so
// TestSensitiveWailsRecordsStillExistUpstream checks them against the module
// source and fails loudly instead of letting the filter go quiet.
const (
	wailsModule        = "github.com/wailsapp/wails/v3"
	bindingCallPrefix  = "Binding call "
	runtimeCallMessage = "Runtime call:"
	// connectRequestedMessage is Gul's own: alpha.1 attached the raw server
	// address and username to it.
	connectRequestedMessage = "connect requested"
)

// Wails' debug binding completion record contains every method argument and
// the return value. That includes join passwords, chat text and opaque device
// IDs, so these framework traces must never enter a shareable support bundle.
// Runtime-call traces are excluded for the same reason: their args may contain
// clipboard or dialog payloads. Older Gul builds also attached the raw server
// address and username to "connect requested", so that lifecycle record is
// dropped too. Matching fixed messages as a fallback covers text handlers and
// malformed JSON records.
func isSensitiveLogRecord(line string) bool {
	var record struct {
		Message string `json:"msg"`
	}
	if json.Unmarshal([]byte(line), &record) == nil {
		if strings.HasPrefix(record.Message, bindingCallPrefix) ||
			record.Message == runtimeCallMessage ||
			record.Message == connectRequestedMessage {
			return true
		}
	}
	return strings.Contains(line, bindingCallPrefix) || strings.Contains(line, runtimeCallMessage) ||
		strings.Contains(line, `msg="`+connectRequestedMessage+`"`)
}

// redactConfigPath handles both text logs and JSON logs. encoding/json escapes
// Windows separators (and a few path characters), therefore both the literal
// and JSON-encoded spellings are replaced.
func redactConfigPath(line, cfgDir string) string {
	redacted := strings.ReplaceAll(line, cfgDir, redactedConfigLog)
	if slashPath := filepath.ToSlash(cfgDir); slashPath != cfgDir {
		redacted = strings.ReplaceAll(redacted, slashPath, redactedConfigLog)
	}
	if encoded, err := json.Marshal(cfgDir); err == nil && len(encoded) >= 2 {
		redacted = strings.ReplaceAll(redacted, string(encoded[1:len(encoded)-1]), redactedConfigLog)
	}
	return redacted
}

func closeAll(zw *zip.Writer, f *os.File) {
	_ = zw.Close()
	_ = f.Close()
}
