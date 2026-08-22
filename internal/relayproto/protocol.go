// Package relayproto defines the narrow wire contract shared by the Gul app
// and its fixed-target WSS relay.
package relayproto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

const bearerDomain = "gul-relay-v1 bearer"

const (
	Path        = "/mumble"
	Subprotocol = "gul-mumble-v1"
)

// Authorization returns a domain-separated, one-way bearer credential derived
// from secret. It remains replayable and therefore still relies on HTTPS for
// transport secrecy, but a leaked header is not the direct Mumble password.
func Authorization(secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(bearerDomain))
	return "Bearer " + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// MatchesAuthorization compares a supplied header with secret in constant
// time after validating the strict single-token Bearer shape.
func MatchesAuthorization(header string, secret []byte) bool {
	scheme, credential, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || credential == "" || strings.ContainsAny(credential, " \t\r\n") {
		return false
	}
	provided := sha256.Sum256([]byte(credential))
	expectedHeader := Authorization(secret)
	_, expectedCredential, _ := strings.Cut(expectedHeader, " ")
	expected := sha256.Sum256([]byte(expectedCredential))
	return subtle.ConstantTimeCompare(provided[:], expected[:]) == 1
}
