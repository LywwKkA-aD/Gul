package reality

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var ErrLiveKitProfile = errors.New("invalid LiveKit REALITY profile; use public parameters and enter the password separately")

type LiveKitProfile struct {
	Address string
	Origin  string
	Config  Config
}

// ParseLiveKitProfile accepts only Gul's fixed REALITY/TCP preset. The outer
// port is independent of the HTTPS service: the latter always uses port 443.
func ParseLiveKitProfile(raw string) (LiveKitProfile, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "livekit+vless" || u.User != nil || u.Opaque != "" ||
		u.Hostname() == "" || u.Path != "" || u.RawPath != "" || strings.Contains(raw, "#") {
		return LiveKitProfile{}, ErrLiveKitProfile
	}
	query := make(url.Values)
	for _, part := range strings.Split(u.RawQuery, "&") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" || strings.ContainsAny(part, "%+; \t\r\n") || query.Has(key) {
			return LiveKitProfile{}, ErrLiveKitProfile
		}
		switch key {
		case "security", "flow", "type", "sni", "pbk", "sid":
			query.Set(key, value)
		default:
			return LiveKitProfile{}, ErrLiveKitProfile
		}
	}
	if len(query) != 6 || query.Get("security") != "reality" || query.Get("flow") != "none" || query.Get("type") != "tcp" {
		return LiveKitProfile{}, ErrLiveKitProfile
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || ip.Unmap().IsUnspecified() || ip.Unmap().IsMulticast() || (ip.Is6() && !strings.HasPrefix(u.Host, "[")) {
			return LiveKitProfile{}, ErrLiveKitProfile
		}
		host = ip.Unmap().String()
	} else if !validDNSName(host) || strings.Trim(host, "0123456789.") == "" || strings.HasPrefix(u.Host, "[") {
		return LiveKitProfile{}, ErrLiveKitProfile
	}
	port := u.Port()
	if port == "" {
		if strings.HasSuffix(u.Host, ":") {
			return LiveKitProfile{}, ErrLiveKitProfile
		}
		port = "443"
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return LiveKitProfile{}, ErrLiveKitProfile
	}
	port = strconv.FormatUint(n, 10)
	cfg := Config{Server: net.JoinHostPort(host, port), ServerName: strings.ToLower(query.Get("sni")), PublicKey: query.Get("pbk"), ShortID: query.Get("sid"), Password: "validate-public-profile"}
	if _, err := validate(cfg); err != nil {
		return LiveKitProfile{}, ErrLiveKitProfile
	}
	cfg.Password = ""
	publicHost := host
	if strings.Contains(host, ":") {
		publicHost = "[" + host + "]"
	}
	u.Host = publicHost
	if port != "443" {
		u.Host = net.JoinHostPort(host, port)
	}
	query.Set("sni", cfg.ServerName)
	u.RawQuery = query.Encode()
	return LiveKitProfile{Address: u.String(), Origin: "https://" + publicHost, Config: cfg}, nil
}
