//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

func releaseImageFixture(includeDR bool) (releasecontract.Manifest, []byte) {
	var manifest releasecontract.Manifest
	var rows []string
	for _, name := range []string{"agent", "frontend", "migrate", "server", "shell", "worker", "dr"} {
		if name == "dr" && !includeDR {
			continue
		}
		repository := "astronomer-go-" + name
		if name == "frontend" || name == "shell" || name == "dr" {
			repository = "astronomer-" + name
		}
		reference := "ghcr.io/alphabravo-oss/" + repository + "@sha256:" + strings.Repeat("a", 64)
		manifest.Astronomer.Images = append(manifest.Astronomer.Images, releasecontract.Artifact{Name: name, Reference: reference})
		rows = append(rows, reference)
	}
	return manifest, []byte(strings.Join(rows, "\n") + "\n")
}

func TestReleaseFirstPartyListRetainsExactSixAndSevenSubjects(t *testing.T) {
	for _, dr := range []bool{false, true} {
		manifest, raw := releaseImageFixture(dr)
		if err := exactFirstPartyReferences(raw, manifest); err != nil {
			t.Fatal(err)
		}
		for _, bad := range [][]byte{append(append([]byte(nil), raw...), []byte("\n")...), []byte(strings.TrimSuffix(string(raw), "\n")), []byte(strings.Replace(string(raw), "@sha256:", ":latest#", 1)), []byte(strings.Replace(string(raw), "ghcr.io/alphabravo-oss/", "foreign.invalid/", 1))} {
			if exactFirstPartyReferences(bad, manifest) == nil {
				t.Fatal("unbound first-party list admitted")
			}
		}
		manifest.Astronomer.Images[0].Reference = strings.Replace(manifest.Astronomer.Images[0].Reference, "astronomer-go-agent", "astronomer-go-worker", 1)
		if exactFirstPartyReferences(raw, manifest) == nil {
			t.Fatal("role/repository mismatch admitted")
		}
	}
}

func TestReleaseChecksumInventoryRequiresEveryOriginalAssetAndRejectsAmbiguity(t *testing.T) {
	wanted := map[string][32]byte{"RELEASE_IMAGES": sha256.Sum256([]byte("images")), "astronomer-1.2.0.tgz": sha256.Sum256([]byte("chart")), "release-manifest.json": sha256.Sum256([]byte("manifest")), "release-manifest.sigstore.json": sha256.Sum256([]byte("bundle"))}
	var raw strings.Builder
	for name, hash := range wanted {
		fmt.Fprintf(&raw, "%x  %s\n", hash, name)
	}
	if err := exactReleaseChecksums([]byte(raw.String()), wanted); err != nil {
		t.Fatal(err)
	}
	if err := exactReleaseChecksums([]byte(raw.String()+strings.Repeat("b", 64)+"  extra-file\n"), wanted); err != nil {
		t.Fatal("legacy unrelated checksum row rejected", err)
	}
	for _, bad := range []string{raw.String() + strings.Repeat("a", 64) + "  RELEASE_IMAGES\n", strings.Replace(raw.String(), "release-manifest.json", "../release-manifest.json", 1), strings.Replace(raw.String(), "RELEASE_IMAGES", "missing", 1), strings.Replace(raw.String(), "  ", " \t", 1), strings.TrimSuffix(raw.String(), "\n")} {
		if exactReleaseChecksums([]byte(bad), wanted) == nil {
			t.Fatal("incomplete/ambiguous checksum inventory admitted")
		}
	}
}

func TestReleaseVerificationRebindsPrivateListsWithoutCryptographicShortcut(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "RELEASE_IMAGES")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, err := bindMetadataArtifact(t.Context(), path, releaseSmallFilesMaximum)
	if err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVerification{chart: &TargetChartContent{metadata: &SignedReleaseMetadata{directory: directory}}, inputs: map[string]metadataInput{"RELEASE_IMAGES": bound}}
	if raw, err := v.read(t.Context(), "RELEASE_IMAGES"); err != nil || string(raw) != "original\n" {
		t.Fatal("private original rejected", err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.read(t.Context(), "RELEASE_IMAGES"); err == nil {
		t.Fatal("changed list retained initial authority")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.read(ctx, "RELEASE_IMAGES"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	if New := func() error { _, e := NewReleaseVerification(t.Context(), nil, nil, Tool{}); return e }(); New == nil {
		t.Fatal("unbound verification admitted")
	}
}

func TestGitHubToolVersionRequiresExactOfficialTwoLineIdentity(t *testing.T) {
	for _, raw := range []string{"gh version 2.80.0 (2025-09-23)\nhttps://github.com/cli/cli/releases/tag/v2.80.0\n", "gh version 2.80.0\nhttps://github.com/cli/cli/releases/tag/v2.80.0\n"} {
		got, err := githubToolVersion(raw)
		if err != nil || got != "2.80.0" {
			t.Fatal("official output contract rejected", err)
		}
	}
	for _, raw := range []string{"gh version 2.80.0\n", "gh version 2.80.0\nhttps://github.com/cli/cli/releases/tag/v2.80.1\n", "gh version 2.80.0\nhttps://foreign.invalid/cli/cli/releases/tag/v2.80.0\n", "gh version 2.80.0\nhttps://github.com/cli/cli/releases/tag/v2.80.0\nextra"} {
		if _, err := githubToolVersion(raw); err == nil {
			t.Fatal("ambiguous/foreign version output admitted")
		}
	}
}

func TestGitHubToolAdmissionUsesJoinedActualVersionAndExactBoundBytes(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = GitHubCLI
	tool.Version = "v2.80.0"
	called := 0
	raw := []byte("gh version 2.80.1\nhttps://github.com/cli/cli/releases/tag/v2.80.1\n")
	commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
		called++
		if len(spec.Args) != 1 || spec.Args[0] != "version" {
			t.Fatal("actual version command differs")
		}
		return captureFixture(t, spec, raw), nil
	}
	if _, _, err := commands.Execute(t.Context(), tool, []string{"attestation", "verify", "/private/chart.tgz", "--repo", releaseRepository}); err == nil || called != 0 {
		t.Fatal("unverified CLI admitted")
	}
	if commands.VerifyTool(t.Context(), tool) == nil {
		t.Fatal("wrong actual CLI version admitted")
	}
	raw = []byte("gh version 2.80.0\nhttps://github.com/cli/cli/releases/tag/v2.80.0\n")
	if err := commands.VerifyTool(t.Context(), tool); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatal("unexpected preflight execution count")
	}
	if err := os.WriteFile(tool.Path, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := commands.Execute(t.Context(), tool, []string{"attestation", "verify"}); err == nil || called != 2 {
		t.Fatal("changed bound CLI executed")
	}
}

// Fixtures above prove parser/file/process admission contracts only. No returned
// fake capture acknowledges real GitHub provenance, OCI signatures or Driver Verify.
