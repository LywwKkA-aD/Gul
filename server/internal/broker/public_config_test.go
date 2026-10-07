package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPublicConfigValidation(t *testing.T) {
	if err := publicTestConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PublicConfig){
		func(c *PublicConfig) { c.PublicOrigin = "http://voice.example.test" },
		func(c *PublicConfig) { c.PublicOrigin += "/api" },
		func(c *PublicConfig) { c.LiveKitURL = "wss://other.example.test" },
		func(c *PublicConfig) { c.LiveKitURL = "ws://voice.example.test" },
		func(c *PublicConfig) { c.LiveKitURL += "?secret=x" },
		func(c *PublicConfig) { c.LiveKitInternalURL = "http://192.0.2.1:7880" },
		func(c *PublicConfig) { c.ListenAddress = "0.0.0.0:8787" },
		func(c *PublicConfig) { c.JoinPasswordSHA256 = "bad" },
		func(c *PublicConfig) { c.APISecret = "short" },
	} {
		cfg := publicTestConfig()
		change(&cfg)
		if cfg.Validate() == nil {
			t.Fatal("unsafe public configuration accepted")
		}
	}
}

func TestPublicConfigPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("public server configuration requires Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "server.json")
	data, _ := json.Marshal(publicTestConfig())
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicConfig(path); err != nil {
		t.Fatal("systemd read-only private credential rejected")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicConfig(path); err == nil {
		t.Fatal("public config accepted exposed credentials")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(`{}`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicConfig(path); err == nil {
		t.Fatal("public config accepted trailing JSON")
	}
}
