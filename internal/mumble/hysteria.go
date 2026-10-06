package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/LywwKkA-aD/Gul/internal/hysteria"
	"github.com/LywwKkA-aD/Gul/internal/identity"
)

// The official Hysteria server and Murmur share a network namespace. Its ACL
// permits only this destination, so a Gul invitation cannot proxy arbitrary
// traffic through the voice server.
const mumbleTarget = "127.0.0.1:64738"

func dialHysteria(ctx context.Context, cfg DialConfig, ep endpoint, tofu *TOFUStore) (net.Conn, error) {
	if ep.kind != endpointHysteria {
		return nil, errors.New("требуется адрес сервера Hysteria")
	}
	u, err := url.Parse(ep.address)
	if err != nil {
		return nil, errors.New("неверный адрес сервера Hysteria")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	stream, err := hysteria.Dial(ctx, hysteria.Config{
		Server:      net.JoinHostPort(ep.host, port),
		ServerName:  ep.host,
		Password:    cfg.Password,
		Obfuscation: ep.obfuscation,
		TLSConfig:   cfg.OuterRoots,
	}, mumbleTarget)
	if err != nil {
		return nil, err
	}
	return mumbleTLS(ctx, stream, cfg, ep, tofu)
}

// Mumble's TLS runs end to end inside Hysteria. Existing pins and the derived
// client certificate retain their meaning; no identity key leaves the client.
func mumbleTLS(ctx context.Context, stream net.Conn, cfg DialConfig, ep endpoint, tofu *TOFUStore) (net.Conn, error) {
	if tofu == nil {
		_ = stream.Close()
		return nil, errors.New("TOFU store is required")
	}
	tlsConfig := tofu.TLSConfig(ep.host)
	tlsConfig.ServerName = ep.host
	tlsConfig.MinVersion = tls.VersionTLS12
	if len(cfg.IdentitySeed) != 0 {
		own, err := identity.ForHost(cfg.IdentitySeed, ep.host)
		if err != nil {
			_ = stream.Close()
			return nil, fmt.Errorf("client identity: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{own.Certificate}
	} else if cfg.Certificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*cfg.Certificate}
	}
	secured := tls.Client(stream, tlsConfig)
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("mumble TLS: %w", err)
	}
	return secured, nil
}
