package mumble

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

type endpointKind uint8

const (
	endpointDirect endpointKind = iota
	endpointWSS
)

type endpoint struct {
	kind    endpointKind
	address string
	host    string
}

func parseEndpoint(value string) (endpoint, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return endpoint{}, errors.New("server address is required")
	}

	if strings.Contains(value, "://") {
		return parseWSSEndpoint(value)
	}
	if strings.ContainsAny(value, "/?#@ \t\r\n") {
		return endpoint{}, errors.New("invalid Mumble server address")
	}
	address, host := normalizeAddress(value)
	if err := validateDirectAddress(address, host); err != nil {
		return endpoint{}, err
	}
	host, err := canonicalHost(host)
	if err != nil {
		return endpoint{}, err
	}
	_, port, _ := net.SplitHostPort(address)
	address = net.JoinHostPort(host, port)
	return endpoint{kind: endpointDirect, address: address, host: host}, nil
}

func parseWSSEndpoint(value string) (endpoint, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return endpoint{}, errors.New("invalid WSS server URL")
	}
	if !strings.EqualFold(parsed.Scheme, "wss") {
		return endpoint{}, errors.New("relay URL must use wss://")
	}
	if parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return endpoint{}, errors.New("invalid WSS server URL")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return endpoint{}, errors.New("relay URL cannot contain a query or fragment")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = relayproto.Path
		parsed.RawPath = ""
	}
	if parsed.Path != relayproto.Path || parsed.RawPath != "" {
		return endpoint{}, fmt.Errorf("relay URL path must be %s", relayproto.Path)
	}
	port := parsed.Port()
	if port != "" {
		if err := validatePort(port); err != nil {
			return endpoint{}, err
		}
	}
	host, err := canonicalHost(parsed.Hostname())
	if err != nil {
		return endpoint{}, err
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	parsed.Scheme = "wss"
	return endpoint{kind: endpointWSS, address: parsed.String(), host: host}, nil
}

// canonicalHost makes the TLS SNI name and TOFU key stable for equivalent
// spellings. Without this, DNS case or a trailing root dot could silently
// create a second first-use pin for the same server.
func canonicalHost(host string) (string, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return "", errors.New("scoped IP addresses are not supported")
		}
		return addr.String(), nil
	}

	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasSuffix(host, ".") {
		return "", errors.New("invalid server host")
	}
	host = strings.ToLower(host)
	if !validDNSHost(host) {
		return "", errors.New("invalid server host")
	}
	return host, nil
}

func validDNSHost(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !isASCIILetterOrDigit(label[0]) ||
			!isASCIILetterOrDigit(label[len(label)-1]) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			if !isASCIILetterOrDigit(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIILetterOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func validateDirectAddress(address, host string) error {
	if host == "" {
		return errors.New("mumble server host is required")
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid Mumble server address: %w", err)
	}
	return validatePort(port)
}

func validatePort(port string) error {
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("server port must be between 1 and 65535")
	}
	return nil
}

// normalizeAddress appends the default Mumble port when the caller omitted it
// and returns the dial address plus the bare host used as the TOFU pin key.
func normalizeAddress(address string) (addr, host string) {
	addr = strings.TrimSpace(address)
	if splitHost, _, err := net.SplitHostPort(addr); err == nil {
		return addr, splitHost
	}
	if ip := net.ParseIP(strings.Trim(addr, "[]")); ip != nil {
		host = strings.Trim(addr, "[]")
		return net.JoinHostPort(host, strconv.Itoa(gumble.DefaultPort)), host
	}
	if strings.Contains(addr, ":") {
		return addr, ""
	}
	return net.JoinHostPort(addr, strconv.Itoa(gumble.DefaultPort)), addr
}
