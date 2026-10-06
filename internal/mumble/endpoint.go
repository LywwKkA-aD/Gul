package mumble

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type endpointKind uint8

const endpointHysteria endpointKind = iota

const hysteriaDefaultPort = "443"

type endpoint struct {
	kind        endpointKind
	address     string
	host        string
	obfuscation string
}

func parseEndpoint(value string) (endpoint, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return endpoint{}, errors.New("server address is required")
	}

	if !strings.Contains(value, "://") {
		if strings.ContainsAny(value, "/?#@ \t\r\n") {
			return endpoint{}, errors.New("invalid Hysteria server address")
		}
		if ip, err := netip.ParseAddr(value); err == nil && ip.Is6() {
			value = "[" + value + "]"
		}
		value = "hysteria2://" + value
	}
	return parseHysteriaEndpoint(value)
}

// The address is public configuration and a credential-store key. Accept only
// the endpoint and an optional nonsecret obfuscation mode, never a share URI
// carrying credentials or a configurable proxy destination.
func parseHysteriaEndpoint(value string) (endpoint, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return endpoint{}, errors.New("invalid Hysteria server URL")
	}
	if strings.EqualFold(parsed.Scheme, "wss") {
		return endpoint{}, errors.New("the old WSS relay is no longer supported; enter the new Hysteria server address")
	}
	if !strings.EqualFold(parsed.Scheme, "hysteria2") && !strings.EqualFold(parsed.Scheme, "hy2") {
		return endpoint{}, errors.New("server URL must use hysteria2:// or hy2://")
	}
	if parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return endpoint{}, errors.New("invalid Hysteria server URL; enter the password in the password field")
	}
	if strings.Contains(value, "#") {
		return endpoint{}, errors.New("server URL cannot contain a fragment")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return endpoint{}, errors.New("server URL cannot contain a path")
	}
	var obfuscation string
	switch parsed.RawQuery {
	case "":
		if parsed.ForceQuery {
			return endpoint{}, errors.New("server URL query must select salamander or gecko obfuscation")
		}
	case "obfs=salamander":
		obfuscation = "salamander"
	case "obfs=gecko":
		obfuscation = "gecko"
	default:
		return endpoint{}, errors.New("server URL only supports ?obfs=salamander or ?obfs=gecko")
	}
	port := parsed.Port()
	if port == "" && strings.HasSuffix(parsed.Host, ":") {
		return endpoint{}, errors.New("server port must be between 1 and 65535")
	}
	if port != "" {
		if err := validatePort(port); err != nil {
			return endpoint{}, err
		}
		number, _ := strconv.Atoi(port)
		port = strconv.Itoa(number)
		if port == hysteriaDefaultPort {
			port = ""
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
	parsed.Scheme = "hysteria2"
	parsed.Path = ""
	return endpoint{kind: endpointHysteria, address: parsed.String(), host: host, obfuscation: obfuscation}, nil
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

func validatePort(port string) error {
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("server port must be between 1 and 65535")
	}
	return nil
}
