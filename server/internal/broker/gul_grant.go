package broker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
)

func (b *gulBroker) grantLocked(session *gulSession, role string, now time.Time) api.Grant {
	id := strconv.FormatUint(uint64(session.ID), 10)
	channel := strconv.FormatUint(uint64(session.ChannelID), 10)
	grant := api.Grant{
		URL: b.serverURL, Identity: role + "." + id, Room: "gul-channel-" + channel,
		OwnerIdentity: voiceIdentity(session.ID), SessionID: session.ID,
		ChannelID: session.ChannelID, Revision: session.Revision,
	}
	sources := []string{"microphone"}
	if role == "screen" {
		sources = []string{"screen_share", "screen_share_audio"}
	}
	attributes := map[string]string{"ownerIdentity": grant.OwnerIdentity, "role": role, "sessionId": id, "channelId": channel, "revision": strconv.FormatUint(session.Revision, 10)}
	if b.store != nil {
		attributes["serverId"] = b.store.Snapshot().ServerID
		attributes["memberId"] = session.MemberID
		attributes["authVersion"] = strconv.FormatUint(session.AuthVersion, 10)
		attributes["sessionNonce"] = session.Nonce
	}
	// Only server-authored attributes bind a screen companion to its voice
	// owner. Tokens cannot change metadata or obtain room administration.
	claims, _ := json.Marshal(map[string]any{
		"iss": b.cfg.APIKey, "sub": grant.Identity, "name": session.Name,
		"iat": now.Unix(), "nbf": now.Add(-10 * time.Second).Unix(), "exp": now.Add(b.grantLifetime).Unix(),
		"attributes": attributes,
		"video": map[string]any{
			"roomJoin": true, "room": grant.Room,
			"canPublish": true, "canSubscribe": true, "canPublishData": role == "voice",
			"canPublishSources": sources, "canUpdateOwnMetadata": false,
		},
	})
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(b.cfg.APISecret))
	_, _ = mac.Write([]byte(unsigned))
	grant.Token = unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return grant
}
