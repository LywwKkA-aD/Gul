package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const (
	maxCredentialInputBytes = 4096
	// maxCredentialFileBytes bounds the pre-derived credential file: at most a
	// handful of credentials, each an unpadded base64url value with an
	// optional version prefix.
	maxCredentialFileBytes = 512
	maxExpectedCredentials = 4
)

// deriveCredentialCommand turns the raw Mumble password into the credential
// the relay expects. It is the only place a Gul binary handles that password:
// the derivation is deliberately expensive, and its output is what the
// production secret holds, so the relay itself never sees the password.
func deriveCredentialCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("derive-credential", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	secretFile := flags.String("secret-file", "", "absolute path to the raw Mumble password file")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid derive-credential flags")
	}
	if flags.NArg() != 0 {
		return errors.New("raw credentials must not be passed as command arguments")
	}

	input := stdin
	var file *os.File
	if *secretFile != "" {
		if !filepath.IsAbs(*secretFile) || filepath.Clean(*secretFile) != *secretFile {
			return errors.New("secret-file must be a clean absolute path")
		}
		var err error
		file, err = os.Open(*secretFile)
		if err != nil {
			return fmt.Errorf("open secret-file: %w", err)
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("inspect secret-file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("secret-file must be a regular file")
		}
		input = file
	}

	secret, err := readOneLineSecret(input, maxCredentialInputBytes)
	if err != nil {
		return fmt.Errorf("read raw Mumble password: %w", err)
	}
	defer clear(secret)

	// One line, the current credential. Older builds had a second,
	// compatibility line here; the window it served closed on 2026-08-27.
	var rendered bytes.Buffer
	if _, err := fmt.Fprintln(&rendered, string(relayproto.Derive(secret))); err != nil {
		return fmt.Errorf("render bearer credential: %w", err)
	}
	if _, err := stdout.Write(rendered.Bytes()); err != nil {
		return fmt.Errorf("write bearer credential: %w", err)
	}
	return nil
}

// readCredentialFile loads the expected credentials, one per line, most
// recent first.
func readCredentialFile(path string) ([]relayproto.Credential, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open relay bearer credential: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxCredentialFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read relay bearer credential: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close relay bearer credential: %w", closeErr)
	}
	if len(content) > maxCredentialFileBytes {
		return nil, errors.New("relay bearer credential file is too large")
	}
	credentials, err := parseCredentials(content)
	if err != nil {
		return nil, fmt.Errorf("relay bearer credential: %w", err)
	}
	return credentials, nil
}

func parseCredentials(content []byte) ([]relayproto.Credential, error) {
	credentials := make([]relayproto.Credential, 0, maxExpectedCredentials)
	for _, line := range bytes.Split(content, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if len(credentials) == maxExpectedCredentials {
			return nil, errors.New("too many credentials")
		}
		credential, ok := relayproto.ParseHeader("Bearer " + string(line))
		if !ok {
			// The message must not echo the line: it is the credential itself.
			return nil, errors.New("must contain values produced by `gul-relay derive-credential`, one per line")
		}
		credentials = append(credentials, credential)
	}
	if len(credentials) == 0 {
		return nil, errors.New("file is empty")
	}
	return credentials, nil
}

func readOneLineSecret(input io.Reader, maxBytes int) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(input, int64(maxBytes+3)))
	if err != nil {
		return nil, err
	}
	if len(value) > maxBytes+2 {
		clear(value)
		return nil, errors.New("secret input is too large")
	}
	if bytes.HasSuffix(value, []byte("\r\n")) {
		value = value[:len(value)-2]
	} else if bytes.HasSuffix(value, []byte("\n")) {
		value = value[:len(value)-1]
	}
	if len(value) == 0 {
		clear(value)
		return nil, errors.New("secret input is empty")
	}
	if len(value) > maxBytes {
		clear(value)
		return nil, errors.New("secret input is too large")
	}
	if bytes.ContainsAny(value, "\r\n") {
		clear(value)
		return nil, errors.New("secret input must contain exactly one line")
	}
	return value, nil
}
