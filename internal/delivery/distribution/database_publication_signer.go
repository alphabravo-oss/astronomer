package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabasePublicationSigner is operator composition for the protected producer
// command. Public construction supports only the captured GitHub OIDC policy.
// Local-key QA construction is private to this package's real Cosign fixtures;
// no command/config/feed field selects it in production.
type DatabasePublicationSigner struct {
	binary        string
	binaryFile    *os.File
	binaryInfo    os.FileInfo
	binaryDigest  string
	qaKey         string
	qaPassword    string
	githubRequest *DatabasePublicationGitHubRequest
}

func NewDatabasePublicationSigner(binary string) (*DatabasePublicationSigner, error) {
	return newDatabasePublicationSigner(binary, "", "")
}

// The executable bootstrap passes an already captured protected request. No
// library or runner resolves process environment, provider or trust overrides.
func NewDatabasePublicationSignerWithGitHubOIDC(binary string, request *DatabasePublicationGitHubRequest) (*DatabasePublicationSigner, error) {
	if request == nil || !validDatabasePublicationGitHubRequest(request.requestURL, request.requestToken) {
		return nil, errors.New("database publisher protected GitHub request unavailable")
	}
	signer, err := newDatabasePublicationSigner(binary, "", "")
	if err != nil {
		return nil, err
	}
	captured := *request
	signer.githubRequest = &captured
	return signer, nil
}

func newDatabasePublicationSigner(binary, qaKey, qaPassword string) (*DatabasePublicationSigner, error) {
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
		return nil, errors.New("database publisher signer path invalid")
	}
	file, err := openDatabaseInspectionRegular(binary)
	if err != nil {
		return nil, errors.New("database publisher signer unavailable")
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Size() <= 0 || before.Size() > 256<<20 {
		_ = file.Close()
		return nil, errors.New("database publisher signer executable invalid")
	}
	hash := sha256.New()
	if copied, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1)); err != nil || copied != before.Size() || !sameDatabaseInspectionFile(file, binary, before) {
		_ = file.Close()
		return nil, errors.New("database publisher signer changed during capture")
	}
	return &DatabasePublicationSigner{binary: binary, binaryFile: file, binaryInfo: before, binaryDigest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), qaKey: qaKey, qaPassword: qaPassword}, nil
}
func (signer *DatabasePublicationSigner) Close() {
	if signer != nil && signer.binaryFile != nil {
		_ = signer.binaryFile.Close()
		signer.binaryFile = nil
	}
}
func (signer *DatabasePublicationSigner) current(ctx context.Context) error {
	if ctx == nil || signer == nil || signer.binaryFile == nil || !sameDatabaseInspectionFile(signer.binaryFile, signer.binary, signer.binaryInfo) {
		return errors.New("database publisher signer identity changed")
	}
	if _, err := signer.binaryFile.Seek(0, io.SeekStart); err != nil {
		return errors.New("database publisher signer read unavailable")
	}
	hash := sha256.New()
	copied, err := io.Copy(hash, io.LimitReader(databaseInspectionReader{ctx: ctx, input: signer.binaryFile}, (256<<20)+1))
	if err != nil {
		return supplierDatabaseError(ctx, "database publisher signer rehash unavailable")
	}
	if copied != signer.binaryInfo.Size() || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != signer.binaryDigest || !sameDatabaseInspectionFile(signer.binaryFile, signer.binary, signer.binaryInfo) {
		return errors.New("database publisher signer identity changed")
	}
	return ctx.Err()
}

type databaseSimpleSigningPayload struct {
	Critical struct {
		Identity struct {
			Repository string `json:"docker-reference"`
		} `json:"identity"`
		Image struct {
			Digest string `json:"docker-manifest-digest"`
		} `json:"image"`
		Type string `json:"type"`
	} `json:"critical"`
	Optional map[string]string `json:"optional"`
}

func (signer *DatabasePublicationSigner) sign(ctx context.Context, policy model.TrustPolicy, trustDirectory, repository, subject string) (resolver.SignatureEvidence, error) {
	var empty resolver.SignatureEvidence
	if ctx == nil || signer == nil || !validDatabaseUpdateSubject(repository+"@"+subject, subject) || policy.AllowUnsigned || policy.Validate() != nil {
		return empty, errors.New("database publisher signing policy invalid")
	}
	if signer.qaKey == "" && (policy.Provider != model.SignatureCosignKeyless || policy.Issuer != "https://token.actions.githubusercontent.com" || !strings.HasPrefix(policy.Identity, "https://github.com/alphabravo-oss/astronomer/.github/workflows/")) {
		return empty, errors.New("database publisher requires captured production GitHub identity")
	}
	if signer.qaKey != "" && policy.Provider != model.SignatureCosignKey {
		return empty, errors.New("database publisher QA policy differs")
	}
	if signer.qaKey == "" && (signer.githubRequest == nil || !validDatabasePublicationGitHubRequest(signer.githubRequest.requestURL, signer.githubRequest.requestToken)) {
		return empty, errors.New("database publisher protected GitHub request unavailable")
	}
	if err := signer.current(ctx); err != nil {
		return empty, err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	payload := databaseSimpleSigningPayload{Optional: map[string]string{}}
	payload.Critical.Identity.Repository = repository
	payload.Critical.Image.Digest = subject
	payload.Critical.Type = "cosign container image signature"
	content, err := json.Marshal(payload)
	if err != nil {
		return empty, errors.New("database publisher signature payload encoding failed")
	}
	directory, err := os.MkdirTemp("", "astronomer-database-signature-")
	if err != nil {
		return empty, errors.New("database publisher signature staging unavailable")
	}
	// The private directory contains only four fixed bounded metadata files. No
	// supplier layer or caller-selected cleanup path is passed to the signer.
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode().Perm() != 0700 {
		return empty, errors.New("database publisher signature staging identity unavailable")
	}
	ownedFiles := map[string]os.FileInfo{}
	defer cleanupDatabaseSignatureDirectory(directory, directoryInfo, ownedFiles)
	input := filepath.Join(directory, "payload.json")
	if err := os.WriteFile(input, content, 0600); err != nil {
		return empty, errors.New("database publisher signing payload staging unavailable")
	}
	if info, err := os.Lstat(input); err == nil {
		ownedFiles["payload.json"] = info
	} else {
		return empty, errors.New("database publisher signature input identity unavailable")
	}
	args := []string{"sign-blob", "--yes", "--use-signing-config=false", "--new-bundle-format=false", "--b64=false", "--output-signature", filepath.Join(directory, "signature"), "--output-certificate", filepath.Join(directory, "certificate"), "--bundle", filepath.Join(directory, "bundle")}
	if signer.qaKey != "" {
		args = append(args, "--key", signer.qaKey, "--tlog-upload=false")
	} else {
		args = append(args, "--oidc-provider=github-actions", "--trusted-root", filepath.Join(trustDirectory, "trusted_root.json"))
	}
	args = append(args, input)
	if err := runPinnedDatabaseSigner(bounded, signer.binaryFile, args, signer.qaKey != "", signer.qaPassword, signer.githubRequest); err != nil {
		return empty, err
	}
	if err := signer.current(bounded); err != nil {
		return empty, err
	}
	signature, signatureInfo, err := readDatabaseSigningOutput(filepath.Join(directory, "signature"), 1<<20, false)
	if err != nil {
		return empty, err
	}
	ownedFiles["signature"] = signatureInfo
	certificate, certificateInfo, err := readDatabaseSigningOutput(filepath.Join(directory, "certificate"), 1<<20, signer.qaKey != "")
	if err != nil {
		return empty, err
	}
	ownedFiles["certificate"] = certificateInfo
	bundle, bundleInfo, err := readDatabaseSigningOutput(filepath.Join(directory, "bundle"), 1<<20, signer.qaKey != "")
	if err != nil {
		return empty, err
	}
	ownedFiles["bundle"] = bundleInfo
	evidence := resolver.SignatureEvidence{Payload: content, Signature: []byte(base64.StdEncoding.EncodeToString(signature)), Certificate: certificate, Bundle: bundle}
	verifier, err := resolver.NewExecVerifier(trustDirectory)
	if err != nil {
		return empty, errors.New("database publisher canonical signer verification unavailable")
	}
	parsed, _ := model.ParseDigest(subject)
	verified, err := verifier.Verify(bounded, resolver.VerificationInput{Source: model.Source{Type: model.SourceOCIArtifact, Trust: policy}, Revision: model.ImmutableRevision{Kind: model.RevisionOCIDigest, Value: subject, ArtifactDigest: parsed}, Evidence: []resolver.SignatureEvidence{evidence}})
	if err != nil || verified.Status != "verified" {
		return empty, supplierDatabaseError(bounded, "database publisher local signature failed exact canonical policy")
	}
	return evidence, nil
}

func readDatabaseSigningOutput(path string, maximum int64, optional bool) ([]byte, os.FileInfo, error) {
	if optional {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
	}
	file, err := openDatabaseInspectionRegular(path)
	if err != nil {
		return nil, nil, errors.New("database publisher signature output unavailable")
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() <= 0 || before.Size() > maximum {
		return nil, nil, errors.New("database publisher signature output invalid")
	}
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(value)) != before.Size() || !sameDatabaseInspectionFile(file, path, before) {
		return nil, nil, errors.New("database publisher signature output changed or exceeds bound")
	}
	return value, before, nil
}

func cleanupDatabaseSignatureDirectory(directory string, before os.FileInfo, ownedFiles map[string]os.FileInfo) {
	// Only captured owned regular inodes are removed; partial unbound outputs and
	// same-UID replacements are preserved. The caller may retain failed staging.
	current, err := os.Lstat(directory)
	if err != nil || before == nil || !current.IsDir() || current.Mode().Perm() != 0700 || !os.SameFile(current, before) {
		return
	}
	for name, owned := range ownedFiles {
		if owned == nil || !owned.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		directoryNow, directoryError := os.Lstat(directory)
		if directoryError != nil || !os.SameFile(directoryNow, before) {
			return
		}
		if err == nil && info.Mode().IsRegular() && os.SameFile(info, owned) {
			_ = os.Remove(path)
		}
	}
	current, err = os.Lstat(directory)
	if err == nil && os.SameFile(current, before) {
		_ = os.Remove(directory)
	}
}
