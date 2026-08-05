package contract

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gowebpki/jcs"
)

const ApprovalManifestVersionV1 = "charlie.approval-manifest/v1"

var (
	approvalManifestIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	approvalManifestNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,127}$`)
	approvalManifestHashPattern = regexp.MustCompile(`^(?:sha256:)?[a-f0-9]{64}$`)
)

type unsignedApprovalManifest struct {
	Version          string                     `json:"version"`
	DeploymentID     OpaqueId                   `json:"deployment_id"`
	SessionID        OpaqueId                   `json:"session_id"`
	TurnID           OpaqueId                   `json:"turn_id"`
	ApprovalID       OpaqueId                   `json:"approval_id"`
	ActionID         OpaqueId                   `json:"action_id"`
	Capability       string                     `json:"capability"`
	Effect           string                     `json:"effect"`
	ArgumentDigest   string                     `json:"argument_digest"`
	DisclosureDigest string                     `json:"disclosure_digest"`
	ModeRevision     int64                      `json:"mode_revision"`
	PolicyRevision   int64                      `json:"policy_revision"`
	FencingEpoch     int64                      `json:"fencing_epoch"`
	ExpiresAt        time.Time                  `json:"expires_at"`
	Resources        []ApprovalManifestResource `json:"resources"`
}

// ApprovalManifestSigningBytes returns the exact domain-separated RFC 8785
// payload covered by Charlie's onboarding Ed25519 key. No outer Approval field
// participates in this payload.
func ApprovalManifestSigningBytes(manifest ApprovalManifest) ([]byte, error) {
	if err := validateApprovalManifest(manifest, false); err != nil {
		return nil, err
	}
	unsigned := unsignedApprovalManifest{
		Version: string(manifest.Version), DeploymentID: manifest.DeploymentId,
		SessionID: manifest.SessionId, TurnID: manifest.TurnId, ApprovalID: manifest.ApprovalId,
		ActionID: manifest.ActionId, Capability: manifest.Capability, Effect: string(manifest.Effect),
		ArgumentDigest: manifest.ArgumentDigest, DisclosureDigest: manifest.DisclosureDigest,
		ModeRevision: manifest.ModeRevision, PolicyRevision: manifest.PolicyRevision,
		FencingEpoch: manifest.FencingEpoch, ExpiresAt: manifest.ExpiresAt.UTC(),
		Resources: append([]ApprovalManifestResource(nil), manifest.Resources...),
	}
	raw, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("encode Charlie approval manifest: %w", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Charlie approval manifest: %w", err)
	}
	return append([]byte(ApprovalManifestVersionV1+"\n"), canonical...), nil
}

// VerifyApprovalManifest verifies Charlie's signature and the exact signed-to-
// outer tuple. The returned digest binds a later approval decision to this
// signed manifest. Callers must still rederive resources/verb/argument digest
// from the local capability schema and check live revisions, fencing, and RBAC.
func VerifyApprovalManifest(approval Approval, publicKey ed25519.PublicKey, expectedDeploymentID string, now time.Time) (string, error) {
	manifest := approval.Manifest
	if len(publicKey) != ed25519.PublicKeySize || !approvalManifestIDPattern.MatchString(expectedDeploymentID) {
		return "", errors.New("Charlie approval verification trust or deployment is invalid")
	}
	if err := validateApprovalManifest(manifest, true); err != nil {
		return "", err
	}
	if string(manifest.DeploymentId) != expectedDeploymentID || manifest.ApprovalId != approval.ApprovalId ||
		manifest.ActionId != approval.ActionId || !manifest.ExpiresAt.Equal(approval.ExpiresAt) {
		return "", errors.New("Charlie approval manifest does not match its authoritative product context")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !now.UTC().Before(manifest.ExpiresAt) {
		return "", errors.New("Charlie approval manifest is expired")
	}
	signature, err := base64.RawURLEncoding.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", errors.New("Charlie approval signature is invalid")
	}
	payload, err := ApprovalManifestSigningBytes(manifest)
	if err != nil {
		return "", err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return "", errors.New("Charlie approval signature verification failed")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode signed Charlie approval manifest: %w", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize signed Charlie approval manifest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func validateApprovalManifest(manifest ApprovalManifest, requireSignature bool) error {
	if string(manifest.Version) != ApprovalManifestVersionV1 || string(manifest.Effect) != "write" ||
		!approvalManifestNamePattern.MatchString(manifest.Capability) ||
		!approvalManifestHashPattern.MatchString(manifest.ArgumentDigest) ||
		!approvalManifestHashPattern.MatchString(manifest.DisclosureDigest) ||
		manifest.ModeRevision < 1 || manifest.PolicyRevision < 1 || manifest.FencingEpoch < 1 || manifest.ExpiresAt.IsZero() {
		return errors.New("Charlie approval manifest authority is invalid")
	}
	for _, value := range []string{string(manifest.DeploymentId), string(manifest.SessionId), string(manifest.TurnId), string(manifest.ApprovalId), string(manifest.ActionId)} {
		if !approvalManifestIDPattern.MatchString(value) {
			return errors.New("Charlie approval manifest identifier is invalid")
		}
	}
	if len(manifest.Resources) == 0 || len(manifest.Resources) > 25 {
		return errors.New("Charlie approval manifest scope is invalid")
	}
	seen := make(map[string]struct{}, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		if strings.TrimSpace(resource.Kind) == "" || len(resource.Kind) > 128 ||
			!approvalManifestIDPattern.MatchString(string(resource.Id)) || !approvalManifestNamePattern.MatchString(resource.RequiredVerb) {
			return errors.New("Charlie approval manifest resource is invalid")
		}
		key := resource.Kind + "\x00" + string(resource.Id) + "\x00" + resource.RequiredVerb
		if _, duplicate := seen[key]; duplicate {
			return errors.New("Charlie approval manifest resource is duplicated")
		}
		seen[key] = struct{}{}
	}
	if !slices.IsSortedFunc(manifest.Resources, func(a, b ApprovalManifestResource) int {
		return strings.Compare(a.Kind+"\x00"+string(a.Id)+"\x00"+a.RequiredVerb,
			b.Kind+"\x00"+string(b.Id)+"\x00"+b.RequiredVerb)
	}) {
		return errors.New("Charlie approval manifest resources are not canonical")
	}
	if requireSignature && strings.TrimSpace(manifest.Signature) == "" {
		return errors.New("Charlie approval signature is missing")
	}
	return nil
}
