// Package catalog persists server-authored member identity and channel policy.
package catalog

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxChannels = 64
const MaxMembers = 128
const MaxAllowedMembers = 64
const MaxInvites = 256
const MaxVersion = 1<<53 - 1

type Member struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	CredentialHash string `json:"credentialHash"`
	AuthVersion    uint64 `json:"authVersion"`
	Revoked        bool   `json:"revoked"`
}
type Channel struct {
	ID               uint32   `json:"id"`
	Name             string   `json:"name"`
	Position         int32    `json:"position"`
	Version          uint64   `json:"version"`
	Access           string   `json:"access"`
	AllowedMemberIDs []string `json:"allowedMemberIds"`
}
type Invite struct {
	Digest     string `json:"digest"`
	ExpiresAt  int64  `json:"expiresAtUnixSeconds"`
	ConsumedBy string `json:"consumedBy,omitempty"`
}
type State struct {
	SchemaVersion  int       `json:"schemaVersion"`
	ServerID       string    `json:"serverId"`
	CatalogVersion uint64    `json:"catalogVersion"`
	NextChannelID  uint32    `json:"nextChannelId"`
	Members        []Member  `json:"members"`
	Channels       []Channel `json:"channels"`
	Invites        []Invite  `json:"invites"`
}
type OwnerKey struct {
	Format     string `json:"format"`
	ServerID   string `json:"serverId"`
	MemberID   string `json:"memberId"`
	Credential string `json:"credential"`
}

func RandomID() string {
	var data [16]byte
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}
func RandomCredential() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}
func ValidID(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 16 && value == hex.EncodeToString(data)
}
func ValidCredential(value string) bool {
	data, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(data) == 32 && len(value) == 43 && value == base64.RawURLEncoding.EncodeToString(data)
}
func Digest(kind, serverID, secret string) string {
	hash := sha256.Sum256([]byte("gul/" + kind + "/v1\x00" + serverID + "\x00" + secret))
	return hex.EncodeToString(hash[:])
}
func ValidName(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value == "" || utf8.RuneCountInString(value) > 64 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}
func (s State) Authenticate(credential string) (Member, bool) {
	if !ValidCredential(credential) {
		return Member{}, false
	}
	hash := Digest("member", s.ServerID, credential)
	for _, member := range s.Members {
		if !member.Revoked && subtle.ConstantTimeCompare([]byte(member.CredentialHash), []byte(hash)) == 1 {
			return member, true
		}
	}
	return Member{}, false
}
func (s State) Channel(id uint32) (Channel, bool) {
	for _, channel := range s.Channels {
		if channel.ID == id {
			return channel, true
		}
	}
	return Channel{}, false
}
func (s State) Member(id string) (Member, bool) {
	for _, member := range s.Members {
		if member.ID == id {
			return member, true
		}
	}
	return Member{}, false
}
func (s State) CanJoin(memberID string, channelID uint32) bool {
	channel, exists := s.Channel(channelID)
	if !exists {
		return false
	}
	member, known := s.Member(memberID)
	if known && member.Revoked {
		return false
	}
	return channel.Access == "open" || known && (member.Role == "owner" || slices.Contains(channel.AllowedMemberIDs, memberID))
}
func (s State) clone() State {
	s.Members = slices.Clone(s.Members)
	s.Invites = slices.Clone(s.Invites)
	s.Channels = slices.Clone(s.Channels)
	for i := range s.Channels {
		s.Channels[i].AllowedMemberIDs = slices.Clone(s.Channels[i].AllowedMemberIDs)
	}
	return s
}
func validHash(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && value == hex.EncodeToString(data)
}
func (s State) Validate() error {
	invalid := errors.New("invalid private channel catalogue")
	if s.SchemaVersion != 1 || !ValidID(s.ServerID) || s.CatalogVersion == 0 || s.CatalogVersion > MaxVersion || len(s.Members) < 1 || len(s.Members) > MaxMembers || len(s.Channels) < 2 || len(s.Channels) > MaxChannels || len(s.Invites) > MaxInvites {
		return invalid
	}
	members, hashes := map[string]bool{}, map[string]bool{}
	owners := 0
	for _, member := range s.Members {
		if !ValidID(member.ID) || members[member.ID] || !ValidName(member.Name) || !validHash(member.CredentialHash) || hashes[member.CredentialHash] || member.AuthVersion == 0 || member.AuthVersion > MaxVersion || (member.Role != "owner" && member.Role != "member") {
			return invalid
		}
		if member.Role == "owner" {
			owners++
			if member.Revoked {
				return invalid
			}
		}
		members[member.ID], hashes[member.CredentialHash] = true, true
	}
	if owners != 1 {
		return invalid
	}
	channels := map[uint32]bool{}
	for _, channel := range s.Channels {
		if channels[channel.ID] || channel.ID > 0x7fffffff || channel.ID >= s.NextChannelID || !ValidName(channel.Name) || channel.Version == 0 || channel.Version > MaxVersion || channel.Position < 0 || (channel.Access != "open" && channel.Access != "restricted") || len(channel.AllowedMemberIDs) > MaxAllowedMembers || (channel.Access == "open" && len(channel.AllowedMemberIDs) != 0) || (channel.ID <= 1 && channel.Access != "open") {
			return invalid
		}
		channels[channel.ID] = true
		allowed := map[string]bool{}
		for _, id := range channel.AllowedMemberIDs {
			if !members[id] || allowed[id] {
				return invalid
			}
			allowed[id] = true
		}
	}
	if !channels[0] || !channels[1] || s.NextChannelID > 0x80000000 {
		return invalid
	}
	invites := map[string]bool{}
	for _, invite := range s.Invites {
		if !validHash(invite.Digest) || invites[invite.Digest] || invite.ExpiresAt <= 0 || (invite.ConsumedBy != "" && !members[invite.ConsumedBy]) {
			return invalid
		}
		invites[invite.Digest] = true
	}
	return nil
}
