package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Known vectors for the password "secret": the current PBKDF2 credential, and
// the value v0.3.0-alpha.2 clients sent. Nothing derives the second one any
// more - it is written out here because the tests still need its shape.
const (
	currentVector = "v2.P1fPld_7YGIF3a4iPHzj38wXSnYZgJWjaS7G2Zgz3w4"
	legacyVector  = "ecXjMdtgB9bAbJ4xSNptLwta9ET3_MHCKlC72qd_3Ik"
)

// One credential, on one line. The compatibility line that used to follow it
// was the whole of the deprecation window, and it is gone: a secret written by
// this command can no longer authorize a client that predates the v2 scheme.
func TestDeriveCredentialCommandEmitsOnlyTheCurrentCredential(t *testing.T) {
	var output bytes.Buffer
	if err := deriveCredentialCommand(nil, strings.NewReader("secret\n"), &output); err != nil {
		t.Fatalf("derive credential: %v", err)
	}
	if got := output.String(); got != currentVector+"\n" {
		t.Fatalf("credentials = %q, want only the current vector", got)
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatal("output leaked the raw Mumble password")
	}
}

func TestDeriveCredentialCommandFromAbsoluteSecretFile(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "mumble-password")
	if err := os.WriteFile(secretFile, []byte("secret\r\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	var output bytes.Buffer
	if err := deriveCredentialCommand([]string{"--secret-file", secretFile}, strings.NewReader("ignored"), &output); err != nil {
		t.Fatalf("derive credential from file: %v", err)
	}
	if got := output.String(); !strings.HasPrefix(got, currentVector+"\n") {
		t.Fatalf("credentials = %q, want the known vector first", got)
	}
}

func TestDeriveCredentialCommandRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "empty", input: "\n"},
		{name: "oversized", input: strings.Repeat("x", maxCredentialInputBytes+1)},
		{name: "relative file", args: []string{"--secret-file", "relative-secret"}, input: "unused"},
		{name: "positional secret", args: []string{"raw-password"}, input: "unused"},
		{name: "unknown flag", args: []string{"--raw-secret", "raw-password"}, input: "unused"},
		// The flag that used to select this behavior. It is now the only
		// behavior, and a runbook still passing it should fail loudly rather
		// than look like it did something.
		{name: "the retired v2-only flag", args: []string{"--v2-only"}, input: "secret\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := deriveCredentialCommand(tc.args, strings.NewReader(tc.input), &output); err == nil {
				t.Fatal("expected error")
			}
			if output.Len() != 0 {
				t.Fatalf("output on failure = %q, want empty", output.String())
			}
		})
	}
}

// A secret file written before the window closed still parses, so an operator
// who has not recreated it is not locked out of their own relay: the handler
// is what drops the second line (prepareCredentials).
func TestReadCredentialFileAcceptsBothGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "GUL_RELAY_BEARER")
	if err := os.WriteFile(path, []byte(currentVector+"\n"+legacyVector+"\n"), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	credentials, err := readCredentialFile(path)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credentials = %d, want 2", len(credentials))
	}
	if string(credentials[0]) != currentVector || credentials[0].Legacy() {
		t.Fatalf("first credential = %q, want the current one", credentials[0])
	}
	if !credentials[1].Legacy() {
		t.Fatalf("second credential = %q, want the compatibility one", credentials[1])
	}
}

func TestParseCredentialsRejectsUnusableFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "blank lines only", content: "\n\n"},
		{name: "raw password", content: "hunter2 with spaces\n"},
		{name: "padded", content: currentVector + "==\n"},
		{name: "too many", content: strings.Repeat(currentVector+"\n", maxExpectedCredentials+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseCredentials([]byte(tc.content)); err == nil {
				t.Fatal("expected error")
			} else if strings.Contains(err.Error(), currentVector) {
				t.Fatal("error message echoed the credential")
			}
		})
	}
}

func TestReadCredentialFileRejectsOversizedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "GUL_RELAY_BEARER")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), maxCredentialFileBytes+1), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	if _, err := readCredentialFile(path); err == nil {
		t.Fatal("expected error")
	}
}
