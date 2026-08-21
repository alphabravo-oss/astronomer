package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	sigstorebundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

const (
	defaultTrustDir        = "/etc/astronomer/delivery-trust"
	trustedRootRef         = "trusted_root"
	maxTrustKeyBytes       = 1 << 20
	cosignV2BundleMediaTyp = "application/vnd.dev.sigstore.bundle+json;version=0.1"
)

// ExecVerifier is the production cryptographic verifier. OCI and Helm
// signatures are checked in-process with sigstore-go against operator-mounted
// trust material. Git commit signatures are checked in-process against an
// armored public keyring. It never places credentials, keys, source URLs, or
// library output in errors, and it never contacts TUF, Rekor, or Fulcio.
type ExecVerifier struct {
	trustDir string
}

func NewExecVerifier(trustDirectory string) (*ExecVerifier, error) {
	if strings.TrimSpace(trustDirectory) == "" {
		trustDirectory = defaultTrustDir
	}
	if !filepath.IsAbs(trustDirectory) {
		return nil, errors.New("delivery verifier paths must be absolute")
	}
	return &ExecVerifier{trustDir: filepath.Clean(trustDirectory)}, nil
}

func (v *ExecVerifier) Verify(ctx context.Context, input VerificationInput) (Verification, error) {
	if v == nil {
		return Verification{}, errors.New("source verifier is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Verification{}, errors.New("signature verification failed")
	}
	switch input.Source.Type {
	case model.SourceGit:
		return v.verifyGit(input)
	case model.SourceOCIArtifact, model.SourceHelmOCI:
		return v.verifyOCI(ctx, input)
	case model.SourceHelmHTTP:
		return v.verifyBlob(ctx, input)
	default:
		return Verification{}, errors.New("source signature provider is unsupported")
	}
}

func (v *ExecVerifier) verifyGit(input VerificationInput) (Verification, error) {
	if input.Source.Trust.Provider != model.SignatureGit || input.Source.Trust.KeyRef == "" || len(input.Artifact) == 0 {
		return Verification{}, errors.New("Git signature verification inputs are incomplete")
	}
	key, _, err := v.readTrustKey(input.Source.Trust.KeyRef, ".asc")
	if err != nil {
		return Verification{}, err
	}
	defer clearBytes(key)
	encoded := &plumbing.MemoryObject{}
	encoded.SetType(plumbing.CommitObject)
	if _, err := encoded.Write(input.Artifact); err != nil {
		return Verification{}, errors.New("Git signed commit is invalid")
	}
	if encoded.Hash().String() != input.Revision.Value {
		return Verification{}, errors.New("Git signed commit identity does not match")
	}
	commit, err := object.DecodeCommit(nil, encoded)
	if err != nil {
		return Verification{}, errors.New("Git signed commit is invalid")
	}
	entity, err := commit.Verify(string(key))
	if err != nil || entity == nil || entity.PrimaryKey == nil {
		return Verification{}, errors.New("Git commit signature is invalid")
	}
	fingerprint := strings.ToLower(hex.EncodeToString(entity.PrimaryKey.Fingerprint))
	expected := strings.TrimSpace(input.Source.Trust.Identity)
	if expected != "" && !strings.EqualFold(expected, fingerprint) {
		matched := false
		for _, identity := range entity.Identities {
			if identity != nil && (identity.Name == expected || (identity.UserId != nil && identity.UserId.Email == expected)) {
				matched = true
				break
			}
		}
		if !matched {
			return Verification{}, errors.New("Git signature identity does not match policy")
		}
	}
	return Verification{Status: "verified", Identity: fingerprint, Provider: "openpgp"}, nil
}

func (v *ExecVerifier) verifyOCI(ctx context.Context, input VerificationInput) (Verification, error) {
	identity, err := v.cosignPolicyIdentity(input.Source.Trust)
	if err != nil {
		return Verification{}, err
	}
	if len(input.Evidence) == 0 {
		return Verification{}, errors.New("OCI signature evidence is unavailable")
	}
	for _, evidence := range input.Evidence {
		candidate := input
		candidate.Artifact = evidence.Payload
		candidate.Signature = evidence.Signature
		candidate.Certificate = evidence.Certificate
		candidate.Bundle = evidence.Bundle
		if err := v.verifyBlobWithPolicy(ctx, candidate); err == nil {
			return Verification{Status: "verified", Identity: identity, Provider: "cosign"}, nil
		}
	}
	return Verification{}, errors.New("OCI signature verification failed")
}

func (v *ExecVerifier) verifyBlob(ctx context.Context, input VerificationInput) (Verification, error) {
	if len(input.Artifact) == 0 || len(input.Signature) == 0 {
		return Verification{}, errors.New("detached artifact signature is unavailable")
	}
	identity, err := v.cosignPolicyIdentity(input.Source.Trust)
	if err != nil {
		return Verification{}, err
	}
	if err := v.verifyBlobWithPolicy(ctx, input); err != nil {
		return Verification{}, err
	}
	return Verification{Status: "verified", Identity: identity, Provider: "cosign"}, nil
}

func (v *ExecVerifier) verifyBlobWithPolicy(ctx context.Context, input VerificationInput) error {
	if err := ctx.Err(); err != nil {
		return errors.New("signature verification failed")
	}
	switch input.Source.Trust.Provider {
	case model.SignatureCosignKey:
		return v.verifyCosignKey(input)
	case model.SignatureCosignKeyless:
		return v.verifyCosignKeyless(input)
	default:
		return errors.New("cosign signature provider is required")
	}
}

func (v *ExecVerifier) verifyCosignKey(input VerificationInput) error {
	if len(input.Artifact) == 0 || len(input.Signature) == 0 {
		return errors.New("detached artifact signature is unavailable")
	}
	pemBytes, _, err := v.readTrustKey(input.Source.Trust.KeyRef, ".pub")
	if err != nil {
		return err
	}
	defer clearBytes(pemBytes)
	publicKey, err := cryptoutils.UnmarshalPEMToPublicKey(pemBytes)
	if err != nil || publicKey == nil {
		return errors.New("referenced signature verification key is unavailable")
	}
	verifier, err := signature.LoadDefaultVerifier(publicKey)
	if err != nil {
		return errors.New("referenced signature verification key is unavailable")
	}
	for _, candidate := range signatureCandidates(input.Signature) {
		if err := verifier.VerifySignature(bytes.NewReader(candidate), bytes.NewReader(input.Artifact)); err == nil {
			return nil
		}
	}
	return errors.New("signature verification failed")
}

func (v *ExecVerifier) verifyCosignKeyless(input VerificationInput) error {
	if len(input.Bundle) == 0 {
		return errors.New("offline keyless signing evidence is unavailable")
	}
	rootJSON, _, err := v.readTrustKey(trustedRootRef, ".json")
	if err != nil {
		return errors.New("offline keyless trusted root is unavailable")
	}
	defer clearBytes(rootJSON)
	trustedRoot, err := root.NewTrustedRootFromJSON(rootJSON)
	if err != nil {
		return errors.New("offline keyless trusted root is unavailable")
	}
	entity, err := signedEntityFromEvidence(input)
	if err != nil {
		return errors.New("offline keyless signing evidence is unavailable")
	}
	verifier, err := verify.NewVerifier(trustedRoot, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return errors.New("signature verification failed")
	}
	identity, err := verify.NewShortCertificateIdentity(input.Source.Trust.Issuer, "", input.Source.Trust.Identity, "")
	if err != nil {
		return errors.New("keyless signature identity policy is incomplete")
	}
	if _, err := verifier.Verify(entity, verify.NewPolicy(verify.WithArtifact(bytes.NewReader(input.Artifact)), verify.WithCertificateIdentity(identity))); err != nil {
		return errors.New("signature verification failed")
	}
	return nil
}

func (v *ExecVerifier) cosignPolicyIdentity(policy model.TrustPolicy) (string, error) {
	switch policy.Provider {
	case model.SignatureCosignKey:
		if _, _, err := v.readTrustKey(policy.KeyRef, ".pub"); err != nil {
			return "", err
		}
		return "key:" + policy.KeyRef, nil
	case model.SignatureCosignKeyless:
		if strings.TrimSpace(policy.Identity) == "" || strings.TrimSpace(policy.Issuer) == "" {
			return "", errors.New("keyless signature identity policy is incomplete")
		}
		return policy.Identity, nil
	default:
		return "", errors.New("cosign signature provider is required")
	}
}

func (v *ExecVerifier) readTrustKey(reference, suffix string) ([]byte, string, error) {
	if reference == "" || strings.ContainsAny(reference, "/\\\r\n\x00") {
		return nil, "", errors.New("signature key reference is invalid")
	}
	path := filepath.Join(v.trustDir, reference+suffix)
	if filepath.Dir(path) != v.trustDir {
		return nil, "", errors.New("signature key reference escapes the trust directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxTrustKeyBytes {
		return nil, "", errors.New("referenced signature verification key is unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > maxTrustKeyBytes {
		clearBytes(data)
		return nil, "", errors.New("referenced signature verification key is unavailable")
	}
	return data, path, nil
}

func signedEntityFromEvidence(input VerificationInput) (*sigstorebundle.Bundle, error) {
	entity := &sigstorebundle.Bundle{}
	if err := entity.UnmarshalJSON(input.Bundle); err == nil {
		return entity, nil
	}
	if len(input.Certificate) == 0 || len(input.Signature) == 0 {
		return nil, errors.New("offline keyless signing evidence is unavailable")
	}
	return bundleFromCosignV2(input.Artifact, input.Signature, input.Certificate, input.Bundle)
}

func bundleFromCosignV2(artifact, signatureValue, certificatePEM, rekorBundle []byte) (*sigstorebundle.Bundle, error) {
	cert, err := parseSigningCertificate(certificatePEM)
	if err != nil {
		return nil, err
	}
	sig := primarySignature(signatureValue)
	if len(sig) == 0 || len(artifact) == 0 {
		return nil, errors.New("offline keyless signing evidence is unavailable")
	}
	entry, err := transparencyLogEntryFromCosignBundle(rekorBundle)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(artifact)
	pb := &protobundle.Bundle{
		MediaType: cosignV2BundleMediaTyp,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_X509CertificateChain{
				X509CertificateChain: &protocommon.X509CertificateChain{
					Certificates: []*protocommon.X509Certificate{{RawBytes: cert.Raw}},
				},
			},
			TlogEntries: []*protorekor.TransparencyLogEntry{entry},
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{
					Algorithm: protocommon.HashAlgorithm_SHA2_256,
					Digest:    digest[:],
				},
				Signature: sig,
			},
		},
	}
	return sigstorebundle.NewBundle(pb)
}

type cosignRekorBundle struct {
	SignedEntryTimestamp []byte `json:"SignedEntryTimestamp"`
	Payload              struct {
		Body           json.RawMessage `json:"body"`
		IntegratedTime int64           `json:"integratedTime"`
		LogIndex       int64           `json:"logIndex"`
		LogID          string          `json:"logID"`
	} `json:"Payload"`
}

func transparencyLogEntryFromCosignBundle(raw []byte) (*protorekor.TransparencyLogEntry, error) {
	var bundle cosignRekorBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	if len(bundle.SignedEntryTimestamp) == 0 || bundle.Payload.LogID == "" || len(bundle.Payload.Body) == 0 {
		return nil, errors.New("offline keyless signing evidence is unavailable")
	}
	body, err := decodeRekorBody(bundle.Payload.Body)
	if err != nil || len(body) == 0 {
		return nil, errors.New("offline keyless signing evidence is unavailable")
	}
	logID, err := hex.DecodeString(bundle.Payload.LogID)
	if err != nil || len(logID) == 0 {
		return nil, errors.New("offline keyless signing evidence is unavailable")
	}
	kind, version := hashedRekordKind(body)
	return &protorekor.TransparencyLogEntry{
		LogIndex: bundle.Payload.LogIndex,
		LogId:    &protocommon.LogId{KeyId: logID},
		KindVersion: &protorekor.KindVersion{
			Kind:    kind,
			Version: version,
		},
		IntegratedTime: bundle.Payload.IntegratedTime,
		InclusionPromise: &protorekor.InclusionPromise{
			SignedEntryTimestamp: bundle.SignedEntryTimestamp,
		},
		CanonicalizedBody: body,
	}, nil
}

func decodeRekorBody(raw json.RawMessage) ([]byte, error) {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return bytes.TrimSpace(raw), nil
}

func hashedRekordKind(body []byte) (string, string) {
	var header struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
	}
	if json.Unmarshal(body, &header) != nil || header.Kind == "" {
		return "hashedrekord", "0.0.1"
	}
	if header.APIVersion == "" {
		return header.Kind, "0.0.1"
	}
	return header.Kind, header.APIVersion
}

func parseSigningCertificate(value []byte) (*x509.Certificate, error) {
	certs, err := cryptoutils.LoadCertificatesFromPEM(bytes.NewReader(value))
	if err == nil && len(certs) > 0 && certs[0] != nil {
		return certs[0], nil
	}
	return x509.ParseCertificate(value)
}

func signatureCandidates(value []byte) [][]byte {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil
	}
	candidates := [][]byte{trimmed}
	if decoded, err := base64.StdEncoding.DecodeString(string(trimmed)); err == nil && len(decoded) > 0 && !bytes.Equal(decoded, trimmed) {
		candidates = [][]byte{decoded, trimmed}
	}
	return candidates
}

func primarySignature(value []byte) []byte {
	candidates := signatureCandidates(value)
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

var _ Verifier = (*ExecVerifier)(nil)
