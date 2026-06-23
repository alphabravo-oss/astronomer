package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// SCIMTokenPrefix is the human-recognisable prefix on every SCIM
// provisioning bearer token. Lets operators eyeball a leaked secret in
// a log and know what it grants.
const SCIMTokenPrefix = "astro_scim_"

// GenerateSCIMToken mints a fresh SCIM provisioning bearer token. The
// plaintext is returned to the caller once at creation time; only the
// SHA-256 hash (HashOpaqueToken) is persisted.
func GenerateSCIMToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate scim token: %w", err)
	}
	return SCIMTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashSCIMToken returns the stored hash form of a SCIM token. Shares the
// opaque-token SHA-256 contract used by the cluster-agent / argocd-proxy
// tokens so a DB compromise never yields a usable credential.
func HashSCIMToken(token string) string {
	return HashOpaqueToken(token)
}

// SCIMTokenDisplayPrefix returns the leading slice stored in the row's
// `prefix` column so the operator UI can show "astro_scim_AbCd…" without
// holding the secret.
func SCIMTokenDisplayPrefix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 16 {
		return token
	}
	return token[:16]
}
