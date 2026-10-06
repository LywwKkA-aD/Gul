package livekittransport

import (
	"net"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
	lk "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (g *Gateway) rewriteSignal(epoch uint64, kind int, data []byte, previous *string) ([]byte, error) {
	u, _ := url.Parse(g.profile.Origin)
	return transformSignal(kind, data, u.Hostname(), g.turn.Addr().String(), func(token string) error {
		if previous == nil {
			_, err := g.SignalURL(epoch, token)
			return err
		}
		err := g.refreshToken(epoch, *previous, token)
		if err == nil {
			*previous = token
		}
		return err
	})
}

func rewriteSignal(kind int, data []byte, host, turnAddress string) ([]byte, error) {
	return transformSignal(kind, data, host, turnAddress, nil)
}

func transformSignal(kind int, data []byte, host, turnAddress string, register func(string) error) ([]byte, error) {
	message := &lk.SignalResponse{}
	var err error
	switch kind {
	case websocket.BinaryMessage:
		err = proto.Unmarshal(data, message)
	case websocket.TextMessage:
		err = protojson.Unmarshal(data, message)
	default:
		return nil, ErrGateway
	}
	if err != nil {
		return nil, ErrGateway
	}
	if token := message.GetRefreshToken(); token != "" {
		if register == nil || register(token) != nil {
			return nil, ErrGateway
		}
		return data, nil
	}
	if message.GetRoomMoved() != nil {
		return nil, ErrGateway
	}
	if leave := message.GetLeave(); leave != nil {
		leave.Regions = nil
		return encodeSignal(kind, message)
	}
	var servers *[]*lk.ICEServer
	var config **lk.ClientConfiguration
	if join := message.GetJoin(); join != nil {
		if join.AlternativeUrl != "" {
			return nil, ErrGateway
		}
		servers, config = &join.IceServers, &join.ClientConfiguration
	} else if reconnect := message.GetReconnect(); reconnect != nil {
		servers, config = &reconnect.IceServers, &reconnect.ClientConfiguration
	} else {
		return data, nil
	}
	var relay *lk.ICEServer
	for _, server := range *servers {
		if server == nil {
			continue
		}
		for _, address := range server.Urls {
			if trustedTURN(address, host) && server.Username != "" && server.Credential != "" {
				relay = &lk.ICEServer{Urls: []string{"turn:" + turnAddress + "?transport=tcp"}, Username: server.Username, Credential: server.Credential}
				break
			}
		}
	}
	if relay == nil {
		return nil, ErrGateway
	}
	*servers = []*lk.ICEServer{relay}
	if *config == nil {
		*config = &lk.ClientConfiguration{}
	}
	(*config).ForceRelay = lk.ClientConfigSetting_ENABLED
	return encodeSignal(kind, message)
}
func encodeSignal(kind int, message *lk.SignalResponse) ([]byte, error) {
	if kind == websocket.BinaryMessage {
		return proto.Marshal(message)
	}
	return protojson.Marshal(message)
}

func trustedTURN(address, host string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "turns" || u.Fragment != "" || u.User != nil || u.Host != "" || u.Path != "" {
		return false
	}
	h, p, err := net.SplitHostPort(u.Opaque)
	if err != nil || !strings.EqualFold(h, host) || p != "443" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	return err == nil && (len(query) == 0 || (len(query) == 1 && len(query["transport"]) == 1 && query.Get("transport") == "tcp"))
}
