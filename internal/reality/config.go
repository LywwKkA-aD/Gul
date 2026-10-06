// Package reality provides a fixed VLESS TCP transport with an authenticated
// REALITY handshake adapted from the official Xray implementation.
package reality

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var (
	ErrPasswordRequired = errors.New("VLESS password is required")
	ErrAuthentication   = errors.New("REALITY server authentication failed")
	ErrProtocol         = errors.New("invalid VLESS response")
)

// Config is a fixed REALITY + TCP preset without Vision, never an arbitrary Xray config.
// Password is supplied separately from the public endpoint parameters.
type Config struct {
	Server     string
	ServerName string
	PublicKey  string
	ShortID    string
	Password   string
}

// UserID derives the server's VLESS UUID from the original join password bytes.
// The domain-separated SHA-256 result uses the UUIDv8 and RFC variant bits.
// Deployments should generate high-entropy passwords; this is not a password KDF.
func UserID(password string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("gul/vless-reality/user-id/v1\x00"))
	_, _ = h.Write([]byte(password))
	b := h.Sum(nil)[:16]
	b[6] = b[6]&0x0f | 0x80
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type parsedConfig struct {
	publicKey []byte
	shortID   []byte
}

func validate(cfg Config) (*parsedConfig, error) {
	if cfg.Password == "" {
		return nil, ErrPasswordRequired
	}
	host, portText, err := net.SplitHostPort(cfg.Server)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 || !validHost(host) {
		return nil, errors.New("invalid VLESS server address: expected host:port")
	}
	if !validDNSName(cfg.ServerName) || net.ParseIP(cfg.ServerName) != nil {
		return nil, errors.New("REALITY requires a valid DNS server name")
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(cfg.PublicKey)
	if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != cfg.PublicKey {
		return nil, errors.New("invalid REALITY public key")
	}
	id, err := hex.DecodeString(cfg.ShortID)
	if err != nil || len(id) < 1 || len(id) > 8 || hex.EncodeToString(id) != cfg.ShortID {
		return nil, errors.New("invalid REALITY short ID")
	}
	shortID := make([]byte, 8)
	copy(shortID, id)
	return &parsedConfig{publicKey: key, shortID: shortID}, nil
}

func validHost(host string) bool {
	return net.ParseIP(host) != nil || validDNSName(host)
}

func validDNSName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}
