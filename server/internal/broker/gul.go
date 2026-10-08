package broker

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
	"github.com/LywwKkA-aD/Gul/server/internal/catalog"
)

const (
	gulSessionLease = 60 * time.Second
	gulMaxSessions  = 128
)

type gulSession struct {
	opMu        sync.Mutex
	ID          uint32
	Name        string
	ChannelID   uint32
	Revision    uint64
	Audio       api.AudioState
	ExpiresAt   time.Time
	Revoked     bool
	Moving      bool
	MemberID    string
	AuthVersion uint64
	Nonce       string
}

// gulBroker manages authenticated logical sessions and their media grants.
// Only the trusted HTTPS proxy exposes these state operations to clients.
type gulBroker struct {
	mu              sync.Mutex
	cfg             credentials
	now             func() time.Time
	newID           func() uint32
	sessions        map[[32]byte]*gulSession
	serverURL       string
	grantLifetime   time.Duration
	maxSessions     int
	passwordHash    *[32]byte
	remover         ParticipantRemover
	store           *catalog.Store
	closing         map[uint32]bool
	flows           map[*signalFlow]struct{}
	closed          bool
	signalTransport http.RoundTripper
}

func newGulBroker(cfg credentials, now func() time.Time) *gulBroker {
	return &gulBroker{
		cfg: cfg, now: now, newID: randomSessionID,
		sessions:      make(map[[32]byte]*gulSession),
		grantLifetime: 90 * time.Second, maxSessions: gulMaxSessions,
	}
}

func randomSessionID() uint32 {
	var data [4]byte
	// crypto/rand.Read either fills the buffer or terminates the process in
	// our pinned Go toolchain; it never returns a recoverable short read.
	_, _ = rand.Read(data[:])
	return binary.BigEndian.Uint32(data[:]) & 0x7fffffff
}

func (b *gulBroker) nextIDLocked() uint32 {
	for {
		id := b.newID()
		if id == 0 || id > 0x7fffffff {
			continue
		}
		used := false
		for _, session := range b.sessions {
			if session.ID == id {
				used = true
				break
			}
		}
		if !used {
			return id
		}
	}
}

func (b *gulBroker) expireLocked(now time.Time) {
	for key, session := range b.sessions {
		if !now.Before(session.ExpiresAt) {
			if b.remover == nil {
				delete(b.sessions, key)
			} else {
				session.Revoked = true
				b.cancelFlowsLocked(session)
			}
		}
	}
}

func sessionToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func voiceIdentity(id uint32) string { return "voice." + strconv.FormatUint(uint64(id), 10) }

func (b *gulBroker) responseLocked(session *gulSession, token string, now time.Time) api.LoginResponse {
	response := api.LoginResponse{
		SessionToken: token, SessionID: session.ID, Identity: voiceIdentity(session.ID),
		Name: session.Name, ChannelID: session.ChannelID, Revision: session.Revision,
		Grant: b.grantLocked(session, "voice", now),
	}
	if b.store != nil {
		state := b.store.Snapshot()
		response.ServerID = state.ServerID
		response.CatalogVersion = state.CatalogVersion
		response.Member = b.memberInfoLocked(state, session)
	}
	return response
}

func (b *gulBroker) stateLocked(self *gulSession) api.State {
	if b.store != nil {
		return b.managedStateLocked(self)
	}
	root := api.ChannelNode{
		ID: 0, Name: "Gul LiveKit", Users: []api.UserInfo{}, Children: []api.ChannelNode{
			{ID: 1, Name: "Общая", Position: 0, Users: []api.UserInfo{}, Children: []api.ChannelNode{}},
			{ID: 2, Name: "Игра", Position: 1, Users: []api.UserInfo{}, Children: []api.ChannelNode{}},
			{ID: 3, Name: "AFK", Position: 2, Users: []api.UserInfo{}, Children: []api.ChannelNode{}},
		},
	}
	for _, session := range b.sessions {
		if session.Revoked {
			continue
		}
		user := api.UserInfo{
			Session: session.ID, Key: "s:livekit:" + strconv.FormatUint(uint64(session.ID), 10),
			Name: session.Name, ChannelID: session.ChannelID,
			SelfMute: session.Audio.Muted, SelfDeaf: session.Audio.Deafened, IsSelf: session.ID == self.ID,
		}
		if session.ChannelID == 0 {
			root.Users = append(root.Users, user)
		} else {
			index := int(session.ChannelID) - 1
			root.Children[index].Users = append(root.Children[index].Users, user)
		}
	}
	compare := func(a, b api.UserInfo) int {
		if a.Session < b.Session {
			return -1
		}
		if a.Session > b.Session {
			return 1
		}
		return 0
	}
	slices.SortFunc(root.Users, compare)
	for i := range root.Children {
		slices.SortFunc(root.Children[i].Users, compare)
	}
	return api.State{Tree: root, SelfSession: self.ID, SelfChannel: self.ChannelID, Revision: self.Revision}
}

func tokenKey(token string) ([32]byte, bool) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) != 32 || len(token) != 43 {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}
