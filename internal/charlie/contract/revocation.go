package contract

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gowebpki/jcs"
)

const credentialRevocationSignatureDomain = "charlie.credential-revocation/v1\n"

// VerifyCredentialRevocationReceipt verifies the signed raw response before
// trusting its generated representation. Canonicalization starts from the raw
// JSON so timestamp spelling cannot change through a time.Time round trip.
func VerifyCredentialRevocationReceipt(raw []byte, publicKey ed25519.PublicKey, expectedRequestID, expectedDeploymentID, expectedPackageID, expectedIntegrationID, expectedSigningKeyID string, expectedReplicas int, final bool) (CredentialRevocationReceipt, error) {
	if len(publicKey) != ed25519.PublicKeySize || expectedReplicas < 2 || expectedReplicas > 20 {
		return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation trust is incomplete")
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return CredentialRevocationReceipt{}, fmt.Errorf("decode Charlie credential revocation receipt: %w", err)
	}
	signatureText, ok := object["signature"].(string)
	if !ok || strings.TrimSpace(signatureText) == "" {
		return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation receipt is unsigned")
	}
	delete(object, "signature")
	unsigned, err := json.Marshal(object)
	if err != nil {
		return CredentialRevocationReceipt{}, err
	}
	canonical, err := jcs.Transform(unsigned)
	if err != nil {
		return CredentialRevocationReceipt{}, fmt.Errorf("canonicalize Charlie credential revocation receipt: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, append([]byte(credentialRevocationSignatureDomain), canonical...), signature) {
		return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation receipt signature is invalid")
	}
	var receipt CredentialRevocationReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return CredentialRevocationReceipt{}, fmt.Errorf("decode signed Charlie credential revocation receipt: %w", err)
	}
	if receipt.Schema != CredentialRevocationSchemaV1 || string(receipt.RequestId) != expectedRequestID ||
		string(receipt.DeploymentId) != expectedDeploymentID || string(receipt.PackageId) != expectedPackageID ||
		string(receipt.IntegrationId) != expectedIntegrationID || string(receipt.SigningKeyId) != expectedSigningKeyID || receipt.RevokedAt.IsZero() {
		return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation receipt binding is invalid")
	}
	if final {
		if !receipt.Complete || receipt.State != CredentialRevocationComplete {
			return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation is not complete")
		}
	} else if receipt.Complete || receipt.State != CredentialRevocationPending {
		return CredentialRevocationReceipt{}, fmt.Errorf("Charlie credential revocation did not enter pending caller revocation")
	}
	if err := verifyCredentialRevocationStatuses(receipt.Credentials, expectedReplicas, final); err != nil {
		return CredentialRevocationReceipt{}, err
	}
	return receipt, nil
}

func verifyCredentialRevocationStatuses(statuses []CredentialRevocationStatus, replicas int, final bool) error {
	if len(statuses) != replicas+3 {
		return fmt.Errorf("Charlie credential revocation receipt is incomplete")
	}
	seen := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		purpose := string(status.Purpose)
		key := purpose
		switch status.Purpose {
		case RevocationCredentialPurposeAgentEnrollment:
			if status.ReplicaOrdinal == nil || *status.ReplicaOrdinal < 0 || *status.ReplicaOrdinal >= replicas {
				return fmt.Errorf("Charlie enrollment credential revocation ordinal is invalid")
			}
			key += fmt.Sprintf(":%d", *status.ReplicaOrdinal)
		case RevocationCredentialPurposeArtifactPull, RevocationCredentialPurposeProductAgent, RevocationCredentialPurposeProductClient:
			if status.ReplicaOrdinal != nil {
				return fmt.Errorf("Charlie non-replica credential revocation has an ordinal")
			}
		default:
			return fmt.Errorf("Charlie credential revocation purpose is invalid")
		}
		if seen[key] {
			return fmt.Errorf("Charlie credential revocation receipt contains duplicate status")
		}
		seen[key] = true

		pendingCaller := !final && status.Purpose == RevocationCredentialPurposeProductClient
		if pendingCaller {
			if status.State != CredentialStatePending || status.RevokedAt != nil {
				return fmt.Errorf("Charlie caller credential revocation state is invalid")
			}
		} else if status.State != CredentialStateRevoked || status.RevokedAt == nil || status.RevokedAt.IsZero() {
			return fmt.Errorf("Charlie credential revocation status is not durably revoked")
		}
	}
	return nil
}
