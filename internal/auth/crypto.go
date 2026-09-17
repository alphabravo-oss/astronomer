package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
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
	keys     []encryptionKey
	keysByID map[string]*fernet.Key
}

type encryptionKey struct {
	id  string
	key *fernet.Key
}

// EncryptionKeyInfo is the non-secret key inventory exposed to diagnostics and
// rotation tooling. IDs are either operator supplied or deterministic SHA-256
// fingerprints; key material is never returned.
type EncryptionKeyInfo struct {
	ID      string `json:"id"`
	Primary bool   `json:"primary"`
}

const ciphertextEnvelopePrefix = "astronomer:v1:fernet:"

var encryptionKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// NewEncryptor creates an Encryptor from one or more comma-separated Fernet
// keys. Entries may be either a legacy bare Fernet key or `key-id:key`. Bare
// keys receive a deterministic, non-secret SHA-256 fingerprint ID. The first
// key is the primary (used for Encrypt); additional keys are read-only fallbacks.
func NewEncryptor(keyString string) (*Encryptor, error) {
	if keyString == "" {
		return nil, fmt.Errorf("fernet key must not be empty")
	}

	var keys []encryptionKey
	keysByID := make(map[string]*fernet.Key)
	for i, raw := range strings.Split(keyString, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		id, encoded := parseEncryptionKeyEntry(s)
		k, err := fernet.DecodeKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid fernet key at position %d: %w", i, err)
		}
		if id == "" {
			id = derivedEncryptionKeyID(encoded)
		}
		if !encryptionKeyIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid encryption key id %q at position %d", id, i)
		}
		if _, duplicate := keysByID[id]; duplicate {
			return nil, fmt.Errorf("duplicate encryption key id %q", id)
		}
		keys = append(keys, encryptionKey{id: id, key: k})
		keysByID[id] = k
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("fernet key must not be empty")
	}

	return &Encryptor{keys: keys, keysByID: keysByID}, nil
}

// Encrypt encrypts plaintext under the primary key and returns a versioned,
// authenticated envelope containing the format, algorithm, key ID, and Fernet
// ciphertext. New writes never emit the legacy unlabelled token form.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e == nil || len(e.keys) == 0 {
		return "", fmt.Errorf("fernet encrypt: encryptor is not configured")
	}
	tok, err := fernet.EncryptAndSign([]byte(plaintext), e.keys[0].key)
	if err != nil {
		return "", fmt.Errorf("fernet encrypt: %w", err)
	}
	return ciphertextEnvelopePrefix + e.keys[0].id + ":" + string(tok), nil
}

// Decrypt decrypts a Fernet token using whichever configured key signed it.
// No TTL is enforced — secrets are stored indefinitely.
func (e *Encryptor) Decrypt(token string) (string, error) {
	msg, err := e.DecryptBytes(token)
	if err != nil {
		return "", err
	}
	return string(msg), nil
}

// DecryptBytes avoids an immutable plaintext string for short-lived worker
// credentials. The caller owns the returned slice and can overwrite it as soon
// as the credential has been parsed or handed to its bounded consumer.
func (e *Encryptor) DecryptBytes(token string) ([]byte, error) {
	if e == nil || len(e.keys) == 0 {
		return nil, fmt.Errorf("fernet decrypt: encryptor is not configured")
	}
	if strings.HasPrefix(token, "astronomer:") {
		keyID, ciphertext, err := parseCiphertextEnvelope(token)
		if err != nil {
			CiphertextDecryptionsTotal.WithLabelValues("invalid").Inc()
			return nil, err
		}
		key := e.keysByID[keyID]
		if key == nil {
			CiphertextDecryptionsTotal.WithLabelValues("unknown_key").Inc()
			return nil, fmt.Errorf("fernet decrypt: ciphertext references unknown key id %q", keyID)
		}
		msg := fernet.VerifyAndDecrypt([]byte(ciphertext), time.Duration(0), []*fernet.Key{key})
		if msg == nil {
			CiphertextDecryptionsTotal.WithLabelValues("invalid").Inc()
			return nil, fmt.Errorf("fernet decrypt: invalid envelope ciphertext")
		}
		CiphertextDecryptionsTotal.WithLabelValues("v1").Inc()
		return msg, nil
	}
	legacyKeys := make([]*fernet.Key, 0, len(e.keys))
	for _, configured := range e.keys {
		legacyKeys = append(legacyKeys, configured.key)
	}
	msg := fernet.VerifyAndDecrypt([]byte(token), time.Duration(0), legacyKeys)
	if msg == nil {
		CiphertextDecryptionsTotal.WithLabelValues("invalid").Inc()
		return nil, fmt.Errorf("fernet decrypt: invalid token or no matching key")
	}
	CiphertextDecryptionsTotal.WithLabelValues("legacy").Inc()
	return msg, nil
}

// KeyCount reports how many keys are loaded. Useful for /api/v1/admin
// diagnostics so an operator can confirm a rotation is mid-flight (>1)
// vs steady state (==1).
func (e *Encryptor) KeyCount() int {
	if e == nil {
		return 0
	}
	return len(e.keys)
}

// KeyInventory returns configured IDs in primary-first order without exposing
// key material.
func (e *Encryptor) KeyInventory() []EncryptionKeyInfo {
	if e == nil {
		return nil
	}
	result := make([]EncryptionKeyInfo, 0, len(e.keys))
	for i, configured := range e.keys {
		result = append(result, EncryptionKeyInfo{ID: configured.id, Primary: i == 0})
	}
	return result
}

func (e *Encryptor) PrimaryKeyID() string {
	if e == nil || len(e.keys) == 0 {
		return ""
	}
	return e.keys[0].id
}

// IsPrimaryCiphertext is the post-rotation gate: only a v1 envelope naming the
// current primary key is complete. Legacy tokens and fallback-key envelopes
// must be rewrapped before an old key can be removed.
func (e *Encryptor) IsPrimaryCiphertext(token string) bool {
	if e == nil || len(e.keys) == 0 {
		return false
	}
	keyID, _, err := parseCiphertextEnvelope(token)
	return err == nil && keyID == e.keys[0].id
}

func parseEncryptionKeyEntry(entry string) (id, encoded string) {
	if before, after, ok := strings.Cut(entry, ":"); ok && encryptionKeyIDPattern.MatchString(before) {
		return before, after
	}
	return "", entry
}

func derivedEncryptionKeyID(encoded string) string {
	digest := sha256.Sum256([]byte(encoded))
	return "sha256-" + hex.EncodeToString(digest[:8])
}

func parseCiphertextEnvelope(value string) (keyID, ciphertext string, err error) {
	if !strings.HasPrefix(value, ciphertextEnvelopePrefix) {
		return "", "", fmt.Errorf("fernet decrypt: unsupported ciphertext envelope")
	}
	rest := strings.TrimPrefix(value, ciphertextEnvelopePrefix)
	keyID, ciphertext, ok := strings.Cut(rest, ":")
	if !ok || !encryptionKeyIDPattern.MatchString(keyID) || ciphertext == "" || strings.Contains(ciphertext, ":") {
		return "", "", fmt.Errorf("fernet decrypt: malformed ciphertext envelope")
	}
	return keyID, ciphertext, nil
}

// GenerateKey generates a new random Fernet key (base64 URL-safe encoded).
func GenerateKey() (string, error) {
	var k fernet.Key
	if err := k.Generate(); err != nil {
		return "", fmt.Errorf("generate fernet key: %w", err)
	}
	return k.Encode(), nil
}
