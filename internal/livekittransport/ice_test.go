package livekittransport

import (
	"crypto/sha256"
	"testing"

	"github.com/gorilla/websocket"
	lk "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestSignalRewritesOnlyAuthenticatedTURNToLoopbackAndForcesRelay(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, reconnect := range []bool{false, true} {
			servers := []*lk.ICEServer{{Urls: []string{"stun:public.example:7882"}}, {Urls: []string{"turns:public.example:443?transport=tcp"}, Username: "fixture-user", Credential: "fixture-password"}}
			response := &lk.SignalResponse{}
			if reconnect {
				response.Message = &lk.SignalResponse_Reconnect{Reconnect: &lk.ReconnectResponse{IceServers: servers}}
			} else {
				response.Message = &lk.SignalResponse_Join{Join: &lk.JoinResponse{IceServers: servers}}
			}
			var data []byte
			kind := websocket.TextMessage
			if binary {
				kind = websocket.BinaryMessage
				data, _ = proto.Marshal(response)
			} else {
				data, _ = protojson.Marshal(response)
			}
			got, err := rewriteSignal(kind, data, "public.example", "127.0.0.1:45000")
			if err != nil {
				t.Fatal(err)
			}
			decoded := &lk.SignalResponse{}
			if binary {
				err = proto.Unmarshal(got, decoded)
			} else {
				err = protojson.Unmarshal(got, decoded)
			}
			if err != nil {
				t.Fatal(err)
			}
			var ice []*lk.ICEServer
			var config *lk.ClientConfiguration
			if reconnect {
				ice, config = decoded.GetReconnect().IceServers, decoded.GetReconnect().ClientConfiguration
			} else {
				ice, config = decoded.GetJoin().IceServers, decoded.GetJoin().ClientConfiguration
			}
			if len(ice) != 1 || len(ice[0].Urls) != 1 || ice[0].Urls[0] != "turn:127.0.0.1:45000?transport=tcp" || ice[0].Username != "fixture-user" || ice[0].Credential != "fixture-password" || config.GetForceRelay() != lk.ClientConfigSetting_ENABLED {
				t.Fatal("signal did not preserve TURN auth while removing direct paths")
			}
		}
	}
}

func TestSignalWithoutTrustedTURNFailsClosed(t *testing.T) {
	for _, address := range []string{"stun:public.example:7882", "turn:public.example:443", "turns:elsewhere.example:443", "turns:public.example:5349", "turns:public.example:443?transport=udp"} {
		response := &lk.SignalResponse{Message: &lk.SignalResponse_Join{Join: &lk.JoinResponse{IceServers: []*lk.ICEServer{{Urls: []string{address}, Username: "fixture", Credential: "fixture"}}}}}
		data, _ := proto.Marshal(response)
		if _, err := rewriteSignal(websocket.BinaryMessage, data, "public.example", "127.0.0.1:45000"); err == nil {
			t.Fatal("signal allowed an unsupported direct or unrelated ICE route")
		}
	}
}

func TestSignalCannotRedirectAndRefreshIsEpochBound(t *testing.T) {
	g := testGateway(t)
	refresh := &lk.SignalResponse{Message: &lk.SignalResponse_RefreshToken{RefreshToken: "refreshed-fixture"}}
	data, _ := proto.Marshal(refresh)
	if _, err := g.rewriteSignal(7, websocket.BinaryMessage, data, nil); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	_, registered := g.tokens[sha256.Sum256([]byte("refreshed-fixture"))]
	g.mu.Unlock()
	if !registered {
		t.Fatal("authenticated refresh token not registered")
	}
	if err := g.BeginEpoch(8); err != nil {
		t.Fatal(err)
	}
	if _, err := g.rewriteSignal(7, websocket.BinaryMessage, data, nil); err == nil {
		t.Fatal("late refresh token accepted")
	}
	redirect := &lk.SignalResponse{Message: &lk.SignalResponse_Join{Join: &lk.JoinResponse{AlternativeUrl: "wss://elsewhere.example"}}}
	data, _ = proto.Marshal(redirect)
	if _, err := g.rewriteSignal(8, websocket.BinaryMessage, data, nil); err == nil {
		t.Fatal("alternate signal route permitted")
	}
	leave := &lk.SignalResponse{Message: &lk.SignalResponse_Leave{Leave: &lk.LeaveRequest{Action: lk.LeaveRequest_RECONNECT, Regions: &lk.RegionSettings{Regions: []*lk.RegionInfo{{Url: "wss://elsewhere.example"}}}}}}
	data, _ = proto.Marshal(leave)
	got, err := g.rewriteSignal(8, websocket.BinaryMessage, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &lk.SignalResponse{}
	_ = proto.Unmarshal(got, decoded)
	if decoded.GetLeave().GetRegions() != nil {
		t.Fatal("server region bypass survived")
	}
}
