package api

import (
	"bytes"
	"encoding/json"
	"errors"
)

// An omitted personal key allows guest login. An explicitly supplied empty
// or null key is a malformed credential, never a request to become a guest.
func (r *LoginRequest) UnmarshalJSON(data []byte) error {
	type plain LoginRequest
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("invalid login request")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errors.New("invalid login request")
	}
	if raw, present := fields["memberCredential"]; present && (decoded.MemberCredential == "" || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))) {
		return errors.New("invalid member credential")
	}
	*r = LoginRequest(decoded)
	return nil
}
