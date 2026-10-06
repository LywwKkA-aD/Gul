// This Source Code Form is subject to the terms of the Mozilla Public License,
// v. 2.0. If a copy of the MPL was not distributed with this file, You can obtain
// one at https://mozilla.org/MPL/2.0/.
//
// REALITY client handshake adapted from XTLS/Xray-core v26.3.27,
// commit d2758a023cd7f4174a5a5fa4ff66e487d4342ba0,
// transport/internet/reality/reality.go. Gul changes: public certificate parsing
// instead of unsafe reflection; fixed Chrome fingerprint; fail closed without
// cover-site fallback, optional ML-DSA or logging; bounded setup context.
package reality

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/crypto/hkdf"
)

func handshake(ctx context.Context, raw net.Conn, serverName string, cfg *parsedConfig) (net.Conn, error) {
	var authKey []byte
	verified := false
	tlsConfig := &utls.Config{
		ServerName:             serverName,
		SessionTicketsDisabled: true,
		// REALITY authenticates its ephemeral certificate with the pinned key's
		// HMAC, not WebPKI. The required callback rejects every other certificate.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(authKey) != 32 || len(rawCerts) == 0 {
				return ErrAuthentication
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return ErrAuthentication
			}
			pub, ok := cert.PublicKey.(ed25519.PublicKey)
			if !ok {
				return ErrAuthentication
			}
			h := hmac.New(sha512.New, authKey)
			_, _ = h.Write(pub)
			if !hmac.Equal(h.Sum(nil), cert.Signature) {
				return ErrAuthentication
			}
			verified = true
			return nil
		},
	}
	conn := utls.UClient(raw, tlsConfig, utls.HelloChrome_Auto)
	if err := conn.BuildHandshakeState(); err != nil {
		return nil, errors.New("construct REALITY ClientHello")
	}
	hello := conn.HandshakeState.Hello
	if hello == nil || len(hello.Raw) < 71 || len(hello.Random) != 32 || len(hello.SessionId) != 32 || hello.Raw[38] != 32 {
		return nil, errors.New("unsupported REALITY ClientHello")
	}
	hello.SessionId = make([]byte, 32)
	copy(hello.Raw[39:], hello.SessionId)
	hello.SessionId[0], hello.SessionId[1], hello.SessionId[2] = 26, 3, 27
	binary.BigEndian.PutUint32(hello.SessionId[4:], uint32(time.Now().Unix()))
	copy(hello.SessionId[8:], cfg.shortID)
	publicKey, err := ecdh.X25519().NewPublicKey(cfg.publicKey)
	if err != nil {
		return nil, ErrAuthentication
	}
	shares := conn.HandshakeState.State13.KeyShareKeys
	if shares == nil {
		return nil, errors.New("REALITY ClientHello lacks key shares")
	}
	privateKey := shares.Ecdhe
	if privateKey == nil {
		privateKey = shares.MlkemEcdhe
	}
	if privateKey == nil {
		return nil, errors.New("REALITY requires an X25519 key share")
	}
	authKey, err = privateKey.ECDH(publicKey)
	if err != nil {
		return nil, ErrAuthentication
	}
	if _, err := io.ReadFull(hkdf.New(sha256.New, authKey, hello.Random[:20], []byte("REALITY")), authKey); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(authKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	aead.Seal(hello.SessionId[:0], hello.Random[20:], hello.SessionId[:16], hello.Raw)
	copy(hello.Raw[39:], hello.SessionId)
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if !verified {
		return nil, ErrAuthentication
	}
	return conn, nil
}
