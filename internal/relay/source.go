package relay

import (
	"net"
	"net/netip"
)

// sourceKey folds an address into the block one subscriber controls: IPv4 by
// /32, IPv6 by /64.
//
// Every source-keyed defense keys on this value and never on the raw address:
// the pre-authentication connection limit, the authorization ban, the session
// limit, and the pseudonymous upstream address Murmur autobans on. An IPv6
// subscriber is handed a /64 or shorter, so keying on the full address lets one
// customer rotate through 2^64 addresses and walk past all four of them while
// every counter stays at one.
func sourceKey(host string) string {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		// Unparseable addresses share one bucket rather than escaping the limit.
		return host
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return ip.String()
	}
	prefix, err := ip.Prefix(64)
	if err != nil {
		return ip.String()
	}
	return prefix.String()
}

// sourcePrefixKey is the net.Addr form of sourceKey, used by the
// pre-authentication listener, which sees connections rather than requests.
func sourcePrefixKey(addr net.Addr) string {
	host := ""
	if addr != nil {
		host = addr.String()
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return sourceKey(host)
}

// remoteIP returns the address part of an http.Request RemoteAddr. It is the
// raw address of one client and belongs in log lines only; anything that
// counts, bans, or buckets a source keys on sourceKey instead.
func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}
