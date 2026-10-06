package livekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

var (
	ErrNotConnected   = errors.New("LiveKit: нет активного подключения")
	ErrInvalidAddress = errors.New("LiveKit: укажите HTTPS-адрес, профиль livekit+vless:// или http://127.0.0.1:8787 для локального стенда")
	ErrBroker         = errors.New("LiveKit: сервер не ответил корректно")
	ErrAuthentication = errors.New("LiveKit: имя или пароль не приняты сервером")
	ErrStaleSession   = errors.New("LiveKit: комната изменилась; повторите действие")
	ErrMedia          = errors.New("LiveKit: не удалось установить медиасоединение")
)

type brokerAPI interface {
	login(context.Context, string, string) (api.LoginResponse, error)
	state(context.Context, string) (api.State, error)
	channel(context.Context, string, uint32) (api.LoginResponse, error)
	audio(context.Context, string, api.AudioState) (api.AudioState, error)
	screen(context.Context, string, api.ScreenRequest) (api.Grant, error)
	logout(context.Context, string)
	close()
}

type broker struct {
	base   string
	client *http.Client
}

func validGrant(g api.Grant, screen bool) bool {
	if _, err := mediaAddress(g.URL); err != nil {
		return false
	}
	id, role, ok := participantID(g.Identity)
	return ok && (role == "screen") == screen && id == g.SessionID && g.Token != "" && g.ChannelID <= 3 && g.Revision > 0 &&
		g.Room == "gul-channel-"+strconv.FormatUint(uint64(g.ChannelID), 10) &&
		g.OwnerIdentity == "voice."+strconv.FormatUint(uint64(g.SessionID), 10)
}

func newBroker(base string) *broker {
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
	return &broker{base: base, client: &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *broker) close() { b.client.CloseIdleConnections() }

func (b *broker) request(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return ErrBroker
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+"/api/gul"+path, reader)
	if err != nil {
		return ErrBroker
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := b.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrBroker
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return ErrAuthentication
	}
	if res.StatusCode == http.StatusConflict {
		return ErrStaleSession
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrBroker
	}
	if out == nil {
		return nil
	}
	limited := io.LimitReader(res.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	if decoder.Decode(out) != nil {
		return ErrBroker
	}
	return nil
}
func (b *broker) login(ctx context.Context, name, password string) (v api.LoginResponse, err error) {
	err = b.request(ctx, http.MethodPost, "/login", "", api.LoginRequest{Username: name, Password: password}, &v)
	return
}
func (b *broker) state(ctx context.Context, token string) (v api.State, err error) {
	err = b.request(ctx, http.MethodGet, "/state", token, nil, &v)
	return
}
func (b *broker) channel(ctx context.Context, token string, id uint32) (v api.LoginResponse, err error) {
	err = b.request(ctx, http.MethodPost, "/channel", token, api.ChannelRequest{ChannelID: id}, &v)
	return
}
func (b *broker) audio(ctx context.Context, token string, want api.AudioState) (v api.AudioState, err error) {
	err = b.request(ctx, http.MethodPost, "/audio", token, want, &v)
	return
}
func (b *broker) screen(ctx context.Context, token string, want api.ScreenRequest) (v api.Grant, err error) {
	err = b.request(ctx, http.MethodPost, "/screen", token, want, &v)
	return
}
func (b *broker) logout(ctx context.Context, token string) {
	_ = b.request(ctx, http.MethodPost, "/logout", token, nil, nil)
}
