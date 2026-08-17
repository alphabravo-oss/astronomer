package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/gowebpki/jcs"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Digest is a lowercase, algorithm-qualified SHA-256 digest.
type Digest string

// ParseDigest validates and returns a digest. Digest strings are intentionally
// not normalized: uppercase or an omitted algorithm is rejected so signatures,
// approvals, and optimistic concurrency all bind the same representation.
func ParseDigest(value string) (Digest, error) {
	digest := Digest(value)
	if err := digest.Validate(); err != nil {
		return "", err
	}
	return digest, nil
}

func (d Digest) String() string { return string(d) }

func (d Digest) Validate() error {
	if !digestPattern.MatchString(string(d)) {
		return &ValidationError{Violations: []Violation{{
			Field: "digest", Code: CodeInvalid,
			Message: "must use sha256 followed by 64 lowercase hexadecimal characters",
		}}}
	}
	return nil
}

// DigestBytes hashes exact bytes. Use CanonicalDigest for structured domain
// objects whose map ordering must not affect identity.
func DigestBytes(value []byte) Digest {
	sum := sha256.Sum256(value)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

// CanonicalJSON serializes value using RFC 8785 JSON Canonicalization Scheme.
// Unsupported JSON values (NaN, infinity, functions, channels) return an error.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical delivery value: %w", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize delivery value: %w", err)
	}
	return canonical, nil
}

// CanonicalDigest returns the content identity of an RFC 8785 representation.
func CanonicalDigest(value any) (Digest, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return DigestBytes(canonical), nil
}
