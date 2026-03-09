package auth

import (
	"fmt"
	"time"

	"github.com/fernet/fernet-go"
)

// Encryptor handles Fernet encryption/decryption.
type Encryptor struct {
	key *fernet.Key
}

// NewEncryptor creates an Encryptor from a base64-encoded Fernet key.
// The key must be a valid 32-byte Fernet key (base64 URL-safe encoded, 44 chars).
func NewEncryptor(keyString string) (*Encryptor, error) {
	if keyString == "" {
		return nil, fmt.Errorf("fernet key must not be empty")
	}

	k, err := fernet.DecodeKey(keyString)
	if err != nil {
		return nil, fmt.Errorf("invalid fernet key: %w", err)
	}

	return &Encryptor{key: k}, nil
}

// Encrypt encrypts plaintext and returns a Fernet token (base64 URL-safe encoded).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	tok, err := fernet.EncryptAndSign([]byte(plaintext), e.key)
	if err != nil {
		return "", fmt.Errorf("fernet encrypt: %w", err)
	}
	return string(tok), nil
}

// Decrypt decrypts a Fernet token and returns the plaintext.
// No TTL is enforced — secrets are stored indefinitely.
func (e *Encryptor) Decrypt(token string) (string, error) {
	msg := fernet.VerifyAndDecrypt([]byte(token), time.Duration(0), []*fernet.Key{e.key})
	if msg == nil {
		return "", fmt.Errorf("fernet decrypt: invalid token or wrong key")
	}
	return string(msg), nil
}

// GenerateKey generates a new random Fernet key (base64 URL-safe encoded).
func GenerateKey() (string, error) {
	var k fernet.Key
	if err := k.Generate(); err != nil {
		return "", fmt.Errorf("generate fernet key: %w", err)
	}
	return k.Encode(), nil
}
