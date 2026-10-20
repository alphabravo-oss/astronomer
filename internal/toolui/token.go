package toolui

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// sealToken seals a tool-session document into a compact, URL- and
// cookie-safe string: "<key id>.<base64url age ciphertext>". The key id
// selects the decryption key on the way back, as for column values.
func (g *Gateway) sealToken(plaintext []byte) (string, error) {
	sealed, err := g.cipher.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return sealed.KeyID + "." + base64.RawURLEncoding.EncodeToString(sealed.Ciphertext), nil
}

// openToken reverses sealToken.
func (g *Gateway) openToken(token string) ([]byte, error) {
	keyID, encoded, ok := strings.Cut(token, ".")
	if !ok || keyID == "" || encoded == "" {
		return nil, errors.New("invalid tool session")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid tool session")
	}
	return g.cipher.Decrypt(crypto.Sealed{KeyID: keyID, Algorithm: crypto.AlgorithmAgeX25519, Ciphertext: ciphertext})
}
