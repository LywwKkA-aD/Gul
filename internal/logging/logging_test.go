package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWithMinimumLevelFiltersWailsDebugPayloads(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wails := WithMinimumLevel(base, slog.LevelWarn)

	wails.Debug("Binding call complete:", "args", "test-binding-secret-marker")
	wails.Info("framework detail")
	wails.With("component", "webview").WithGroup("framework").Warn("framework warning", "retry", true)

	got := out.String()
	if strings.Contains(got, "test-binding-secret-marker") || strings.Contains(got, "framework detail") {
		t.Fatal("records below the minimum level reached the wrapped logger")
	}
	if !strings.Contains(got, "framework warning") || !strings.Contains(got, "webview") {
		t.Fatal("warning record or its attributes were lost")
	}
}

func TestSetupWritesJSONAndRotatesExistingLogs(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, logFileName)
	if err := os.WriteFile(current, []byte("old-current"), 0o600); err != nil {
		t.Fatalf("write current log: %v", err)
	}
	if err := os.Truncate(current, maxLogSize); err != nil {
		t.Fatalf("grow current log: %v", err)
	}
	if err := os.WriteFile(current+".1", []byte("old-one"), 0o600); err != nil {
		t.Fatalf("write generation 1: %v", err)
	}
	if err := os.WriteFile(current+".2", []byte("old-two"), 0o600); err != nil {
		t.Fatalf("write generation 2: %v", err)
	}

	previousDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previousDefault) })
	logger, closeLog, err := Setup(dir, slog.LevelDebug)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Debug("diagnostic event", "attempt", 2)
	if err := closeLog(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	contents, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	if !strings.Contains(string(contents), `"msg":"diagnostic event"`) ||
		!strings.Contains(string(contents), `"attempt":2`) {
		t.Fatal("current JSON log is missing the emitted record")
	}
	if info, err := os.Stat(current + ".1"); err != nil {
		t.Fatalf("stat rotated current log: %v", err)
	} else if info.Size() != maxLogSize {
		t.Fatalf("rotated current size = %d, want %d", info.Size(), maxLogSize)
	}
	for name, want := range map[string]string{
		current + ".2": "old-one",
		current + ".3": "old-two",
	} {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(name), err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", filepath.Base(name), got, want)
		}
	}
	if info, err := os.Stat(current); err != nil {
		t.Fatalf("stat current log: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("current log mode = %v, want a regular file", info.Mode())
	} else if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		// Windows reports synthesized POSIX bits; privacy there comes from the
		// per-user ACL inherited from os.UserConfigDir rather than chmod bits.
		t.Fatalf("current log permissions = %o, want 600", got)
	}
}

func TestSetupReportsOpenFailure(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing", "logs")
	logger, closeLog, err := Setup(missing, slog.LevelInfo)
	if err == nil {
		t.Fatal("Setup succeeded for a missing directory")
	}
	if logger != nil || closeLog != nil {
		t.Fatal("Setup returned usable values alongside an error")
	}
}

type handlerProbe struct {
	enabled        bool
	handleErr      error
	handled        *int
	withAttrsCalls *int
	withGroupCalls *int
}

func (h handlerProbe) Enabled(context.Context, slog.Level) bool { return h.enabled }
func (h handlerProbe) Handle(context.Context, slog.Record) error {
	(*h.handled)++
	return h.handleErr
}
func (h handlerProbe) WithAttrs([]slog.Attr) slog.Handler {
	(*h.withAttrsCalls)++
	return h
}
func (h handlerProbe) WithGroup(string) slog.Handler {
	(*h.withGroupCalls)++
	return h
}

func TestFanoutHonoursHandlersAndReturnsFirstError(t *testing.T) {
	t.Parallel()

	firstErr := errors.New("first handler failed")
	counts := make([]int, 3)
	attrCalls := make([]int, 3)
	groupCalls := make([]int, 3)
	f := fanout{
		handlerProbe{enabled: false, handled: &counts[0], withAttrsCalls: &attrCalls[0], withGroupCalls: &groupCalls[0]},
		handlerProbe{enabled: true, handleErr: firstErr, handled: &counts[1], withAttrsCalls: &attrCalls[1], withGroupCalls: &groupCalls[1]},
		handlerProbe{enabled: true, handled: &counts[2], withAttrsCalls: &attrCalls[2], withGroupCalls: &groupCalls[2]},
	}
	ctx := context.Background()
	if !f.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("fanout disabled a level accepted by wrapped handlers")
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	if err := f.Handle(ctx, record); !errors.Is(err, firstErr) {
		t.Fatalf("Handle error = %v, want %v", err, firstErr)
	}
	if counts[0] != 0 || counts[1] != 1 || counts[2] != 1 {
		t.Fatalf("handler calls = %v, want [0 1 1]", counts)
	}
	if got := f.WithAttrs([]slog.Attr{slog.String("key", "value")}); len(got.(fanout)) != len(f) {
		t.Fatalf("WithAttrs fanout length = %d, want %d", len(got.(fanout)), len(f))
	}
	if got := f.WithGroup("group"); len(got.(fanout)) != len(f) {
		t.Fatalf("WithGroup fanout length = %d, want %d", len(got.(fanout)), len(f))
	}
	for i := range f {
		if attrCalls[i] != 1 || groupCalls[i] != 1 {
			t.Fatalf("handler %d transform calls: attrs=%d group=%d", i, attrCalls[i], groupCalls[i])
		}
	}
}
