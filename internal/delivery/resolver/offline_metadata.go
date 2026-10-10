package resolver

import (
	"context"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// VerifyOfflineMetadata reuses the production keyless verifier for the exact
// bytes of a bounded release manifest or recovery record and its self-contained
// Sigstore bundle. It does not classify a blob as OCI simple-signing evidence,
// invent a detached signature, or retrieve trust roots/tlog/certificate data.
// The caller must independently bind the original trusted-root scope, exact
// publisher policy, artifact file identities, semantic schema and released tag.
// Signature success alone never proves chart/image membership or upgrade safety.
func (v *ExecVerifier) VerifyOfflineMetadata(ctx context.Context, artifact, bundle []byte, policy model.TrustPolicy) (Verification, error) {
	if ctx == nil || v == nil {
		return Verification{}, errors.New("explicit offline metadata verification context required")
	}
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	// The public metadata bound is independent of large chart/database archives.
	// Existing OCI, Git and Helm HTTP verification contracts remain unchanged.
	if len(artifact) == 0 || len(artifact) > 1<<20 || len(bundle) == 0 || len(bundle) > 1<<20 {
		return Verification{}, errors.New("offline metadata or bundle exceeds its bounded contract")
	}
	if policy.Provider != model.SignatureCosignKeyless || policy.AllowUnsigned || policy.Validate() != nil {
		return Verification{}, errors.New("exact signed keyless metadata publisher policy required")
	}
	identity, err := v.cosignPolicyIdentity(policy)
	if err != nil {
		return Verification{}, err
	}
	// A self-contained message-signature bundle supplies its own signature. The
	// existing verifier enforces artifact digest, certificate identity/issuer,
	// mounted trusted root, one transparency log and one observer timestamp.
	input := VerificationInput{Source: model.Source{Trust: policy}, Artifact: artifact, Bundle: bundle}
	if err := v.verifyBlobWithPolicy(ctx, input); err != nil {
		return Verification{}, err
	}
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	return Verification{Status: "verified", Identity: identity, Provider: "cosign"}, nil
}
