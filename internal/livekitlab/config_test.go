//go:build !windows

package livekitlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeKeepsPrivateStableCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := Initialize(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "broker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.APISecret) != 64 || len(cfg.APIKey) < 16 {
		t.Fatal("weak credentials")
	}
	if err := Initialize(dir); err != nil {
		t.Fatal(err)
	}
	again, err := LoadConfig(filepath.Join(dir, "broker.json"))
	if err != nil || cfg != again {
		t.Fatal("credentials changed")
	}
	for _, name := range []string{"broker.json", "server.yaml"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions = %o", name, info.Mode().Perm())
		}
	}
	yaml, err := os.ReadFile(filepath.Join(dir, "server.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"node_ip: 127.0.0.1", "use_external_ip: false", "enable_loopback_candidate: false", "udp_port: 7882", "stun_servers: [\"127.0.0.1:7882\"]", "max_participants: 16", cfg.APISecret, cfg.APIKey} {
		if !strings.Contains(string(yaml), required) {
			t.Fatal("missing server configuration")
		}
	}
}

func TestLoadConfigRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("accepted missing file")
	}
	for _, tc := range []struct {
		name, data string
		mode       os.FileMode
	}{
		{"invalid", "{", 0600},
		{"short", `{"apiKey":"test-key","apiSecret":"short"}`, 0600},
		{"public", `{"apiKey":"test-key","apiSecret":"` + strings.Repeat("x", 32) + `"}`, 0644},
		{"unknown", `{"apiKey":"test-key","apiSecret":"` + strings.Repeat("x", 32) + `","other":true}`, 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.data), tc.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("accepted bad config")
			}
		})
	}
}

func TestInitializeRejectsCorruptExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broker.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(dir); err == nil {
		t.Fatal("overwrote corrupt existing config")
	}
}
