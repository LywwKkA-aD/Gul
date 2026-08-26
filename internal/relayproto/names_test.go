package relayproto

import (
	"strings"
	"testing"
)

// The names must not describe what they carry. The pair they replace did:
// `GET /mumble` with `Sec-WebSocket-Protocol: gul-mumble-v1` announced the
// tunnel to anything that terminates TLS on the way.
func TestNamesSayNothing(t *testing.T) {
	t.Parallel()
	names := NamesFor(Derive([]byte("server password")))
	rendered := strings.ToLower(names.Path + " " + names.Subprotocol)
	for _, word := range []string{"gul", "mumble", "relay", "voice", "tunnel"} {
		if strings.Contains(rendered, word) {
			t.Errorf("names %+v contain %q", names, word)
		}
	}
	if !strings.HasPrefix(names.Path, "/") {
		t.Errorf("path = %q, want an absolute path", names.Path)
	}
	if names.Subprotocol == "" {
		t.Error("no subprotocol")
	}
}

// Two servers must not share a pair, or one leaked path would locate every
// relay that exists.
func TestNamesDifferPerServer(t *testing.T) {
	t.Parallel()
	first := NamesFor(Derive([]byte("one")))
	second := NamesFor(Derive([]byte("two")))
	if first.Path == second.Path {
		t.Errorf("both servers answer on %q", first.Path)
	}
	if first.Subprotocol == second.Subprotocol {
		t.Errorf("both servers offer %q", first.Subprotocol)
	}
}

// Both sides derive the pair independently and have to agree, or nobody
// connects at all.
func TestNamesAreStable(t *testing.T) {
	t.Parallel()
	credential := Derive([]byte("server password"))
	first, second := NamesFor(credential), NamesFor(credential)
	if first != second {
		t.Fatalf("%+v then %+v", first, second)
	}
}

// The path is public inside the TLS session; it must not carry the credential
// that produced it back out.
func TestNamesDoNotContainTheCredential(t *testing.T) {
	t.Parallel()
	credential := Derive([]byte("server password"))
	names := NamesFor(credential)
	body := strings.TrimPrefix(string(credential), v2Prefix)
	if strings.Contains(names.Path, body) || strings.Contains(names.Subprotocol, body) {
		t.Fatalf("names %+v embed the credential", names)
	}
	for size := 8; size <= len(body); size += 8 {
		if strings.Contains(names.Path+names.Subprotocol, body[:size]) {
			t.Fatalf("names %+v embed a %d-character prefix of the credential", names, size)
		}
	}
}

func TestNamesShape(t *testing.T) {
	t.Parallel()
	names := NamesFor(Derive([]byte("server password")))
	t.Logf("path=%s subprotocol=%s", names.Path, names.Subprotocol)
	if strings.Count(names.Path, "/") != 2 {
		t.Errorf("path = %q, want one segment under a prefix", names.Path)
	}
}
