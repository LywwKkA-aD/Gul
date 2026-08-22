package relayproto

import "testing"

func TestAuthorizationRoundTripSupportsArbitrarySecret(t *testing.T) {
	secret := []byte("spaces, unicode: páss\nheader-safe")
	header := Authorization(secret)
	if header == string(secret) {
		t.Fatal("authorization exposed the raw secret")
	}
	if !MatchesAuthorization(header, secret) {
		t.Fatal("matching authorization was rejected")
	}
	if MatchesAuthorization(header, []byte("different")) {
		t.Fatal("wrong secret was accepted")
	}
}

func TestAuthorizationKnownVector(t *testing.T) {
	const want = "Bearer ecXjMdtgB9bAbJ4xSNptLwta9ET3_MHCKlC72qd_3Ik"
	if got := Authorization([]byte("secret")); got != want {
		t.Fatalf("authorization = %q, want known vector", got)
	}
}

func TestMatchesAuthorizationRejectsMalformedValues(t *testing.T) {
	for _, header := range []string{"", "Basic abc", "Bearer", "Bearer ", "Bearer not base64!", "Bearer YQ extra"} {
		if MatchesAuthorization(header, []byte("a")) {
			t.Errorf("accepted %q", header)
		}
	}
}
