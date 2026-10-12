//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

const metadataIssuer = "https://token.actions.githubusercontent.com"
const recoveryTag = "v1.2.0"
const recoverySourceCommit = "4a9986c22a582d71273da61e398b03f0dde4e8d8"
const recoverySourceRun = "37705872945"
const recoveryPublisher = "https://github.com/alphabravo-oss/astronomer/.github/workflows/resume-release.yaml@refs/heads/main"

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var sourceCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)
var runID = regexp.MustCompile(`^[1-9][0-9]*$`)
var contentSHA = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// SignedReleaseMetadata verifies the bounded release manifest with the canonical
// in-process verifier and constructor-owned mounted trust snapshot. It does NOT
// verify chart archive/OCI equality, GitHub attestations, seven image signatures,
// runtime evidence or backup/replacement authority. Full Verify must compose
// those gates before a mutating Driver can use this metadata.
type SignedReleaseMetadata struct {
	target, directory string
	policy            model.TrustPolicy
	trust             *resolver.CosignTrustSnapshot
	verifier          *resolver.ExecVerifier
	inputs            map[string]metadataInput
	contentInputs     map[string]metadataInput
	contentPolicy     ArtifactPolicy
}
type metadataInput struct {
	identity os.FileInfo
	hash     [32]byte
}
type recoveryRecord struct {
	SchemaVersion  *uint32           `json:"schema_version"`
	Tag            string            `json:"tag"`
	SourceCommit   string            `json:"source_commit"`
	SourceRunID    string            `json:"source_run_id"`
	ProducerRunID  string            `json:"producer_run_id"`
	ProducerCommit string            `json:"producer_commit"`
	Files          map[string]string `json:"files"`
}

// NewSignedReleaseMetadata takes only explicit operator filesystem composition,
// never trust material/publisher authority from the unverified manifest. Inputs
// use existing private0700/0600 bounded file contracts. Recovery proof presence
// is discovered from both canonical names; callers cannot hide it with a bool.
func NewSignedReleaseMetadata(ctx context.Context, directory, trustDirectory, target string, contentPolicy ArtifactPolicy) (*SignedReleaseMetadata, error) {
	if ctx == nil || !filepath.IsAbs(directory) || !filepath.IsAbs(trustDirectory) || !releaseTag.MatchString(target) || contentPolicy.MaximumBytes <= 0 {
		return nil, errors.New("explicit release metadata paths, exact stable target and context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &SignedReleaseMetadata{target: target, directory: directory, inputs: make(map[string]metadataInput), contentInputs: make(map[string]metadataInput), contentPolicy: contentPolicy}
	s.policy = model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + target, Issuer: metadataIssuer}
	proof, proofErr := os.Lstat(filepath.Join(directory, "release-recovery.json"))
	bundle, bundleErr := os.Lstat(filepath.Join(directory, "release-recovery.sigstore.json"))
	absent := os.IsNotExist(proofErr) && os.IsNotExist(bundleErr)
	names := []string{"release-manifest.json", "release-manifest.sigstore.json"}
	if !absent {
		if proofErr != nil || bundleErr != nil || proof == nil || bundle == nil || target != recoveryTag {
			return nil, errors.New("incomplete or unsupported release recovery metadata")
		}
		s.policy.Identity = recoveryPublisher
		names = append(names, "release-recovery.json", "release-recovery.sigstore.json")
	}
	for _, name := range names {
		identity, hash, err := privateBoundInput(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		s.inputs[name] = metadataInput{identity, hash}
	}
	if !absent {
		for _, name := range []string{"runtime-image-evidence.json", "runtime-image-evidence.tar.gz", "astronomer-1.2.0.tgz"} {
			path := filepath.Join(directory, name)
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, errors.New("read optional recovery artifact identity")
			}
			bound, err := bindMetadataArtifact(ctx, path, contentPolicy.MaximumBytes)
			if err != nil {
				return nil, err
			}
			s.contentInputs[name] = bound
		}
	}
	snapshot, err := resolver.CaptureCosignTrust(ctx, trustDirectory, s.policy)
	if err != nil {
		return nil, err
	}
	s.trust = snapshot
	verifier, err := resolver.NewExecVerifier(snapshot.FrozenDirectory())
	if err != nil {
		snapshot.Close()
		return nil, err
	}
	s.verifier = verifier
	if _, err := s.Verify(ctx); err != nil {
		snapshot.Close()
		return nil, err
	}
	return s, nil
}

// Close retires only the canonical owned public trust snapshot, never release
// inputs, backup or checkpoint. CaptureCosignTrust fences its own cleanup.
func (s *SignedReleaseMetadata) Close() {
	if s != nil && s.trust != nil {
		s.trust.Close()
	}
}

func (s *SignedReleaseMetadata) read(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bound, ok := s.inputs[name]
	if !ok {
		return nil, errors.New("release input was not initially captured")
	}
	identity, hash, err := privateBoundInput(filepath.Join(s.directory, name))
	if err != nil {
		return nil, err
	}
	if !os.SameFile(bound.identity, identity) || bound.hash != hash {
		return nil, errors.New("initial release input identity or content changed")
	}
	raw, err := readCapture(s.directory, name, 1<<20, "sha256:"+hex.EncodeToString(hash[:]))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

// Verify rechecks the mounted original plus owned trust snapshot before/after
// actual cryptography, and decodes ONLY the exact authenticated frozen bytes.
// Every caller receives a fresh value; mutable public Manifest is not a proof
// capsule. Full Driver must repeat this method before applying its bound plan.
func (s *SignedReleaseMetadata) Verify(ctx context.Context) (releasecontract.Manifest, error) {
	var empty releasecontract.Manifest
	if ctx == nil || s == nil || s.trust == nil || s.verifier == nil {
		return empty, errors.New("explicit signed release composition required")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := s.trust.Revalidate(ctx); err != nil {
		return empty, err
	}
	manifestRaw, err := s.read(ctx, "release-manifest.json")
	if err != nil {
		return empty, err
	}
	bundle, err := s.read(ctx, "release-manifest.sigstore.json")
	if err != nil {
		return empty, err
	}
	if _, err := s.verifier.VerifyOfflineMetadata(ctx, manifestRaw, bundle, s.policy); err != nil {
		return empty, err
	}
	manifest, err := decodeAuthenticatedManifest(manifestRaw, s.target)
	if err != nil {
		return empty, err
	}
	if s.policy.Identity == recoveryPublisher {
		raw, err := s.read(ctx, "release-recovery.json")
		if err != nil {
			return empty, err
		}
		bundle, err := s.read(ctx, "release-recovery.sigstore.json")
		if err != nil {
			return empty, err
		}
		if _, err := s.verifier.VerifyOfflineMetadata(ctx, raw, bundle, s.policy); err != nil {
			return empty, err
		}
		// Metadata-only behavior preserves the old verifier's optional local file
		// presence. Each later chart/runtime consumer must bind its actual bytes to
		// the corresponding digest; this is not a missing-file authorization grant.
		for _, name := range []string{"runtime-image-evidence.json", "runtime-image-evidence.tar.gz", "astronomer-1.2.0.tgz"} {
			_, currentErr := os.Lstat(filepath.Join(s.directory, name))
			_, wasPresent := s.contentInputs[name]
			if (os.IsNotExist(currentErr) && wasPresent) || (currentErr == nil && !wasPresent) || (currentErr != nil && !os.IsNotExist(currentErr)) {
				return empty, errors.New("optional recovery artifact presence changed")
			}
		}
		digests := make(map[string][32]byte)
		for name, initial := range s.contentInputs {
			actual, err := bindMetadataArtifact(ctx, filepath.Join(s.directory, name), s.contentPolicy.MaximumBytes)
			if err != nil {
				return empty, err
			}
			if !os.SameFile(initial.identity, actual.identity) || initial.hash != actual.hash {
				return empty, errors.New("optional recovery artifact changed from initial binding")
			}
			digests[name] = actual.hash
		}
		if err := decodeAuthenticatedRecovery(raw, manifest, manifestRaw, digests); err != nil {
			return empty, err
		}
	}
	for name := range s.inputs {
		if _, err := s.read(ctx, name); err != nil {
			return empty, err
		}
	}
	if err := s.trust.Revalidate(ctx); err != nil {
		return empty, err
	}
	return manifest, ctx.Err()
}

func decodeAuthenticatedManifest(raw []byte, target string) (releasecontract.Manifest, error) {
	var manifest releasecontract.Manifest
	if len(raw) == 0 || len(raw) > 1<<20 || jsoncontract.Decode(raw, &manifest) != nil {
		return manifest, errors.New("authenticated release manifest JSON contract differs")
	}
	if _, err := manifest.Validate(target); err != nil {
		return releasecontract.Manifest{}, errors.New("authenticated canonical release manifest differs")
	}
	return manifest, nil
}

func decodeAuthenticatedRecovery(raw []byte, manifest releasecontract.Manifest, manifestRaw []byte, optional map[string][32]byte) error {
	var record recoveryRecord
	if len(raw) == 0 || len(raw) > 1<<20 || jsoncontract.Decode(raw, &record) != nil {
		return errors.New("authenticated release recovery JSON contract differs")
	}
	if record.SchemaVersion == nil || *record.SchemaVersion != 1 || record.Tag != recoveryTag || record.SourceCommit != recoverySourceCommit || record.SourceRunID != recoverySourceRun || !runID.MatchString(record.ProducerRunID) || !sourceCommit.MatchString(record.ProducerCommit) || manifest.Release.Version != recoveryTag || manifest.Release.SourceCommit != recoverySourceCommit {
		return errors.New("release recovery is outside the exact interrupted source binding")
	}
	names := []string{"release-manifest.json", "runtime-image-evidence.json", "runtime-image-evidence.tar.gz", "astronomer-1.2.0.tgz"}
	if len(record.Files) != len(names) {
		return errors.New("release recovery exact file set differs")
	}
	for _, name := range names {
		if !contentSHA.MatchString(record.Files[name]) {
			return errors.New("release recovery content digest differs")
		}
	}
	sum := sha256.Sum256(manifestRaw)
	if record.Files["release-manifest.json"] != "sha256:"+hex.EncodeToString(sum[:]) {
		return errors.New("recovery does not bind the authenticated manifest")
	}
	for name, hash := range optional {
		if record.Files[name] != "sha256:"+hex.EncodeToString(hash[:]) {
			return errors.New("present recovery artifact differs from authenticated record")
		}
	}
	return nil
}

// bindMetadataArtifact hashes an optional private recovery artifact with an
// explicit byte budget and fixed32KiB memory. No signature/authorization is
// inferred from this digest; the caller compares it to authenticated metadata.
func bindMetadataArtifact(ctx context.Context, path string, maximum int64) (metadataInput, error) {
	var empty metadataInput
	if ctx == nil || !filepath.IsAbs(path) || maximum <= 0 {
		return empty, errors.New("explicit private artifact scope/context/budget required")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() > maximum || before.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return empty, errors.New("private recovery artifact identity or budget differs")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return empty, errors.New("open private recovery artifact")
	}
	file := os.NewFile(uintptr(fd), "private-recovery-artifact")
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !sameArtifact(before, opened) {
		return empty, errors.New("opened private recovery artifact differs")
	}
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		n, readErr := file.Read(buffer)
		if int64(n) > maximum-size {
			return empty, errors.New("recovery artifact budget exceeded")
		}
		size += int64(n)
		_, _ = hash.Write(buffer[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil || n == 0 {
			return empty, errors.New("read private recovery artifact")
		}
	}
	after, err := file.Stat()
	if err != nil || !sameArtifact(before, after) {
		return empty, errors.New("private recovery artifact changed while hashing")
	}
	named, err := os.Lstat(path)
	if err != nil || !sameArtifact(before, named) || size != before.Size() {
		return empty, errors.New("private recovery artifact name or size changed")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return metadataInput{before, digest}, nil
}
