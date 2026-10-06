package livekit

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

const localBrokerAddress = "http://127.0.0.1:8787"

func brokerAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == localBrokerAddress || address == "livekit://127.0.0.1:8787" {
		return localBrokerAddress, nil
	}
	return secureEndpoint(address, "https")
}

func mediaAddress(address string) (string, error) {
	if address == "ws://127.0.0.1:7880" || address == "ws://127.0.0.1:7880/" ||
		address == "http://127.0.0.1:7880" || address == "http://127.0.0.1:7880/" {
		return "ws://127.0.0.1:7880", nil
	}
	return secureEndpoint(address, "wss")
}

// Credentials belong to the authenticated broker's authority. A response may
// not redirect the bearer token to another host, port or plaintext endpoint.
func validGrantForBroker(base string, grant api.Grant, screen bool) bool {
	if !validGrant(grant, screen) {
		return false
	}
	endpoint, err := mediaAddress(grant.URL)
	if err != nil {
		return false
	}
	if base == localBrokerAddress {
		return endpoint == "ws://127.0.0.1:7880"
	}
	canonical, err := secureEndpoint(base, "https")
	return err == nil && endpoint == "wss"+strings.TrimPrefix(canonical, "https")
}

// Only origins are accepted: SDK and broker routes are fixed by this client.
// Canonical authorities make case, IPv6 and implicit port 443 comparisons exact.
func secureEndpoint(raw, scheme string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != scheme || u.Host == "" || u.Opaque != "" || u.User != nil ||
		u.ForceQuery || u.RawQuery != "" || strings.Contains(raw, "#") || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") || strings.HasSuffix(u.Host, ":") {
		return "", ErrInvalidAddress
	}
	host := strings.ToLower(u.Hostname())
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || addr.Unmap().IsUnspecified() || addr.Unmap().IsMulticast() ||
			(addr.Is6() && !strings.HasPrefix(u.Host, "[")) {
			return "", ErrInvalidAddress
		}
		host = addr.Unmap().String()
	} else if strings.HasPrefix(u.Host, "[") || !validDNSName(host) {
		return "", ErrInvalidAddress
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return "", ErrInvalidAddress
		}
		port = strconv.FormatUint(n, 10)
	}
	if port == "443" {
		port = ""
	}
	authority := host
	if port != "" {
		authority = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	return scheme + "://" + authority, nil
}

func validDNSName(host string) bool {
	if host == "" || len(host) > 253 || strings.Trim(host, "0123456789.") == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
