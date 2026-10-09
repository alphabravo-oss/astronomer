package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestDetachedImportDocumentUsesCanonicalOfflineTrust(t *testing.T) {
	directory := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(directory, "vendor.pub"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600)
	document := filepath.Join(directory, "closed-bom.json")
	data := []byte(`{"archive_digest":"sha256:QA","identity":"local QA publisher"}`)
	_ = os.WriteFile(document, data, 0600)
	digest := sha256.Sum256(data)
	signed, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(directory, "signature.json")
	bundle, _ := json.Marshal(map[string]any{"messageSignature": map[string]any{"signature": base64.StdEncoding.EncodeToString(signed)}})
	_ = os.WriteFile(evidence, bundle, 0600)
	if err := verifyDetachedDocument(document, evidence, directory, "cosign_key", "vendor", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ provider, keyRef, identity, issuer string }{
		{"cosign_key", "unknown", "", ""}, {"cosign_keyless", "", "wrong publisher", "https://issuer.example"}, {"none", "", "", ""},
	} {
		if err := verifyDetachedDocument(document, evidence, directory, test.provider, test.keyRef, test.identity, test.issuer); err == nil {
			t.Fatal("untrusted publisher/evidence accepted")
		}
	}
	_ = os.WriteFile(document, append(data, []byte(" changed")...), 0600)
	if err := verifyDetachedDocument(document, evidence, directory, "cosign_key", "vendor", "", ""); err == nil {
		t.Fatal("changed authenticated document accepted")
	}
	if err := verifyApplicationArchive(filepath.Join(directory, "nonexistent-archive"), document, evidence, directory, "cosign_key", "vendor", "", ""); err == nil || err.Error() != "application archive publisher authentication failed" {
		t.Fatal("archive opened before publisher authentication")
	}
}
