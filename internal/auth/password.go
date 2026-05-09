package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

// HashScheme identifies the algorithm used for a password hash.
type HashScheme int

const (
	// HashSchemeUnknown means the format was not recognised.
	HashSchemeUnknown HashScheme = iota
	// HashSchemeBcrypt is the canonical Go-side scheme.
	HashSchemeBcrypt
	// HashSchemePBKDF2SHA256 matches Django's default `pbkdf2_sha256$<iter>$<salt>$<hash>`.
	HashSchemePBKDF2SHA256
	// HashSchemePBKDF2SHA1 matches Django's `pbkdf2_sha1$<iter>$<salt>$<hash>`.
	HashSchemePBKDF2SHA1
	// HashSchemeArgon2 matches Django's `argon2$argon2id$v=19$m=...,t=...,p=...$salt$hash`.
	HashSchemeArgon2
)

// DetectHashScheme inspects a stored password hash and reports its scheme.
// Empty strings return HashSchemeUnknown.
func DetectHashScheme(hash string) HashScheme {
	hash = strings.TrimSpace(hash)
	switch {
	case hash == "":
		return HashSchemeUnknown
	case strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"), strings.HasPrefix(hash, "$2y$"):
		return HashSchemeBcrypt
	case strings.HasPrefix(hash, "pbkdf2_sha256$"):
		return HashSchemePBKDF2SHA256
	case strings.HasPrefix(hash, "pbkdf2_sha1$"):
		return HashSchemePBKDF2SHA1
	case strings.HasPrefix(hash, "argon2$"):
		return HashSchemeArgon2
	default:
		return HashSchemeUnknown
	}
}

// VerifyPassword checks plaintext against the given hash, regardless of scheme.
// The second return value is true when the hash uses a non-bcrypt scheme and
// the caller should rehash and persist a fresh bcrypt hash for the user.
func VerifyPassword(stored, plaintext string) (ok bool, needsRehash bool, err error) {
	scheme := DetectHashScheme(stored)
	switch scheme {
	case HashSchemeBcrypt:
		if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(plaintext)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				return false, false, nil
			}
			return false, false, err
		}
		return true, false, nil
	case HashSchemePBKDF2SHA256:
		ok, err := verifyPBKDF2(stored, plaintext, sha256.New)
		return ok, ok, err
	case HashSchemePBKDF2SHA1:
		ok, err := verifyPBKDF2(stored, plaintext, sha1.New)
		return ok, ok, err
	case HashSchemeArgon2:
		ok, err := verifyArgon2(stored, plaintext)
		return ok, ok, err
	default:
		return false, false, nil
	}
}

// HashPassword bcrypts a plaintext password at the default cost.
func HashPassword(plaintext string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// verifyPBKDF2 verifies a Django-style PBKDF2 password hash of the form
// `pbkdf2_<algo>$<iterations>$<salt>$<base64-hash>`. Salt is used verbatim
// (Django stores it as a printable string) — matching Django's behaviour.
func verifyPBKDF2(stored, plaintext string, h func() hash.Hash) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 {
		return false, fmt.Errorf("invalid pbkdf2 hash format")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false, fmt.Errorf("invalid pbkdf2 iteration count: %w", err)
	}
	salt := parts[2]
	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("invalid pbkdf2 hash encoding: %w", err)
	}
	keyLen := len(expected)
	if keyLen == 0 {
		keyLen = h().Size()
	}
	derived := pbkdf2.Key([]byte(plaintext), []byte(salt), iterations, keyLen, h)
	if subtle.ConstantTimeCompare(derived, expected) == 1 {
		return true, nil
	}
	return false, nil
}

// verifyArgon2 verifies a Django-formatted argon2 hash:
// `argon2$argon2id$v=19$m=102400,t=2,p=8$<salt-b64>$<hash-b64>`.
// Salt and hash are stored without padding (Django uses urlsafe base64 without
// trailing padding), so we accept both raw-and standard base64.
func verifyArgon2(stored, plaintext string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) < 6 {
		return false, fmt.Errorf("invalid argon2 hash format")
	}
	variant := parts[1]
	versionField := parts[2]
	paramField := parts[3]
	saltField := parts[4]
	hashField := parts[5]

	if !strings.EqualFold(variant, "argon2id") && !strings.EqualFold(variant, "argon2i") && !strings.EqualFold(variant, "argon2d") {
		return false, fmt.Errorf("unsupported argon2 variant: %s", variant)
	}
	if !strings.HasPrefix(versionField, "v=") {
		return false, fmt.Errorf("missing argon2 version field")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(versionField, "v="))
	if err != nil {
		return false, fmt.Errorf("invalid argon2 version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version: %d", version)
	}
	memory, time, threads, err := parseArgon2Params(paramField)
	if err != nil {
		return false, err
	}
	salt, err := decodeBase64Loose(saltField)
	if err != nil {
		return false, fmt.Errorf("invalid argon2 salt: %w", err)
	}
	expected, err := decodeBase64Loose(hashField)
	if err != nil {
		return false, fmt.Errorf("invalid argon2 hash: %w", err)
	}
	keyLen := uint32(len(expected))
	if keyLen == 0 {
		return false, fmt.Errorf("invalid argon2 hash length")
	}
	var derived []byte
	if strings.EqualFold(variant, "argon2i") {
		derived = argon2.Key([]byte(plaintext), salt, time, memory, threads, keyLen)
	} else {
		// argon2id / argon2d both fall through here; Django uses argon2id.
		derived = argon2.IDKey([]byte(plaintext), salt, time, memory, threads, keyLen)
	}
	if hmac.Equal(derived, expected) {
		return true, nil
	}
	return false, nil
}

func parseArgon2Params(field string) (memory, time uint32, threads uint8, err error) {
	for _, kv := range strings.Split(field, ",") {
		bits := strings.SplitN(kv, "=", 2)
		if len(bits) != 2 {
			return 0, 0, 0, fmt.Errorf("invalid argon2 param %q", kv)
		}
		val, parseErr := strconv.ParseUint(bits[1], 10, 32)
		if parseErr != nil {
			return 0, 0, 0, fmt.Errorf("invalid argon2 param %q: %w", kv, parseErr)
		}
		switch bits[0] {
		case "m":
			memory = uint32(val)
		case "t":
			time = uint32(val)
		case "p":
			if val > 255 {
				return 0, 0, 0, fmt.Errorf("argon2 parallelism out of range")
			}
			threads = uint8(val)
		}
	}
	if memory == 0 || time == 0 || threads == 0 {
		return 0, 0, 0, fmt.Errorf("missing argon2 parameters in %q", field)
	}
	return memory, time, threads, nil
}

// decodeBase64Loose handles strings encoded with either standard or
// URL-safe base64, with or without `=` padding (Django strips padding).
func decodeBase64Loose(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	enc := base64.StdEncoding
	if strings.ContainsAny(s, "-_") {
		enc = base64.URLEncoding
	}
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	b, err := enc.DecodeString(s)
	if err == nil {
		return b, nil
	}
	// Fall back to the raw variant if the padded decode failed.
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

