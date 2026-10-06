package session

import (
	"strings"
	"testing"
)

func TestRedactServer(t *testing.T) {
	for _, address := range []string{
		"http://127.0.0.1:8787", "livekit://localhost:8787", "voice.example:64738",
		"https://user:secret@voice.example/api?access_token=credential-value",
	} {
		result := RedactServer("failed "+address+" via 203.0.113.7:443: timeout", address)
		if !strings.Contains(result, "timeout") || strings.Contains(result, address) || strings.Contains(result, "203.0.113.7") {
			t.Fatalf("unsafe or unhelpful diagnostic %q", result)
		}
	}
	result := RedactServer("voice.example credential-value secret timeout", "https://user:secret@voice.example/api?access_token=credential-value")
	for _, sensitive := range []string{"voice.example", "credential-value", "secret"} {
		if strings.Contains(result, sensitive) {
			t.Fatalf("leaked %q", sensitive)
		}
	}
}

func TestRedactServerPreservesNonAddresses(t *testing.T) {
	text := "map[state:connecting] 999.999.999.999 ms"
	if RedactServer(text, "") != text {
		t.Fatal("changed non-address diagnostic")
	}
	if RedactServer("", "localhost") != "" {
		t.Fatal("changed empty diagnostic")
	}
}
