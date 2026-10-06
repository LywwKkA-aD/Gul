package mumble

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Gul profiles carry only the public REALITY parameters. Authentication stays
// in the password field; the target and transport options are not configurable.
func parseRealityEndpoint(value string) (endpoint, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return endpoint{}, errors.New("invalid VLESS server URL; enter the password in the password field")
	}
	if parsed.Path != "" || parsed.RawPath != "" || strings.Contains(value, "#") {
		return endpoint{}, errors.New("VLESS server URL cannot contain a path or fragment")
	}
	// Requiring literal keys and values avoids alternate spellings, duplicate
	// options, hidden credentials, and ambiguous share links.
	query := make(url.Values)
	for _, part := range strings.Split(parsed.RawQuery, "&") {
		key, item, ok := strings.Cut(part, "=")
		if !ok || item == "" || strings.ContainsAny(part, "%+; \t\r\n") || query.Has(key) {
			return endpoint{}, errors.New("invalid or duplicate VLESS profile parameter")
		}
		switch key {
		case "security", "sni", "pbk", "sid", "flow", "type":
			query.Set(key, item)
		default:
			return endpoint{}, errors.New("unsupported VLESS profile parameter")
		}
	}
	if len(query) != 6 || query.Get("security") != "reality" || query.Get("flow") != "none" || query.Get("type") != "tcp" {
		return endpoint{}, errors.New("VLESS requires REALITY over TCP with flow=none and sni, pbk, sid parameters")
	}
	serverName, err := canonicalHost(query.Get("sni"))
	if err != nil {
		return endpoint{}, errors.New("invalid REALITY server name")
	}
	if _, err := netip.ParseAddr(serverName); err == nil {
		return endpoint{}, errors.New("REALITY server name must be a DNS name")
	}
	publicKey := query.Get("pbk")
	key, err := base64.RawURLEncoding.Strict().DecodeString(publicKey)
	if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != publicKey {
		return endpoint{}, errors.New("invalid REALITY public key")
	}
	shortID := query.Get("sid")
	id, err := hex.DecodeString(shortID)
	if err != nil || len(id) == 0 || len(id) > 8 {
		return endpoint{}, errors.New("invalid REALITY short ID")
	}
	shortID = hex.EncodeToString(id)
	host, err := canonicalHost(parsed.Hostname())
	if err != nil {
		return endpoint{}, err
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
		if port == "443" {
			port = ""
		}
	}
	parsed.Host = host
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	}
	query.Set("sni", serverName)
	query.Set("sid", shortID)
	parsed.Scheme = "vless"
	parsed.RawQuery = query.Encode()
	return endpoint{
		kind: endpointReality, address: parsed.String(), host: host,
		realityServerName: serverName, realityPublicKey: publicKey, realityShortID: shortID,
	}, nil
}
