package team

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// Agent tokens live in the team database. Tokens Pyro must send to an agent
// (direct agents) are encrypted with a key derived from the JWT secret;
// tokens an agent presents to Pyro (connecting agents) are stored hashed.

// errTokenUnreadable is returned when a stored token fails to decrypt, which
// means the server's secret changed since it was saved.
var errTokenUnreadable = errors.New("token can't be read (the server's secret changed); edit the agent and enter its token again")

// connectTokenPrefix marks tokens minted for connecting agents.
const connectTokenPrefix = "pyro_agent_"

// tokenBox seals and opens agent tokens with AES-256-GCM.
type tokenBox struct{ aead cipher.AEAD }

// newTokenBox derives the key from the JWT secret with HKDF-SHA256.
func newTokenBox(jwtSecret []byte) (*tokenBox, error) {
	key, err := hkdf.Key(sha256.New, jwtSecret, nil, "pyro agent tokens v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive agent token key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &tokenBox{aead: aead}, nil
}

// seal encrypts token and returns base64(nonce || ciphertext).
func (b *tokenBox) seal(token string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(token), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// open reverses seal. Any decoding or authentication failure returns
// errTokenUnreadable.
func (b *tokenBox) open(enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errTokenUnreadable
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errTokenUnreadable
	}
	return string(plain), nil
}

// newConnectToken mints a token for a connecting agent and returns it with
// the hash to store.
func newConnectToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = connectTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return token, hashAgentToken(token), nil
}

// hashAgentToken is the stored form of a connecting agent's token.
func hashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
