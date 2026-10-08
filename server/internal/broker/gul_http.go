package broker

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LywwKkA-aD/Gul/server/internal/api"
)

func (b *gulBroker) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/gul/login", b.login)
	mux.HandleFunc("/api/gul/state", b.state)
	mux.HandleFunc("/api/gul/channel", b.channel)
	mux.HandleFunc("/api/gul/audio", b.audio)
	mux.HandleFunc("/api/gul/screen", b.screen)
	mux.HandleFunc("/api/gul/logout", b.logout)
	mux.HandleFunc("/api/gul/info", b.info)
	if b.store != nil {
		b.registerManagement(mux)
	}
}

func (b *gulBroker) login(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	var input api.LoginRequest
	if !gulJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Username)
	if b.passwordHash != nil {
		hash := sha256.Sum256([]byte(input.Password))
		valid := subtle.ConstantTimeCompare(hash[:], b.passwordHash[:]) == 1
		if !valid || len(input.Password) < 16 || len(input.Password) > 256 {
			http.Error(w, "authentication failed", http.StatusUnauthorized)
			return
		}
	} else if input.Password != "" {
		http.Error(w, "local broker requires an empty password", http.StatusBadRequest)
		return
	}
	if !validGulName(name) {
		http.Error(w, "valid username required", http.StatusBadRequest)
		return
	}
	now := b.now()
	b.mu.Lock()
	b.expireLocked(now)
	if b.closed {
		b.mu.Unlock()
		gulCode(w, 503, "storage_unavailable")
		return
	}
	memberID, authVersion, status := b.loginMemberLocked(input, name)
	if status != 0 {
		b.mu.Unlock()
		if status == 426 {
			gulCode(w, status, "upgrade_required")
		} else {
			gulCode(w, status, "authentication_failed")
		}
		return
	}
	if len(b.sessions) >= b.maxSessions {
		b.mu.Unlock()
		http.Error(w, "local session limit reached", http.StatusTooManyRequests)
		return
	}
	var token string
	var key [32]byte
	for {
		token = sessionToken()
		key = sha256.Sum256([]byte(token))
		if b.sessions[key] == nil {
			break
		}
	}
	session := &gulSession{ID: b.nextIDLocked(), Name: name, ChannelID: 1, Revision: 1, ExpiresAt: now.Add(gulSessionLease), MemberID: memberID, AuthVersion: authVersion, Nonce: sessionToken()}
	b.sessions[key] = session
	response := b.responseLocked(session, token, now)
	b.mu.Unlock()
	gulWrite(w, http.StatusOK, response)
}

func (b *gulBroker) state(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodGet) {
		return
	}
	b.withSession(w, r, func(session *gulSession, _ string, now time.Time) (int, any) {
		session.ExpiresAt = now.Add(gulSessionLease)
		return http.StatusOK, b.stateLocked(session)
	})
}

func (b *gulBroker) channel(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	var input struct {
		ChannelID *uint32 `json:"channelId"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	if input.ChannelID == nil || (b.store == nil && *input.ChannelID > 3) {
		http.Error(w, "unknown channel", http.StatusBadRequest)
		return
	}
	if b.remover != nil {
		b.publicTransition(w, r, input.ChannelID)
		return
	}
	b.withSession(w, r, func(session *gulSession, token string, now time.Time) (int, any) {
		if session.ChannelID != *input.ChannelID {
			session.ChannelID = *input.ChannelID
			session.Revision++
		}
		return http.StatusOK, b.responseLocked(session, token, now)
	})
}

func (b *gulBroker) audio(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	var input struct {
		Muted    *bool `json:"muted"`
		Deafened *bool `json:"deafened"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	if input.Muted == nil || input.Deafened == nil {
		http.Error(w, "both audio flags required", http.StatusBadRequest)
		return
	}
	b.withSession(w, r, func(session *gulSession, _ string, _ time.Time) (int, any) {
		session.Audio = api.AudioState{Muted: *input.Muted || *input.Deafened, Deafened: *input.Deafened}
		return http.StatusOK, session.Audio
	})
}

func (b *gulBroker) screen(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	var input struct {
		ChannelID *uint32 `json:"channelId"`
		Revision  *uint64 `json:"revision"`
	}
	if !gulJSON(w, r, &input) {
		return
	}
	if input.ChannelID == nil || input.Revision == nil {
		http.Error(w, "channel and revision required", http.StatusBadRequest)
		return
	}
	b.withSession(w, r, func(session *gulSession, _ string, now time.Time) (int, any) {
		if session.ChannelID != *input.ChannelID || session.Revision != *input.Revision {
			return http.StatusConflict, nil
		}
		return http.StatusOK, b.grantLocked(session, "screen", now)
	})
}

func (b *gulBroker) logout(w http.ResponseWriter, r *http.Request) {
	if !gulMethod(w, r, http.MethodPost) {
		return
	}
	// The desktop client sends no body. An explicit empty JSON object is also
	// accepted, but unrelated/oversized payloads must not bypass validation.
	if r.ContentLength != 0 {
		var input struct{}
		if !gulJSON(w, r, &input) {
			return
		}
	}
	if b.remover != nil {
		b.publicTransition(w, r, nil)
		return
	}
	b.withSession(w, r, func(_ *gulSession, token string, _ time.Time) (int, any) {
		delete(b.sessions, sha256.Sum256([]byte(token)))
		return http.StatusNoContent, nil
	})
}

// Request bodies and response writes happen outside the mutex: a slow HTTP
// peer must not block unrelated sessions. The callback only copies state.
func (b *gulBroker) withSession(w http.ResponseWriter, r *http.Request, fn func(*gulSession, string, time.Time) (int, any)) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	key, valid := tokenKey(token)
	if !ok || !valid {
		http.Error(w, "local session required", http.StatusUnauthorized)
		return
	}
	now := b.now()
	b.mu.Lock()
	b.expireLocked(now)
	session := b.sessions[key]
	if session == nil || session.Revoked || !b.sessionAccessLocked(session) {
		b.mu.Unlock()
		http.Error(w, "local session expired or unavailable", http.StatusUnauthorized)
		return
	}
	status, response := fn(session, token, now)
	b.mu.Unlock()
	if status == http.StatusConflict && response == nil {
		http.Error(w, "channel generation changed", status)
		return
	}
	gulWrite(w, status, response)
}

func gulMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", method)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != method {
		w.Header().Set("Allow", method+", OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func gulJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || !utf8.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}

func gulWrite(w http.ResponseWriter, status int, body any) {
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func validGulName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
