// Package publish puts an exported website on somebody's web hosting over SFTP or FTPS: it keeps the hosting passwords
// encrypted, refuses to connect to the server's own network, uploads only what changed, and removes only what it uploaded.
package publish

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Cipher encrypts the secrets the platform stores (hosting passwords) with AES-256 in GCM mode. The key comes from the
// server's own configuration and is never in the database, so a copy of the database alone reveals nothing. Each secret
// is bound to the place it belongs (the "context"), so a ciphertext moved to another website's row does not decrypt.
type Cipher struct{ aead cipher.AEAD }

// ErrNoKey is returned when the server has no secret key configured.
var ErrNoKey = errors.New("publish: SECRET_KEY is not set, so hosting passwords cannot be stored")

// NewCipher makes a cipher from a key given as 64 hexadecimal characters (32 bytes).
func NewCipher(hexKey string) (*Cipher, error) {
	if hexKey == "" {
		return nil, ErrNoKey
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("publish: SECRET_KEY must be 64 hexadecimal characters (32 bytes), for example from: openssl rand -hex 32")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce || ciphertext for the secret, bound to its context.
func (c *Cipher) Encrypt(secret, context string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(secret), []byte(context)), nil
}

// Decrypt reverses Encrypt. It fails if the data was changed or belongs to a different context.
func (c *Cipher) Decrypt(data []byte, context string) (string, error) {
	n := c.aead.NonceSize()
	if len(data) < n+c.aead.Overhead() {
		return "", errors.New("publish: stored secret is damaged")
	}
	out, err := c.aead.Open(nil, data[:n], data[n:], []byte(context))
	if err != nil {
		return "", fmt.Errorf("publish: stored secret cannot be read (wrong key, or it was changed)")
	}
	return string(out), nil
}
