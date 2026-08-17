package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HashOpaqueToken returns the stable, one-way representation persisted for
// bearer and registration tokens. Token classes own their prefixes; hashing is
// deliberately generic and shared.
func HashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}
