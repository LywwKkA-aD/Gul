package livekittransport

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/reality"
	"github.com/gorilla/websocket"
	lk "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func upstreamGateway(t *testing.T, handler http.Handler) (*Gateway, *atomic.Int32) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	g := testGatewayHost(t, "127.0.0.1")
	g.roots = x509.NewCertPool()
	g.roots.AddCert(server.Certificate())
	count := &atomic.Int32{}
	g.dial = func(ctx context.Context, _ reality.Config) (net.Conn, error) {
		count.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	return g, count
}

func TestGatewayTLSAuthorityAndValidationRedirect(t *testing.T) {
	g, count := upstreamGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rtc/validate" {
			http.Redirect(w, r, "https://elsewhere.example", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	for _, addr := range []string{"elsewhere.example:443", "127.0.0.1:8443"} {
		if c, err := g.dialTLS(context.Background(), "tcp", addr); err == nil {
			c.Close()
			t.Fatal("foreign authority dialed")
		}
	}
	if count.Load() != 0 {
		t.Fatal("invalid authority reached REALITY dial")
	}
	client := &http.Client{Transport: g.Transport(), Timeout: time.Second}
	res, err := client.Get("https://127.0.0.1/healthz")
	if err != nil {
		t.Fatal("verified TLS failed")
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if count.Load() != 1 {
		t.Fatal("unexpected tunnel count")
	}
	if err := g.BeginEpoch(8); err != nil {
		t.Fatal(err)
	}
	res, err = client.Get("https://127.0.0.1/healthz")
	if err != nil {
		t.Fatal("epoch closed broker transport")
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if count.Load() != 1 {
		t.Fatal("broker pool was not preserved")
	}
	address, _ := g.SignalURL(8, "fixture-token")
	req, _ := http.NewRequest("GET", strings.Replace(address, "ws:", "http:", 1)+"/rtc/validate", nil)
	req.Header.Set("Authorization", "Bearer fixture-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("local validation transport failed")
	}
	_ = res.Body.Close()
	if res.StatusCode != 502 || res.Header.Get("Location") != "" {
		t.Fatal("upstream redirect escaped gateway")
	}
	g.Close()
	if _, err := g.dialTLS(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("closed gateway dialed")
	}
}

func TestGatewayInnerTLSCannotDisableCertificateVerification(t *testing.T) {
	g, _ := upstreamGateway(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	g.roots = nil
	if conn, err := g.dialTLS(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		conn.Close()
		t.Fatal("untrusted inner TLS certificate accepted")
	}
}

func TestGatewayWebsocketBidirectionalRewriteRefreshAndEpochClose(t *testing.T) {
	received := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	g, _ := upstreamGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rtc" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.URL.Query().Has("access_token") {
			http.Error(w, "bad request", 400)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		join := &lk.SignalResponse{Message: &lk.SignalResponse_Join{Join: &lk.JoinResponse{IceServers: []*lk.ICEServer{{Urls: []string{"turns:127.0.0.1:443?transport=tcp"}, Username: "fixture", Credential: "fixture"}}}}}
		data, _ := proto.Marshal(join)
		_ = ws.WriteMessage(websocket.BinaryMessage, data)
		_, data, err = ws.ReadMessage()
		if err != nil {
			return
		}
		received <- data
		refresh, _ := proto.Marshal(&lk.SignalResponse{Message: &lk.SignalResponse_RefreshToken{RefreshToken: "refreshed-fixture"}})
		_ = ws.WriteMessage(websocket.BinaryMessage, refresh)
		_, _, _ = ws.ReadMessage()
	}))
	address, _ := g.SignalURL(7, "fixture-token")
	ws, res, err := websocket.DefaultDialer.Dial(address+"/rtc?access_token=fixture-token", nil)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		t.Fatal("authenticated WebSocket upgrade failed")
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, data, err := ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage {
		t.Fatal("Join proxy failed")
	}
	join := &lk.SignalResponse{}
	_ = proto.Unmarshal(data, join)
	if join.GetJoin().GetClientConfiguration().GetForceRelay() != lk.ClientConfigSetting_ENABLED {
		t.Fatal("relay policy not rewritten")
	}
	request := []byte{8, 1}
	if err := ws.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-received:
		if string(data) != string(request) {
			t.Fatal("client signal changed")
		}
	case <-time.After(time.Second):
		t.Fatal("client signal was not forwarded")
	}
	_, _, err = ws.ReadMessage()
	if err != nil {
		t.Fatal("refresh proxy failed")
	}
	req := httptest.NewRequest("GET", strings.Replace(address, "ws:", "http:", 1)+"/rtc?access_token=refreshed-fixture", nil)
	if _, _, ok := g.authorize(req); !ok {
		t.Fatal("refreshed token cannot reconnect")
	}
	if err := g.BeginEpoch(8); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("old epoch signal stayed open")
	}
}

func TestGatewayTURNRejectsHTTPAndEpochClosesAllocation(t *testing.T) {
	g := testGatewayHost(t, "127.0.0.1")
	var calls atomic.Int32
	g.dial = func(context.Context, reality.Config) (net.Conn, error) { calls.Add(1); return nil, ErrGateway }
	conn, err := net.DialTimeout("tcp", g.turn.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	_ = conn.Close()
	if err == nil || calls.Load() != 0 {
		t.Fatal("non-TURN traffic escaped fixed gateway")
	}
	conn, err = net.DialTimeout("tcp", g.turn.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	header := make([]byte, 20)
	binary.BigEndian.PutUint16(header[:2], 3)
	binary.BigEndian.PutUint32(header[4:8], 0x2112a442)
	_, _ = conn.Write(header[:8])
	if err := g.BeginEpoch(8); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("old TURN handshake remained open")
	}
}

func TestGatewayRepeatedGrantsStayBoundedAndVoiceTokenSurvives(t *testing.T) {
	g := testGateway(t)
	address, _ := g.SignalURL(7, "voice-fixture")
	if err := g.refreshToken(7, "voice-fixture", "voice-refreshed"); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		if _, err := g.SignalURL(7, fmt.Sprintf("screen-fixture-%d", i)); err != nil {
			t.Fatal("legitimate screen retries exhausted gateway")
		}
	}
	g.mu.Lock()
	count := len(g.tokens)
	g.mu.Unlock()
	if count > 32 {
		t.Fatal("token storage unbounded")
	}
	r := httptest.NewRequest("GET", strings.Replace(address, "ws:", "http:", 1)+"/rtc?access_token=voice-refreshed", nil)
	if _, _, ok := g.authorize(r); !ok {
		t.Fatal("screen retry evicted native voice")
	}
	for i := range 20 {
		if err := g.AllowOrigin(7, fmt.Sprintf("http://127.0.0.1:%d", 41000+i)); err != nil {
			t.Fatal("companion restart exhausted gateway")
		}
	}
	if err := g.AllowOrigin(7, "http://127.0.0.1:41019"); err != nil {
		t.Fatal("existing origin rejected at bound")
	}
}
