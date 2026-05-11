package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/fernet/fernet-go"
)

// Encryptor handles Fernet encryption/decryption with optional multi-key
// support for online key rotation.
//
// The keys slice is ordered: keys[0] is the primary (used for Encrypt);
// every key including the primary is tried in order on Decrypt. The
// rotation procedure (see docs/secret-rotation-runbook.md) is:
//
//  1. add the new key as primary, keep the old as fallback
//     (encryptionKey="<new>,<old>")
//  2. restart the server — new ciphertexts are written with <new>; old
//     ciphertexts still decrypt because <old> is in the fallback list
//  3. run the re-encrypt helper (cmd/keyrotate) to rewrite every stored
//     ciphertext under <new>
//  4. drop the old key from config (encryptionKey="<new>") on the next
//     restart
//
// This avoids the unsafe single-key window where some rows have ciphertext
// under one key and others under another with no overlap.
type Encryptor struct {
	keys []*fernet.Key
}

// NewEncryptor creates an Encryptor from one or more comma-separated Fernet
// keys. Each key must be a valid 32-byte Fernet key (base64 URL-safe
// encoded, 44 chars). The first key is the primary (used for Encrypt);
// additional keys are tried only by Decrypt and exist for rotation
// continuity.
func NewEncryptor(keyString string) (*Encryptor, error) {
	if keyString == "" {
		return nil, fmt.Errorf("fernet key must not be empty")
	}

	var keys []*fernet.Key
	for i, raw := range strings.Split(keyString, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		k, err := fernet.DecodeKey(s)
		if err != nil {
			return nil, fmt.Errorf("invalid fernet key at position %d: %w", i, err)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("fernet key must not be empty")
	}

	return &Encryptor{keys: keys}, nil
}

// Encrypt encrypts plaintext under the primary key and returns a Fernet
// token (base64 URL-safe encoded).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	tok, err := fernet.EncryptAndSign([]byte(plaintext), e.keys[0])
	if err != nil {
		return "", fmt.Errorf("fernet encrypt: %w", err)
	}
	return string(tok), nil
}

// Decrypt decrypts a Fernet token using whichever configured key signed it.
// No TTL is enforced — secrets are stored indefinitely.
func (e *Encryptor) Decrypt(token string) (string, error) {
	msg := fernet.VerifyAndDecrypt([]byte(token), time.Duration(0), e.keys)
	if msg == nil {
		return "", fmt.Errorf("fernet decrypt: invalid token or no matching key")
	}
	return string(msg), nil
}

// KeyCount reports how many keys are loaded. Useful for /api/v1/admin
// diagnostics so an operator can confirm a rotation is mid-flight (>1)
// vs steady state (==1).
func (e *Encryptor) KeyCount() int {
	return len(e.keys)
}

// GenerateKey generates a new random Fernet key (base64 URL-safe encoded).
func GenerateKey() (string, error) {
	var k fernet.Key
	if err := k.Generate(); err != nil {
		return "", fmt.Errorf("generate fernet key: %w", err)
	}
	return k.Encode(), nil
}
