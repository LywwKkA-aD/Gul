package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoginJSONDistinguishesOmittedGuestCredentialFromInvalidPersonalKey(t *testing.T) {
	for _, body := range []string{`{"username":"guest"}`, `{"username":"member","protocolVersion":2,"memberCredential":"` + strings.Repeat("a", 43) + `"}`} {
		var input LoginRequest
		if json.Unmarshal([]byte(body), &input) != nil || input.Username == "" {
			t.Fatal("valid wire request denied")
		}
	}
	for _, body := range []string{`{"memberCredential":null}`, `{"memberCredential":""}`, `{"memberCredential":false}`, `{"role":"owner"}`, `{"protocolVersion":"2"}`, `{`, `{} {}`} {
		input := LoginRequest{Username: "unchanged"}
		if input.UnmarshalJSON([]byte(body)) == nil || input.Username != "unchanged" {
			t.Fatal("invalid wire request accepted or partially mutated")
		}
	}
	data, err := json.Marshal(LoginRequest{Username: "guest"})
	if err != nil || strings.Contains(string(data), "memberCredential") {
		t.Fatal("guest marshal included an empty personal key")
	}
}
