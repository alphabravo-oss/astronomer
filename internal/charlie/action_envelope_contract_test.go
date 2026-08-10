package charlie

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// TestCharlieActionEnvelopeCrossLanguageVector pins the exact authorization
// bytes published by Charlie. The vector is produced by Charlie's Go signer and
// independently verified by its TypeScript agent and product example; passing
// it here prevents another repository-local signing format from drifting in.
func TestCharlieActionEnvelopeCrossLanguageVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/action-envelope-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Envelope  ActionEnvelope `json:"envelope"`
		PublicKey string         `json:"public_key"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(vector.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("Charlie action-envelope vector has an invalid public key")
	}
	signature, err := base64.RawURLEncoding.DecodeString(vector.Envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		t.Fatal("Charlie action-envelope vector has an invalid signature")
	}
	payload, err := actionEnvelopeSigningBytes(vector.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		t.Fatal("Astronomer does not reproduce Charlie's RFC 8785 action-envelope signing contract")
	}

	vector.Envelope.Arguments = json.RawMessage(`{"force":true,"nested":{"a":1,"b":2},"note":"scale <  1","resource_id":"scheduler-7"}`)
	reordered, err := actionEnvelopeSigningBytes(vector.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), reordered, signature) {
		t.Fatal("semantically identical reordered action arguments changed the RFC 8785 signature")
	}
}
