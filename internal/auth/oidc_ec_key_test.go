package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestJWKCurvePublicKeyParsing(t *testing.T) {
	for name, curve := range map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()} {
		t.Run(name, func(t *testing.T) {
			private, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			point, err := private.PublicKey.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			size := (len(point) - 1) / 2
			j := jwk{Kty: "EC", Crv: name, X: base64.RawURLEncoding.EncodeToString(point[1 : 1+size]), Y: base64.RawURLEncoding.EncodeToString(point[1+size:])}
			parsed, err := jwkToPublicKey(j)
			if err != nil {
				t.Fatal(err)
			}
			key, ok := parsed.(*ecdsa.PublicKey)
			if !ok || !key.Equal(&private.PublicKey) {
				t.Fatal("JWK key changed")
			}
			signature, err := ecdsa.SignASN1(rand.Reader, private, []byte("OIDC signature regression"))
			if err != nil {
				t.Fatal(err)
			}
			if !ecdsa.VerifyASN1(key, []byte("OIDC signature regression"), signature) {
				t.Fatal("parsed key cannot verify signature")
			}
			invalid := j
			invalid.X = base64.RawURLEncoding.EncodeToString(make([]byte, size))
			invalid.Y = invalid.X
			if _, err := jwkToPublicKey(invalid); err == nil {
				t.Fatal("off-curve point accepted")
			}
			invalid = j
			invalid.X = base64.RawURLEncoding.EncodeToString(point[1:size])
			if _, err := jwkToPublicKey(invalid); err == nil {
				t.Fatal("short coordinate accepted")
			}
		})
	}
}
