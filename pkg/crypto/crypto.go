// Package crypto is the only place in Astronomer that encrypts or decrypts
// column values at rest. Every caller hands it plaintext bytes and gets back a
// Sealed value — the (key_id, algorithm, ciphertext, nonce) row shape every
// encrypted column family stores — and hands a Sealed value back to decrypt.
// Handlers and query code never import filippo.io/age directly.
//
// The only algorithm today is age (X25519 recipient + ChaCha20-Poly1305
// payload), recorded as AlgorithmAgeX25519. age carries its own per-file
// nonce inside the ciphertext, so Sealed.Nonce is always empty for it; the
// column exists so an AEAD algorithm that needs an external nonce (for example
// a FIPS AES-256-GCM mode) can be added as a new Algorithm value without a
// schema change. See docs/decisions/0010-age-column-encryption.md.
//
// Key material is an age X25519 identity ("AGE-SECRET-KEY-1..."). It comes
// from a file or an environment variable, never from source. A keyring holds
// the current key (used for every Encrypt) plus any number of previous keys
// (used only to Decrypt rows that still reference them). Reads select the key
// by the stored key_id; there is no trial decryption.
package crypto

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"filippo.io/age"
)

// AlgorithmAgeX25519 is the algorithm column value for age X25519 recipients
// with the ChaCha20-Poly1305 payload.
const AlgorithmAgeX25519 = "age-x25519-chacha20poly1305"

// maxPlaintextBytes bounds what Decrypt will read back. Column secrets are
// credentials and small documents; 16 MiB is far above any legitimate value
// and stops a corrupted row from exhausting memory.
const maxPlaintextBytes = 16 << 20

var (
	// ErrNoKey reports that no key material was configured.
	ErrNoKey = errors.New("crypto: no encryption key configured")
	// ErrUnknownKey reports a Sealed value whose key_id is not in the keyring.
	ErrUnknownKey = errors.New("crypto: unknown key id")
	// ErrUnsupportedAlgorithm reports a Sealed value with an algorithm this
	// build cannot decrypt.
	ErrUnsupportedAlgorithm = errors.New("crypto: unsupported algorithm")
	// ErrEmpty reports a Decrypt call on a zero Sealed value.
	ErrEmpty = errors.New("crypto: sealed value is empty")
	// ErrDecrypt reports a ciphertext that failed authentication or parsing.
	ErrDecrypt = errors.New("crypto: decrypt failed")
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Sealed is one encrypted column value: the four columns
// <col>_key_id, <col>_algorithm, <col>_ciphertext and <col>_nonce.
type Sealed struct {
	KeyID      string
	Algorithm  string
	Ciphertext []byte
	Nonce      []byte
}

// IsZero reports whether the value is absent (no secret stored). The database
// CHECK constraints guarantee key_id, algorithm and ciphertext are either all
// set or all empty.
func (s Sealed) IsZero() bool {
	return s.KeyID == "" && s.Algorithm == "" && len(s.Ciphertext) == 0
}

// Parts returns the four column values in row order (key_id, algorithm,
// ciphertext, nonce), for assigning to generated query parameters:
//
//	p.SecretKeyID, p.SecretAlgorithm, p.SecretCiphertext, p.SecretNonce = sealed.Parts()
func (s Sealed) Parts() (keyID, algorithm string, ciphertext, nonce []byte) {
	return s.KeyID, s.Algorithm, s.Ciphertext, s.Nonce
}

// FromParts builds a Sealed value from the four columns of a row.
func FromParts(keyID, algorithm string, ciphertext, nonce []byte) Sealed {
	return Sealed{KeyID: keyID, Algorithm: algorithm, Ciphertext: ciphertext, Nonce: nonce}
}

// KeyInfo is the non-secret inventory of a loaded key.
type KeyInfo struct {
	ID        string `json:"id"`
	Primary   bool   `json:"primary"`
	Recipient string `json:"-"` // public; exposed to Go callers only
}

type key struct {
	id        string
	identity  *age.X25519Identity
	recipient *age.X25519Recipient
}

// Encryptor encrypts with the current key and decrypts with any loaded key.
// It is safe for concurrent use.
type Encryptor struct {
	keys []key // keys[0] is current
	byID map[string]*age.X25519Identity
}

// NewEncryptor parses exactly one age X25519 identity
// ("AGE-SECRET-KEY-1..."), optionally prefixed with an operator label
// ("label:AGE-SECRET-KEY-1..."). Without a label the key id is the
// deterministic fingerprint of the identity's public recipient.
func NewEncryptor(secretKey string) (*Encryptor, error) {
	enc, err := ParseKeyring(secretKey)
	if err != nil {
		return nil, err
	}
	if len(enc.keys) != 1 {
		return nil, fmt.Errorf("crypto: NewEncryptor expects one key, got %d (use ParseKeyring for a keyring)", len(enc.keys))
	}
	return enc, nil
}

// ParseKeyring parses a keyring: entries separated by newlines or commas,
// each "AGE-SECRET-KEY-1..." or "label:AGE-SECRET-KEY-1...". Blank lines and
// lines starting with '#' (the age-keygen file format) are ignored. The first
// entry is the current key; the rest are previous keys kept for decryption
// until no row references them.
func ParseKeyring(spec string) (*Encryptor, error) {
	enc := &Encryptor{byID: make(map[string]*age.X25519Identity)}
	sc := bufio.NewScanner(strings.NewReader(spec))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	position := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, raw := range strings.Split(line, ",") {
			entry := strings.TrimSpace(raw)
			if entry == "" {
				continue
			}
			position++
			k, err := parseEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("crypto: key entry %d: %w", position, err)
			}
			if _, dup := enc.byID[k.id]; dup {
				return nil, fmt.Errorf("crypto: duplicate key id %q", k.id)
			}
			for _, existing := range enc.keys {
				if existing.recipient.String() == k.recipient.String() {
					return nil, fmt.Errorf("crypto: key %q is loaded twice (also as %q)", k.id, existing.id)
				}
			}
			enc.keys = append(enc.keys, k)
			enc.byID[k.id] = k.identity
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("crypto: read keyring: %w", err)
	}
	if len(enc.keys) == 0 {
		return nil, ErrNoKey
	}
	return enc, nil
}

// NewEncryptorFromFile loads a keyring (see ParseKeyring) from a file, such as
// a mounted Kubernetes Secret or the output of age-keygen.
func NewEncryptorFromFile(path string) (*Encryptor, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied key path is the point
	if err != nil {
		return nil, fmt.Errorf("crypto: read key file: %w", err)
	}
	enc, err := ParseKeyring(string(data))
	if err != nil {
		return nil, fmt.Errorf("crypto: key file %s: %w", path, err)
	}
	return enc, nil
}

// NewEncryptorFromEnv loads a keyring (see ParseKeyring) from an environment
// variable.
func NewEncryptorFromEnv(envVar string) (*Encryptor, error) {
	value, ok := os.LookupEnv(envVar)
	if !ok || strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("crypto: %s: %w", envVar, ErrNoKey)
	}
	enc, err := ParseKeyring(value)
	if err != nil {
		return nil, fmt.Errorf("crypto: %s: %w", envVar, err)
	}
	return enc, nil
}

// Load resolves the configured key source: a key file path wins, otherwise
// the inline keyring value. Setting both is a configuration error, so an
// operator cannot believe one source is active while the other is. Returns
// ErrNoKey when neither is set.
func Load(inline, path string) (*Encryptor, error) {
	inline, path = strings.TrimSpace(inline), strings.TrimSpace(path)
	switch {
	case inline != "" && path != "":
		return nil, errors.New("crypto: both an inline key and a key file are configured; set only one")
	case path != "":
		return NewEncryptorFromFile(path)
	case inline != "":
		return ParseKeyring(inline)
	default:
		return nil, ErrNoKey
	}
}

// GenerateKey returns a new random age X25519 identity
// ("AGE-SECRET-KEY-1...").
func GenerateKey() (string, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", fmt.Errorf("crypto: generate key: %w", err)
	}
	return id.String(), nil
}

// KeyID returns the id a key entry ("AGE-SECRET-KEY-1..." or
// "label:AGE-SECRET-KEY-1...") is stored under.
func KeyID(entry string) (string, error) {
	k, err := parseEntry(strings.TrimSpace(entry))
	if err != nil {
		return "", err
	}
	return k.id, nil
}

// Encrypt seals plaintext under the current key.
func (e *Encryptor) Encrypt(plaintext []byte) (Sealed, error) {
	if e == nil || len(e.keys) == 0 {
		return Sealed{}, fmt.Errorf("crypto: encrypt: %w", ErrNoKey)
	}
	current := e.keys[0]
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, current.recipient)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return Sealed{}, fmt.Errorf("crypto: encrypt: %w", err)
	}
	if err := w.Close(); err != nil {
		return Sealed{}, fmt.Errorf("crypto: encrypt: %w", err)
	}
	return Sealed{KeyID: current.id, Algorithm: AlgorithmAgeX25519, Ciphertext: buf.Bytes()}, nil
}

// EncryptString is Encrypt for a string plaintext.
func (e *Encryptor) EncryptString(plaintext string) (Sealed, error) {
	return e.Encrypt([]byte(plaintext))
}

// Decrypt opens a Sealed value with the key its KeyID names. The caller owns
// the returned slice and may overwrite it once the secret has been used.
func (e *Encryptor) Decrypt(s Sealed) ([]byte, error) {
	if e == nil || len(e.keys) == 0 {
		return nil, fmt.Errorf("crypto: decrypt: %w", ErrNoKey)
	}
	if s.IsZero() {
		return nil, ErrEmpty
	}
	if s.Algorithm != AlgorithmAgeX25519 {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, s.Algorithm)
	}
	if len(s.Nonce) != 0 {
		return nil, fmt.Errorf("%w: %s carries no external nonce", ErrDecrypt, AlgorithmAgeX25519)
	}
	identity, ok := e.byID[s.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, s.KeyID)
	}
	r, err := age.Decrypt(bytes.NewReader(s.Ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: %w", ErrDecrypt, s.KeyID, err)
	}
	out, err := io.ReadAll(io.LimitReader(r, maxPlaintextBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: %w", ErrDecrypt, s.KeyID, err)
	}
	if len(out) > maxPlaintextBytes {
		return nil, fmt.Errorf("%w: plaintext exceeds %d bytes", ErrDecrypt, maxPlaintextBytes)
	}
	return out, nil
}

// DecryptString is Decrypt returning a string.
func (e *Encryptor) DecryptString(s Sealed) (string, error) {
	out, err := e.Decrypt(s)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CurrentKeyID is the key id stamped on every new write.
func (e *Encryptor) CurrentKeyID() string {
	if e == nil || len(e.keys) == 0 {
		return ""
	}
	return e.keys[0].id
}

// HasKey reports whether the keyring can decrypt rows stamped with id.
func (e *Encryptor) HasKey(id string) bool {
	if e == nil {
		return false
	}
	_, ok := e.byID[id]
	return ok
}

// KeyIDs lists loaded key ids, current first.
func (e *Encryptor) KeyIDs() []string {
	if e == nil {
		return nil
	}
	ids := make([]string, 0, len(e.keys))
	for _, k := range e.keys {
		ids = append(ids, k.id)
	}
	return ids
}

// KeyCount reports how many keys are loaded (>1 while a rotation is pending).
func (e *Encryptor) KeyCount() int {
	if e == nil {
		return 0
	}
	return len(e.keys)
}

// KeyInventory returns the loaded keys, current first, without secret
// material.
func (e *Encryptor) KeyInventory() []KeyInfo {
	if e == nil {
		return nil
	}
	out := make([]KeyInfo, 0, len(e.keys))
	for i, k := range e.keys {
		out = append(out, KeyInfo{ID: k.id, Primary: i == 0, Recipient: k.recipient.String()})
	}
	return out
}

// IsCurrent reports whether s is already sealed under the current key with
// the current algorithm, i.e. rotation has nothing to do for it.
func (e *Encryptor) IsCurrent(s Sealed) bool {
	return e != nil && len(e.keys) > 0 && s.KeyID == e.keys[0].id && s.Algorithm == AlgorithmAgeX25519
}

func parseEntry(entry string) (key, error) {
	label, secret := "", entry
	if before, after, ok := strings.Cut(entry, ":"); ok {
		label, secret = strings.TrimSpace(before), strings.TrimSpace(after)
		if !keyIDPattern.MatchString(label) {
			return key{}, fmt.Errorf("invalid key label %q (want %s)", label, keyIDPattern.String())
		}
	}
	identity, err := age.ParseX25519Identity(secret)
	if err != nil {
		// Never echo the secret: age's own error is generic enough.
		return key{}, fmt.Errorf("not an age X25519 identity (AGE-SECRET-KEY-1...): %w", err)
	}
	recipient := identity.Recipient()
	id := label
	if id == "" {
		id = fingerprint(recipient)
	}
	return key{id: id, identity: identity, recipient: recipient}, nil
}

// fingerprint derives a stable, non-secret key id from the public recipient.
func fingerprint(r *age.X25519Recipient) string {
	sum := sha256.Sum256([]byte(r.String()))
	return "age-" + hex.EncodeToString(sum[:8])
}
