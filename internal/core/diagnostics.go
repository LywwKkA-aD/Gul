package core

import (
	"archive/zip"
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

	if err := writeInfo(zw, cfgDir, now); err != nil {
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

func writeInfo(zw *zip.Writer, cfgDir string, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "app:        Gul %s\n", Version)
	fmt.Fprintf(&b, "collected:  %s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "os/arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "go:         %s\n", runtime.Version())
	fmt.Fprintf(&b, "cpus:       %d\n", runtime.NumCPU())
	fmt.Fprintf(&b, "config_dir: %s\n", cfgDir)

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
		if err := copyInto(zw, src, "logs/"+filepath.Base(src)); err != nil {
			return err
		}
	}
	return nil
}

func copyInto(zw *zip.Writer, src, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("diagnostics: open %s: %w", filepath.Base(src), err)
	}
	defer func() { _ = in.Close() }()

	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("diagnostics: create %s: %w", name, err)
	}
	if _, err := io.Copy(w, in); err != nil {
		return fmt.Errorf("diagnostics: copy %s: %w", name, err)
	}
	return nil
}

func closeAll(zw *zip.Writer, f *os.File) {
	_ = zw.Close()
	_ = f.Close()
}
