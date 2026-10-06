// Package hysteria connects Gul streams through the official Hysteria 2 client.
package hysteria

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	hyerrors "github.com/apernet/hysteria/core/v2/errors"
)

var (
	ErrAuthentication   = errors.New("hysteria authentication failed")
	ErrPasswordRequired = errors.New("hysteria password is required")
)

// Config describes a Gul Hysteria endpoint. Its obfuscation preset uses the
// authentication password for Salamander or Gecko as well.
type Config struct {
	Server      string
	ServerName  string
	Password    string
	Obfuscation string
	// TLSConfig supplies RootCAs for a private CA. Verification cannot be disabled.
	TLSConfig *tls.Config
}

// Dial opens a TCP stream through Hysteria. The context bounds setup only;
// closing the returned connection releases the stream and its private client.
func Dial(ctx context.Context, cfg Config, target string) (net.Conn, error) {
	return dialWithLookup(ctx, cfg, target, net.DefaultResolver.LookupIPAddr)
}

type lookupIPFunc func(context.Context, string) ([]net.IPAddr, error)

func dialWithLookup(ctx context.Context, cfg Config, target string, lookup lookupIPFunc) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host, port, err := validate(cfg, target)
	if err != nil {
		return nil, err
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve hysteria endpoint: %w", err)
	}
	serverName := cfg.ServerName
	if serverName == "" {
		serverName = host
	}
	tlsConfig := hyclient.TLSConfig{ServerName: serverName}
	if cfg.TLSConfig != nil && cfg.TLSConfig.RootCAs != nil {
		tlsConfig.RootCAs = cfg.TLSConfig.RootCAs.Clone()
	}
	return raceAddresses(ctx, addresses, func(attemptCtx context.Context, ip net.IPAddr) (net.Conn, error) {
		address := &net.UDPAddr{IP: ip.IP, Port: port, Zone: ip.Zone}
		return dialAddress(attemptCtx, cfg, target, address, tlsConfig)
	})
}

func dialAddress(ctx context.Context, cfg Config, target string, address net.Addr, tlsConfig hyclient.TLSConfig) (net.Conn, error) {
	factory := &packetFactory{mode: cfg.Obfuscation, password: cfg.Password}
	stop := context.AfterFunc(ctx, factory.close)
	defer stop()
	client, _, err := hyclient.NewClient(&hyclient.Config{
		ServerAddr:  address,
		Auth:        cfg.Password,
		TLSConfig:   tlsConfig,
		ConnFactory: factory,
		// Zero bandwidth keeps adaptive BBR. Chrome parroting remains enabled.
		FastOpen: false,
	})
	if err != nil {
		factory.close()
		return nil, setupError(ctx, err)
	}
	stream, err := client.TCP(target)
	if err != nil {
		_ = client.Close()
		factory.close()
		return nil, setupError(ctx, err)
	}
	conn := &ownedConn{Conn: stream, client: client, factory: factory}
	if !stop() || ctx.Err() != nil {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	return conn, nil
}

func validate(cfg Config, target string) (string, int, error) {
	if cfg.Password == "" {
		return "", 0, ErrPasswordRequired
	}
	switch cfg.Obfuscation {
	case "":
	case "salamander", "gecko":
		if len(cfg.Password) < 4 {
			return "", 0, errors.New("hysteria obfuscation password must contain at least four bytes")
		}
	default:
		return "", 0, errors.New("unsupported hysteria obfuscation")
	}
	if cfg.TLSConfig != nil && cfg.TLSConfig.InsecureSkipVerify {
		return "", 0, errors.New("hysteria TLS verification cannot be disabled")
	}
	host, port, err := splitEndpoint(cfg.Server)
	if err != nil {
		return "", 0, errors.New("invalid hysteria server address: expected host:port")
	}
	if _, _, err := splitEndpoint(target); err != nil {
		return "", 0, errors.New("invalid hysteria target address: expected host:port")
	}
	return host, port, nil
}

func splitEndpoint(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return "", 0, errors.New("missing host or port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("invalid port")
	}
	return host, port, nil
}

func setupError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var authErr hyerrors.AuthError
	if errors.As(err, &authErr) {
		return ErrAuthentication
	}
	return fmt.Errorf("hysteria connection: %w", err)
}

type ownedConn struct {
	net.Conn
	client  hyclient.Client
	factory *packetFactory
	once    sync.Once
	err     error
}

func (c *ownedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		_ = c.client.Close()
		c.factory.close()
	})
	return c.err
}
