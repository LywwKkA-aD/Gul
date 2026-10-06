// Package screensharelab connects the opt-in native lab to its loopback broker.
// It is deliberately independent of the production voice engine and settings.
package screensharelab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Grant struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Identity string `json:"identity"`
	Room     string `json:"room"`
}

type Client struct {
	http     *http.Client
	endpoint string
}

func NewClient() *Client {
	return &Client{
		endpoint: "http://127.0.0.1:8787/api/livekit/token",
		http: &http.Client{
			Timeout:       5 * time.Second,
			Transport:     &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) Join(ctx context.Context, identity string) (Grant, error) {
	body, _ := json.Marshal(struct {
		Identity string `json:"identity"`
		Room     string `json:"room"`
	}{identity, "gul-local"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Grant{}, errors.New("local LiveKit broker request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Grant{}, errors.New("local LiveKit broker unavailable; start scripts/livekit-local.sh")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Grant{}, errors.New("local LiveKit broker refused the request")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	var grant Grant
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &grant) != nil ||
		grant.URL != "ws://127.0.0.1:7880" || grant.Room != "gul-local" || grant.Token == "" || grant.Identity != identity {
		return Grant{}, errors.New("invalid local LiveKit grant")
	}
	return grant, nil
}
