package contract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func signedApprovalFixture(t *testing.T) (Approval, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	manifest := ApprovalManifest{
		Version: ApprovalManifestVersionV1, DeploymentId: "deployment-a", SessionId: "session-a", TurnId: "turn-a",
		ApprovalId: "approval-a", ActionId: "action-a", Capability: "resource.restart", Effect: "write",
		ArgumentDigest: strings.Repeat("a", 64), DisclosureDigest: strings.Repeat("b", 64),
		ModeRevision: 3, PolicyRevision: 4, FencingEpoch: 5, ExpiresAt: expires,
		Resources: []ApprovalManifestResource{{Kind: "fixture.resource", Id: "resource-a", RequiredVerb: "resource.restart"}},
	}
	payload, err := ApprovalManifestSigningBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return Approval{ApprovalId: "approval-a", ActionId: "action-a", State: "pending", ExpiresAt: expires, Manifest: manifest}, publicKey
}

func TestVerifyApprovalManifestBindsSignedAuthorityToOuterResponse(t *testing.T) {
	approval, publicKey := signedApprovalFixture(t)
	digest, err := VerifyApprovalManifest(approval, publicKey, "deployment-a", approval.ExpiresAt.Add(-time.Minute))
	if err != nil || len(digest) != 64 {
		t.Fatalf("verify digest = %q, %v", digest, err)
	}
	for name, mutate := range map[string]func(*Approval){
		"outer approval": func(value *Approval) { value.ApprovalId = "approval-b" },
		"outer action":   func(value *Approval) { value.ActionId = "action-b" },
		"outer expiry":   func(value *Approval) { value.ExpiresAt = value.ExpiresAt.Add(time.Second) },
		"deployment":     func(value *Approval) { value.Manifest.DeploymentId = "deployment-b" },
		"session":        func(value *Approval) { value.Manifest.SessionId = "session-b" },
		"turn":           func(value *Approval) { value.Manifest.TurnId = "turn-b" },
		"capability":     func(value *Approval) { value.Manifest.Capability = "resource.replace" },
		"argument":       func(value *Approval) { value.Manifest.ArgumentDigest = strings.Repeat("c", 64) },
		"disclosure":     func(value *Approval) { value.Manifest.DisclosureDigest = strings.Repeat("d", 64) },
		"mode revision":  func(value *Approval) { value.Manifest.ModeRevision++ },
		"policy revision": func(value *Approval) {
			value.Manifest.PolicyRevision++
		},
		"fencing":  func(value *Approval) { value.Manifest.FencingEpoch++ },
		"resource": func(value *Approval) { value.Manifest.Resources[0].Id = "resource-b" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := approval
			changed.Manifest.Resources = append([]ApprovalManifestResource(nil), approval.Manifest.Resources...)
			mutate(&changed)
			if _, err := VerifyApprovalManifest(changed, publicKey, "deployment-a", approval.ExpiresAt.Add(-time.Minute)); err == nil {
				t.Fatal("changed authority verified")
			}
		})
	}
}

func TestVerifyApprovalManifestRejectsUnsignedExpiredAndWrongTrust(t *testing.T) {
	approval, publicKey := signedApprovalFixture(t)
	unsigned := approval
	unsigned.Manifest.Signature = ""
	if _, err := VerifyApprovalManifest(unsigned, publicKey, "deployment-a", approval.ExpiresAt.Add(-time.Minute)); err == nil {
		t.Fatal("unsigned approval verified")
	}
	if _, err := VerifyApprovalManifest(approval, publicKey, "deployment-a", approval.ExpiresAt); err == nil {
		t.Fatal("approval verified at expiry")
	}
	wrongKey, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyApprovalManifest(approval, wrongKey, "deployment-a", approval.ExpiresAt.Add(-time.Minute)); err == nil {
		t.Fatal("approval verified under the wrong onboarding trust")
	}
}
