package reality

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	xreality "github.com/xtls/reality"
)

func startRealityFixture(t *testing.T) Config {
	t.Helper()
	decoy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "welcome") }))
	decoy.EnableHTTP2 = true
	decoy.Config.ErrorLog = log.New(io.Discard, "", 0)
	decoy.TLS = &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, SessionTicketsDisabled: true}
	decoy.StartTLS()
	t.Cleanup(decoy.Close)
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Server: listener.Addr().String(), ServerName: "example.com", PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), ShortID: "0102030405060708", Password: "fixture-random-looking-password"}
	serverConfig := &xreality.Config{
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		Dest:        decoy.Listener.Addr().String(), Type: "tcp", ServerNames: map[string]bool{cfg.ServerName: true},
		PrivateKey: key.Bytes(), ShortIds: map[[8]byte]bool{{1, 2, 3, 4, 5, 6, 7, 8}: true}, SessionTicketsDisabled: true,
	}
	// The local decoy deliberately emits no session tickets. Supply that known
	// fixture property instead of launching upstream's background probe loops.
	for alpn := range 3 {
		cacheKey := serverConfig.Dest + " " + cfg.ServerName + " " + strconv.Itoa(alpn)
		xreality.GlobalPostHandshakeRecordsLens.Store(cacheKey, []int{})
		t.Cleanup(func() { xreality.GlobalPostHandshakeRecordsLens.Delete(cacheKey) })
	}
	var mu sync.Mutex
	var sockets []net.Conn
	var handlers sync.WaitGroup
	acceptDone := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, socket := range sockets {
			_ = socket.Close()
		}
		mu.Unlock()
		handlers.Wait()
	})
	go func() {
		defer close(acceptDone)
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			sockets = append(sockets, raw)
			mu.Unlock()
			handlers.Go(func() {
				defer raw.Close()
				_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
				secured, err := xreality.Server(context.Background(), raw, serverConfig)
				if err != nil {
					return
				}
				defer secured.Close()
				serveVLESSFixture(secured, cfg.Password)
			})
		}
	}()
	return cfg
}

// This fixed-protocol peer runs over the official REALITY server. The separate
// live Mumble tests verify interoperability with the full official Xray server.
func serveVLESSFixture(conn net.Conn, password string) {
	var request [26]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return
	}
	id, _ := hex.DecodeString(strings.ReplaceAll(UserID(password), "-", ""))
	if request[0] != 0 || !bytes.Equal(request[1:17], id) || request[17] != 0 || request[18] != 1 || request[21] != 1 {
		return
	}
	address := net.IP(request[22:])
	if !address.IsLoopback() {
		return
	}
	backend, err := net.DialTimeout("tcp", net.JoinHostPort(address.String(), strconv.Itoa(int(binary.BigEndian.Uint16(request[19:21])))), time.Second)
	if err != nil {
		return
	}
	defer backend.Close()
	if _, err := conn.Write([]byte{0, 0}); err != nil {
		return
	}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(backend, conn); _ = backend.Close(); close(copied) }()
	_, _ = io.Copy(conn, backend)
	_ = conn.Close()
	<-copied
}

func startEchoFixture(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer close(closed)
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	return listener.Addr().String(), closed
}
