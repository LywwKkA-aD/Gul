package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	logFileName     = "gul.log"
	maxLogSize      = 5 << 20 // rotate when the previous run left more than 5 MiB
	keptGenerations = 3
)

// Setup configures the process-wide slog default: text to stderr plus JSON to
// a file in dir. Rotation happens at startup only; the audio path must never
// pay for logging I/O mid-session.
func Setup(dir string, level slog.Level) (*slog.Logger, func() error, error) {
	path := filepath.Join(dir, logFileName)
	rotate(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}

	logger := slog.New(fanout{
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}),
		slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level}),
	})
	slog.SetDefault(logger)
	return logger, f.Close, nil
}

func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	// Rotation is best-effort: a failed rename must not block startup.
	for i := keptGenerations - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	_ = os.Rename(path, path+".1")
}

// fanout duplicates records to every wrapped handler.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

var _ io.Closer = (*os.File)(nil)
