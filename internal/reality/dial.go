package reality

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dial authenticates a REALITY server, then opens VLESS TCP to Gul's Mumble
// service. The caller's Mumble TLS handshake additionally verifies VLESS
// authorization and end-to-end connectivity. Context bounds setup only.
func Dial(ctx context.Context, cfg Config, target string) (net.Conn, error) {
	if target != "127.0.0.1:64738" {
		return nil, errors.New("VLESS target must be the local Mumble service")
	}
	return dialTarget(ctx, cfg, target)
}

// DialLiveKit permits only the server's HTTPS/TURN TLS multiplexer. It cannot
// turn a client profile into an arbitrary VLESS proxy destination.
func DialLiveKit(ctx context.Context, cfg Config) (net.Conn, error) {
	return dialTarget(ctx, cfg, "127.0.0.1:443")
}

func dialTarget(ctx context.Context, cfg Config, target string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed, err := validate(cfg)
	if err != nil {
		return nil, err
	}
	header, err := requestHeader(cfg.Password, target)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	raw, err := dialer.DialContext(ctx, "tcp", cfg.Server)
	if err != nil {
		return nil, setupError(ctx, "connect to REALITY server")
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	secured, err := handshake(ctx, raw, cfg.ServerName, parsed)
	if err != nil {
		_ = raw.Close()
		if ctxErr := setupContextError(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, ErrAuthentication) {
			return nil, ErrAuthentication
		}
		return nil, errors.New("REALITY TLS handshake failed")
	}
	// Never send the derived user ID until REALITY's keyed certificate verifies.
	if _, err := secured.Write(header); err != nil {
		_ = raw.Close()
		return nil, setupError(ctx, "write VLESS request")
	}
	if !stop() || ctx.Err() != nil {
		_ = raw.Close()
		return nil, ctx.Err()
	}
	if err := secured.SetDeadline(time.Time{}); err != nil {
		_ = raw.Close()
		return nil, errors.New("clear REALITY setup deadline")
	}
	return &vlessConn{Conn: secured}, nil
}

func setupError(ctx context.Context, message string) error {
	if err := setupContextError(ctx); err != nil {
		return err
	}
	return errors.New(message)
}

func setupContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The socket's deadline can fire just before the context's timer callback.
	// Preserve deadline classification whichever timer the scheduler runs first.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// requestHeader implements the fixed VLESS v0 TCP/IPv4 wire format. The preset
// has empty addons (no Vision), no mux, no UDP and no extra encryption.
func requestHeader(password, target string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	port, portErr := strconv.Atoi(portText)
	ip := net.ParseIP(host).To4()
	if err != nil || portErr != nil || port < 1 || port > 65535 || ip == nil || !ip.IsLoopback() {
		return nil, errors.New("invalid local VLESS target")
	}
	header := make([]byte, 26)
	id, _ := hex.DecodeString(strings.ReplaceAll(UserID(password), "-", ""))
	copy(header[1:17], id)
	header[18] = 1 // TCP command; byte 17 is the zero addon length.
	binary.BigEndian.PutUint16(header[19:21], uint16(port))
	header[21] = 1 // IPv4 address type.
	copy(header[22:], ip)
	return header, nil
}

type vlessConn struct {
	net.Conn
	readMu      sync.Mutex
	header      [2]byte
	read        int
	protocolErr error
}

func (c *vlessConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.protocolErr != nil {
		return 0, c.protocolErr
	}
	if c.read < len(c.header) {
		n, err := io.ReadFull(c.Conn, c.header[c.read:])
		c.read += n
		if err != nil {
			if errors.Is(err, io.EOF) && c.read != 0 {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if c.header != [2]byte{0, 0} {
			c.protocolErr = ErrProtocol
			_ = c.Close()
			return 0, ErrProtocol
		}
	}
	return c.Conn.Read(p)
}
