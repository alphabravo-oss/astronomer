package resolver

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

const (
	defaultCosignPath = "/usr/local/bin/cosign"
	defaultTrustDir   = "/etc/astronomer/delivery-trust"
	maxVerifierOutput = 1 << 20
	maxTrustKeyBytes  = 1 << 20
)

// ExecVerifier is the production cryptographic verifier. OCI and Helm
// signatures are checked with the pinned cosign binary; Git commit signatures
// are checked in-process against an operator-mounted armored public keyring.
// It never places credentials, keys, source URLs, or command output in errors.
type ExecVerifier struct {
	cosignPath string
	trustDir   string
}

func NewExecVerifier(cosignPath, trustDirectory string) (*ExecVerifier, error) {
	if strings.TrimSpace(cosignPath) == "" {
		cosignPath = defaultCosignPath
	}
	if strings.TrimSpace(trustDirectory) == "" {
		trustDirectory = defaultTrustDir
	}
	if !filepath.IsAbs(cosignPath) || !filepath.IsAbs(trustDirectory) {
		return nil, errors.New("delivery verifier paths must be absolute")
	}
	info, err := os.Stat(cosignPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return nil, errors.New("pinned cosign verifier binary is unavailable")
	}
	return &ExecVerifier{cosignPath: cosignPath, trustDir: filepath.Clean(trustDirectory)}, nil
}

func (v *ExecVerifier) Verify(ctx context.Context, input VerificationInput) (Verification, error) {
	if v == nil {
		return Verification{}, errors.New("source verifier is unavailable")
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
	identity, keyPath, err := v.cosignPolicyArgs(input.Source.Trust)
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
		if err := v.verifyBlobWithPolicy(ctx, candidate, keyPath); err == nil {
			return Verification{Status: "verified", Identity: identity, Provider: "cosign"}, nil
		}
	}
	return Verification{}, errors.New("OCI signature verification failed")
}

func (v *ExecVerifier) verifyBlob(ctx context.Context, input VerificationInput) (Verification, error) {
	if len(input.Artifact) == 0 || len(input.Signature) == 0 {
		return Verification{}, errors.New("detached artifact signature is unavailable")
	}
	identity, keyPath, err := v.cosignPolicyArgs(input.Source.Trust)
	if err != nil {
		return Verification{}, err
	}
	if err := v.verifyBlobWithPolicy(ctx, input, keyPath); err != nil {
		return Verification{}, err
	}
	return Verification{Status: "verified", Identity: identity, Provider: "cosign"}, nil
}

func (v *ExecVerifier) verifyBlobWithPolicy(ctx context.Context, input VerificationInput, keyPath string) error {
	temporary, err := os.MkdirTemp("", "astronomer-cosign-verify-*")
	if err != nil {
		return errors.New("signature verification workspace is unavailable")
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return errors.New("signature verification workspace is unavailable")
	}
	blobPath := filepath.Join(temporary, "artifact")
	signaturePath := filepath.Join(temporary, "artifact.sig")
	if err := writePrivateFile(blobPath, input.Artifact); err != nil {
		return err
	}
	if err := writePrivateFile(signaturePath, input.Signature); err != nil {
		return err
	}
	// --offline is load-bearing: every remote byte (including the Rekor bundle)
	// was already fetched by the policy-bound resolver. The subprocess receives
	// no URL or registry credential and therefore has no attacker-influenced
	// network destination to follow.
	args := []string{"verify-blob", "--offline", "--signature", signaturePath}
	if keyPath != "" {
		args = append(args, "--key", keyPath)
	} else {
		if len(input.Certificate) == 0 || len(input.Bundle) == 0 {
			return errors.New("offline keyless signing evidence is unavailable")
		}
		certificatePath := filepath.Join(temporary, "artifact.pem")
		if err := writePrivateFile(certificatePath, input.Certificate); err != nil {
			return err
		}
		bundlePath := filepath.Join(temporary, "rekor.bundle")
		if err := writePrivateFile(bundlePath, input.Bundle); err != nil {
			return err
		}
		args = append(args, "--certificate", certificatePath,
			"--bundle", bundlePath,
			"--certificate-identity", input.Source.Trust.Identity,
			"--certificate-oidc-issuer", input.Source.Trust.Issuer)
	}
	args = append(args, blobPath)
	return v.runCosign(ctx, args, temporary)
}

func (v *ExecVerifier) cosignPolicyArgs(policy model.TrustPolicy) (identity, keyPath string, err error) {
	switch policy.Provider {
	case model.SignatureCosignKey:
		key, path, readErr := v.readTrustKey(policy.KeyRef, ".pub")
		if readErr != nil {
			return "", "", readErr
		}
		clearBytes(key)
		return "key:" + policy.KeyRef, path, nil
	case model.SignatureCosignKeyless:
		if strings.TrimSpace(policy.Identity) == "" || strings.TrimSpace(policy.Issuer) == "" {
			return "", "", errors.New("keyless signature identity policy is incomplete")
		}
		return policy.Identity, "", nil
	default:
		return "", "", errors.New("cosign signature provider is required")
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

func (v *ExecVerifier) runCosign(ctx context.Context, args []string, existingTemporary string) error {
	temporary := existingTemporary
	if temporary == "" {
		var err error
		temporary, err = os.MkdirTemp("", "astronomer-cosign-runtime-*")
		if err != nil {
			return errors.New("signature verification workspace is unavailable")
		}
		defer func() { _ = os.RemoveAll(temporary) }()
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		return errors.New("signature verification workspace is unavailable")
	}
	environment := []string{
		"HOME=" + temporary, "DOCKER_CONFIG=" + temporary, "COSIGN_EXPERIMENTAL=1",
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	command := exec.CommandContext(ctx, v.cosignPath, args...)
	command.Env = environment
	command.Dir = temporary
	output := &boundedOutput{remaining: maxVerifierOutput}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	output.Clear()
	if err != nil || output.exceeded {
		return errors.New("cosign signature verification failed")
	}
	return nil
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("signature verification workspace is unavailable")
	}
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("signature verification workspace is unavailable")
	}
	return nil
}

type boundedOutput struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:max(0, b.remaining)]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.buffer.Write(value)
	return original, nil
}

func (b *boundedOutput) Clear() {
	data := b.buffer.Bytes()
	clearBytes(data)
	b.buffer.Reset()
}

var _ Verifier = (*ExecVerifier)(nil)
