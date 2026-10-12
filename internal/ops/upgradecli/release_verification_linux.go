//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

const releaseRepository = "alphabravo-oss/astronomer"
const releaseSmallFilesMaximum = int64(16 << 10)

// ReleaseVerification composes actual signature-first chart verification, the
// legacy GitHub attestation command and six/seven canonical first-party image
// signatures. No arbitrary Manifest, checksum or bool can acknowledge Verify.
// It does not authorize a backup, replacement, Apply or complete post-upgrade
// workload observation. Serialize this handle with its single Driver invocation.
type ReleaseVerification struct {
	chart      *TargetChartContent
	commands   *Commands
	github     Tool
	inputs     map[string]metadataInput
	repository string
}

func NewReleaseVerification(ctx context.Context, chart *TargetChartContent, commands *Commands, github Tool) (*ReleaseVerification, error) {
	return NewReleaseVerificationWithRepository(ctx, chart, commands, github, releaseRepository)
}

var releaseRepositoryName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$`)

// The explicit legacy --repo selection affects only the GH repository check.
// The normal release Sigstore identity below remains fixed to releaseRepository.
func NewReleaseVerificationWithRepository(ctx context.Context, chart *TargetChartContent, commands *Commands, github Tool, repository string) (*ReleaseVerification, error) {
	if repository == "" {
		repository = releaseRepository
	}
	if !releaseRepositoryName.MatchString(repository) {
		return nil, errors.New("explicit GitHub repository selection invalid")
	}
	if ctx == nil || chart == nil || chart.metadata == nil || chart.client == nil || commands == nil || github.Kind != GitHubCLI {
		return nil, errors.New("explicit verified chart, joined commands and bound GitHub CLI required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := toolIdentity(github); err != nil {
		return nil, err
	}
	v := &ReleaseVerification{chart: chart, commands: commands, github: github, repository: repository, inputs: make(map[string]metadataInput)}
	for _, name := range []string{"RELEASE_IMAGES", "SHA256SUMS"} {
		input, err := bindMetadataArtifact(ctx, filepath.Join(chart.metadata.directory, name), releaseSmallFilesMaximum)
		if err != nil {
			return nil, err
		}
		v.inputs[name] = input
	}
	return v, ctx.Err()
}

func (v *ReleaseVerification) read(ctx context.Context, name string) ([]byte, error) {
	if ctx == nil || v == nil || v.chart == nil || v.chart.metadata == nil {
		return nil, errors.New("explicit release input context and ownership required")
	}
	initial, exists := v.inputs[name]
	if !exists {
		return nil, errors.New("release input is outside initial binding")
	}
	directory := v.chart.metadata.directory
	current, err := bindMetadataArtifact(ctx, filepath.Join(directory, name), releaseSmallFilesMaximum)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(initial.identity, current.identity) || initial.hash != current.hash {
		return nil, errors.New("initial release checksum/image input changed")
	}
	raw, err := readCapture(directory, name, releaseSmallFilesMaximum, "sha256:"+hex.EncodeToString(initial.hash[:]))
	if err != nil {
		return nil, err
	}
	return raw, ctx.Err()
}

// Verify repeats the actual concrete gates, not only a historical acknowledgment.
// GitHub uses the explicit operator environment from Commands; no ambient PATH,
// credential or proxy is imported by this adapter. The --repo contract retains
// the historical upgrade check; it does not claim an exact-workflow restriction
// that the historical command did not enforce. OCI signer identities remain
// separately fixed to the normal release workflow at the exact target tag.
func (v *ReleaseVerification) Verify(ctx context.Context) error {
	if ctx == nil || v == nil {
		return errors.New("explicit release verification context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, _, err := v.chart.Verify(ctx); err != nil {
		return err
	}
	manifest, err := v.chart.metadata.Verify(ctx)
	if err != nil {
		return err
	}
	refs, err := v.read(ctx, "RELEASE_IMAGES")
	if err != nil {
		return err
	}
	if err := exactFirstPartyReferences(refs, manifest); err != nil {
		return err
	}
	sums, err := v.read(ctx, "SHA256SUMS")
	if err != nil {
		return err
	}
	hashes := map[string][32]byte{"RELEASE_IMAGES": sha256.Sum256(refs), filepath.Base(v.chart.archivePath): v.chart.input.hash}
	for _, name := range []string{"release-manifest.json", "release-manifest.sigstore.json"} {
		raw, err := v.chart.metadata.read(ctx, name)
		if err != nil {
			return err
		}
		hashes[name] = sha256.Sum256(raw)
	}
	if err := exactReleaseChecksums(sums, hashes); err != nil {
		return err
	}
	// A joined successful real gh invocation verifies the archive against actual
	// GitHub attestations. A test seam alone is not native/provenance qualification.
	if _, _, err := v.commands.Execute(ctx, v.github, []string{"attestation", "verify", v.chart.archivePath, "--repo", v.repository}); err != nil {
		return err
	}
	if _, err := v.chart.readArchive(ctx); err != nil {
		return err
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Issuer: metadataIssuer, Identity: "https://github.com/" + releaseRepository + "/.github/workflows/release.yaml@refs/tags/" + v.chart.metadata.target}
	for _, image := range manifest.Astronomer.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		parts := strings.Split(image.Reference, "@")
		if len(parts) != 2 {
			return errors.New("signed image does not identify one immutable subject")
		}
		verification, err := resolver.VerifyPinnedOCI(ctx, v.chart.client, parts[0], parts[1], policy, v.chart.metadata.trust.FrozenDirectory())
		if err != nil {
			return err
		}
		if verification.Status != "verified" || verification.Provider != "cosign" || verification.Identity != policy.Identity {
			return errors.New("image signature was not acknowledged")
		}
	}
	// Rebind every input after actual external operations; changed private files
	// cannot inherit the earlier cryptographic acknowledgment. The underlying
	// verifier also rechecks the originally mounted and captured trust material.
	if _, _, err := v.chart.Verify(ctx); err != nil {
		return err
	}
	for _, name := range []string{"RELEASE_IMAGES", "SHA256SUMS"} {
		if _, err := v.read(ctx, name); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func exactFirstPartyReferences(raw []byte, manifest releasecontract.Manifest) error {
	if len(raw) == 0 || int64(len(raw)) > releaseSmallFilesMaximum || raw[len(raw)-1] != '\n' {
		return errors.New("release image list must be bounded complete lines")
	}
	rows := strings.Split(string(raw[:len(raw)-1]), "\n")
	if len(rows) != len(manifest.Astronomer.Images) || (len(rows) != 6 && len(rows) != 7) {
		return errors.New("release image list differs from signed first-party set")
	}
	wanted := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, image := range manifest.Astronomer.Images {
		repository := "astronomer-go-" + image.Name
		switch image.Name {
		case "frontend":
			repository = "astronomer-frontend"
		case "shell":
			repository = "astronomer-shell"
		case "dr":
			repository = "astronomer-dr"
		case "server", "worker", "agent", "migrate":
		default:
			return errors.New("signed image role is outside historical first-party contract")
		}
		prefix := "ghcr.io/alphabravo-oss/" + repository + "@"
		if !strings.HasPrefix(image.Reference, prefix) || !contentSHA.MatchString(strings.TrimPrefix(image.Reference, prefix)) || seen[image.Name] {
			return errors.New("signed image role/repository/digest is not exact")
		}
		seen[image.Name] = true
		wanted = append(wanted, image.Reference)
	}
	sort.Strings(wanted)
	sort.Strings(rows)
	for i, row := range rows {
		if row != wanted[i] {
			return errors.New("release image list does not match exact signed references")
		}
	}
	return nil
}

func exactReleaseChecksums(raw []byte, wanted map[string][32]byte) error {
	if len(raw) == 0 || int64(len(raw)) > releaseSmallFilesMaximum || raw[len(raw)-1] != '\n' || strings.Count(string(raw), "\n") > 100 {
		return errors.New("release checksum file exceeds bounded complete-line contract")
	}
	found := make(map[string]string)
	for _, row := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		// sha256sum uses two spaces; a binary marker is accepted by the historical
		// awk contract too. No filename can contain whitespace/path traversal.
		if len(row) < 67 || row[64] != ' ' || (row[65] != ' ' && row[65] != '*') {
			return errors.New("release checksum line contract differs")
		}
		hash, name := row[:64], row[66:]
		if !contentSHA.MatchString("sha256:"+hash) || name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "\x00\t\r\n ") || name == "." || name == ".." {
			return errors.New("release checksum filename/digest differs")
		}
		if _, ok := found[name]; ok {
			return errors.New("release checksum filename duplicated")
		}
		found[name] = hash
	}
	for name, expected := range wanted {
		if found[name] != fmt.Sprintf("%x", expected) {
			return errors.New("required release asset checksum differs")
		}
	}
	return nil
}
