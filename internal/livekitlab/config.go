package livekitlab

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var errNativeWindowsConfig = errors.New("native Windows local server configuration is unsupported; run the local server on macOS or Linux")

// Config is stored only in the ignored local lab directory, with mode 0600.
type Config struct {
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
}

func (c Config) Validate() error {
	if !validIdentity(c.APIKey) || len(c.APISecret) < 32 || len(c.APISecret) > 256 || strings.ContainsAny(c.APISecret, "\r\n\t ") {
		return errors.New("invalid local LiveKit credentials")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	// Windows' os.FileMode cannot verify private NTFS ACLs. Keep the GUI
	// portable, but reject the Unix-only broker bootstrap before any file I/O.
	if runtime.GOOS == "windows" {
		return Config{}, errNativeWindowsConfig
	}
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, errors.New("local config is missing; run scripts/livekit-local.sh up")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Config{}, errors.New("local config must be a private regular file (0600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, errors.New("cannot read local config")
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, errors.New("invalid local config")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Initialize creates or reuses credentials and writes a matching SFU config.
func Initialize(dir string) error {
	if runtime.GOOS == "windows" {
		return errNativeWindowsConfig
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create local directory: %w", err)
	}
	path := filepath.Join(dir, "broker.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			return err
		}
		key, secret := make([]byte, 12), make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return errors.New("cannot generate local key")
		}
		if _, err := rand.Read(secret); err != nil {
			return errors.New("cannot generate local secret")
		}
		cfg = Config{APIKey: "gul-" + hex.EncodeToString(key), APISecret: hex.EncodeToString(secret)}
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return errors.New("cannot encode local config")
		}
		if err := writePrivate(path, append(data, '\n')); err != nil {
			return err
		}
	}
	// Docker publishes every port on host loopback. Inside the container the
	// listeners bind normally, while ICE advertises the published host address.
	// Do not gather the container's loopback interface: NAT would map it and
	// eth0 to one candidate, and the UDP mux could select the unreachable one.
	// An empty STUN list makes LiveKit advertise public defaults to clients.
	// Keep that list explicitly on loopback; local connections use host ICE
	// candidates and do not require a standalone STUN service.
	server := fmt.Sprintf(`port: 7880
rtc:
  tcp_port: 7881
  udp_port: 7882
  node_ip: 127.0.0.1
  use_external_ip: false
  enable_loopback_candidate: false
  stun_servers: ["127.0.0.1:7882"]
keys:
  %s: %q
logging:
  level: warn
room:
  max_participants: 16
  empty_timeout: 60
  departure_timeout: 20
`, cfg.APIKey, cfg.APISecret)
	return writePrivate(filepath.Join(dir, "server.yaml"), []byte(server))
}

func writePrivate(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".livekit-")
	if err != nil {
		return errors.New("cannot create private local config")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return errors.New("cannot write local config")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close local config")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot install local config")
	}
	return nil
}
