package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const ArgoCDClusterProxyTokenPrefix = "astro_argocd_"

func GenerateArgoCDClusterProxyToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate argocd cluster proxy token: %w", err)
	}
	return ArgoCDClusterProxyTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func HashArgoCDClusterProxyToken(token string) string {
	return HashOpaqueToken(token)
}

func HashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func ArgoCDClusterProxyTokenDisplayPrefix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 18 {
		return token
	}
	return token[:18]
}
