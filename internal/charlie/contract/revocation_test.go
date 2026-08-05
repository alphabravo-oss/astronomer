package contract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
)

func signedRevocationReceipt(t *testing.T, privateKey ed25519.PrivateKey, final bool) []byte {
	t.Helper()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	ordinal0, ordinal1 := 0, 1
	statuses := []CredentialRevocationStatus{
		{Purpose: RevocationCredentialPurposeAgentEnrollment, ReplicaOrdinal: &ordinal0, State: CredentialStateRevoked, RevokedAt: &now},
		{Purpose: RevocationCredentialPurposeAgentEnrollment, ReplicaOrdinal: &ordinal1, State: CredentialStateRevoked, RevokedAt: &now},
		{Purpose: RevocationCredentialPurposeArtifactPull, State: CredentialStateRevoked, RevokedAt: &now},
		{Purpose: RevocationCredentialPurposeProductAgent, State: CredentialStateRevoked, RevokedAt: &now},
		{Purpose: RevocationCredentialPurposeProductClient, State: CredentialStatePending},
	}
	state := CredentialRevocationPending
	if final {
		state = CredentialRevocationComplete
		statuses[len(statuses)-1].State = CredentialStateRevoked
		statuses[len(statuses)-1].RevokedAt = &now
	}
	receipt := CredentialRevocationReceipt{
		Complete: final, Credentials: statuses, DeploymentId: "deployment-1", IntegrationId: "integration-1",
		PackageId: "package-1", RequestId: "request-1", RevokedAt: now, Schema: CredentialRevocationSchemaV1,
		SigningKeyId: "key-1", State: state,
	}
	unsigned, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(unsigned, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "signature")
	unsigned, _ = json.Marshal(object)
	canonical, err := jcs.Transform(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, append([]byte(credentialRevocationSignatureDomain), canonical...)))
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifyCredentialRevocationReceiptTwoPhaseBinding(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, final := range []bool{false, true} {
		raw := signedRevocationReceipt(t, privateKey, final)
		if _, err := VerifyCredentialRevocationReceipt(raw, publicKey, "request-1", "deployment-1", "package-1", "integration-1", "key-1", 2, final); err != nil {
			t.Fatalf("final=%v valid signed receipt rejected: %v", final, err)
		}
	}
}

func TestVerifyCredentialRevocationReceiptRejectsTamperingWrongBindingAndIncompleteState(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := signedRevocationReceipt(t, privateKey, true)
	if _, err := VerifyCredentialRevocationReceipt(valid, publicKey, "request-wrong", "deployment-1", "package-1", "integration-1", "key-1", 2, true); err == nil {
		t.Fatal("signed receipt with the wrong request binding was accepted")
	}
	var tampered map[string]any
	_ = json.Unmarshal(valid, &tampered)
	tampered["package_id"] = "package-wrong"
	raw, _ := json.Marshal(tampered)
	if _, err := VerifyCredentialRevocationReceipt(raw, publicKey, "request-1", "deployment-1", "package-wrong", "integration-1", "key-1", 2, true); err == nil {
		t.Fatal("receipt modified after signing was accepted")
	}

	incomplete := CredentialRevocationReceipt{}
	_ = json.Unmarshal(valid, &incomplete)
	incomplete.Credentials = incomplete.Credentials[:len(incomplete.Credentials)-1]
	raw = signRevocationFixture(t, privateKey, incomplete)
	if _, err := VerifyCredentialRevocationReceipt(raw, publicKey, "request-1", "deployment-1", "package-1", "integration-1", "key-1", 2, true); err == nil {
		t.Fatal("signed but incomplete credential revocation was accepted")
	}
}

func signRevocationFixture(t *testing.T, privateKey ed25519.PrivateKey, receipt CredentialRevocationReceipt) []byte {
	t.Helper()
	receipt.Signature = ""
	raw, _ := json.Marshal(receipt)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	delete(object, "signature")
	unsigned, _ := json.Marshal(object)
	canonical, err := jcs.Transform(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, append([]byte(credentialRevocationSignatureDomain), canonical...)))
	raw, _ = json.Marshal(receipt)
	return raw
}
