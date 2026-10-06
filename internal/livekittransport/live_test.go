//go:build live

package livekittransport

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"github.com/pion/logging"
	"github.com/pion/turn/v5"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/reality"
	"github.com/gorilla/websocket"
	lk "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestRealityGatewayLiveSignal(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_REALITY") != "1" {
		t.Skip("explicit REALITY fixture opt-in required")
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(os.Getenv(name))
		if err != nil {
			t.Fatal("fixture file unavailable")
		}
		return strings.TrimSpace(string(data))
	}
	p, err := reality.ParseLiveKitProfile(read("GUL_LIVEKIT_ADDRESS_FILE"))
	if err != nil {
		t.Fatal("fixture profile invalid")
	}
	var roots *x509.CertPool
	if path := os.Getenv("GUL_LIVEKIT_CA_FILE"); path != "" {
		data, err := os.ReadFile(path)
		roots = x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(data) {
			t.Fatal("fixture CA unavailable")
		}
	}
	g, err := New(p, read("GUL_LIVEKIT_PASSWORD_FILE"), Options{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	client := &http.Client{Transport: g.Transport(), Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(p.Origin + "/healthz")
	if err != nil {
		t.Fatal("gateway HTTPS health transport failed")
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("gateway HTTPS health rejected")
	}
	t.Log("verified HTTPS over REALITY passed")
	body, _ := json.Marshal(api.LoginRequest{Username: "gateway-signal-fixture", Password: read("GUL_LIVEKIT_PASSWORD_FILE")})
	res, err = client.Post(p.Origin+"/api/gul/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal("gateway broker login transport failed")
	}
	var login api.LoginResponse
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&login)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != 200 || login.Grant.Token == "" {
		t.Fatal("gateway broker login failed")
	}
	t.Log("authenticated broker over REALITY passed")
	defer func() {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", p.Origin+"/api/gul/logout", nil)
		req.Header.Set("Authorization", "Bearer "+login.SessionToken)
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
		}
	}()
	if err := g.BeginEpoch(1); err != nil {
		t.Fatal(err)
	}
	address, err := g.SignalURL(1, login.Grant.Token)
	if err != nil {
		t.Fatal(err)
	}
	ws, _, err := websocket.DefaultDialer.Dial(address+"/rtc?protocol=16&auto_subscribe=0", http.Header{"Authorization": {"Bearer " + login.Grant.Token}})
	if err != nil {
		t.Fatal("gateway signaling upgrade failed")
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatal("gateway signaling Join failed")
	}
	message := &lk.SignalResponse{}
	if kind != websocket.BinaryMessage || proto.Unmarshal(data, message) != nil || message.GetJoin() == nil {
		t.Fatal("gateway expected binary Join")
	}
	join := message.GetJoin()
	if len(join.IceServers) != 1 || join.IceServers[0].Urls[0] != "turn:"+g.turn.Addr().String()+"?transport=tcp" {
		t.Fatal("gateway Join relay rewrite failed")
	}
	t.Log("authenticated signaling Join and forced relay rewrite passed")
	conn, err := net.DialTimeout("tcp", g.turn.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal("local TURN listener unavailable")
	}
	defer conn.Close()
	factory := logging.NewDefaultLoggerFactory()
	factory.Writer = io.Discard
	turnClient, err := turn.NewClient(&turn.ClientConfig{TURNServerAddr: g.turn.Addr().String(), Username: join.IceServers[0].Username, Password: join.IceServers[0].Credential, Conn: turn.NewSTUNConn(conn), LoggerFactory: factory, RTO: 100 * time.Millisecond})
	if err != nil {
		t.Fatal("TURN client setup failed")
	}
	defer turnClient.Close()
	if err := turnClient.Listen(); err != nil {
		t.Fatal("TURN read setup failed")
	}
	relay, err := turnClient.Allocate()
	if err != nil {
		t.Fatal("authenticated TURN allocation over REALITY failed")
	}
	defer relay.Close()
	t.Log("authenticated TURN allocation over REALITY passed")
}
