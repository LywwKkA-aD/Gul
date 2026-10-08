package broker

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

// PublicConfig is private server configuration, never a client response.
// JoinPasswordSHA256 must hash a randomly generated password of at least 16
// bytes. Store it with mode 0600; a read-only systemd credential may use 0400.
type PublicConfig struct {
	ListenAddress      string `json:"listenAddress"`
	PublicOrigin       string `json:"publicOrigin"`
	LiveKitURL         string `json:"liveKitURL"`
	LiveKitInternalURL string `json:"liveKitInternalURL"`
	APIKey             string `json:"apiKey"`
	APISecret          string `json:"apiSecret"`
	JoinPasswordSHA256 string `json:"joinPasswordSHA256"`
	StatePath          string `json:"statePath,omitempty"`
}

func (c PublicConfig) Validate() error {
	invalid := errors.New("invalid public LiveKit server configuration")
	if (credentials{APIKey: c.APIKey, APISecret: c.APISecret}).validate() != nil {
		return invalid
	}
	address, err := netip.ParseAddrPort(c.ListenAddress)
	if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
		return invalid
	}
	origin, err := url.Parse(c.PublicOrigin)
	if err != nil || !cleanServerURL(origin, "https") {
		return invalid
	}
	sfu, err := url.Parse(c.LiveKitURL)
	if err != nil || !cleanServerURL(sfu, "wss") || sfu.Host != origin.Host {
		return invalid
	}
	internal, err := url.Parse(c.LiveKitInternalURL)
	if err != nil || !cleanServerURL(internal, "http") {
		return invalid
	}
	internalIP, err := netip.ParseAddr(internal.Hostname())
	if err != nil || !internalIP.IsLoopback() {
		return invalid
	}
	hash, err := hex.DecodeString(c.JoinPasswordSHA256)
	if err != nil || len(hash) != 32 {
		return invalid
	}
	if c.StatePath != "" && (!filepath.IsAbs(c.StatePath) || filepath.Clean(c.StatePath) != c.StatePath) {
		return invalid
	}
	return nil
}

func cleanServerURL(u *url.URL, scheme string) bool {
	return u.Scheme == scheme && u.Host != "" && u.Hostname() != "" && u.User == nil &&
		u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == ""
}

func LoadPublicConfig(path string) (PublicConfig, error) {
	var cfg PublicConfig
	if runtime.GOOS == "windows" {
		return cfg, errors.New("public server configuration requires Unix file permissions")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || (info.Mode().Perm() != 0600 && info.Mode().Perm() != 0400) {
		return cfg, errors.New("public server config must be a private regular file (0400 or 0600)")
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("cannot read public server config")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return cfg, errors.New("invalid public server config")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return PublicConfig{}, errors.New("invalid public server config")
	}
	if err := cfg.Validate(); err != nil {
		return PublicConfig{}, err
	}
	return cfg, nil
}
