//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// This isolates the concrete acknowledged CLI lifecycle after trusted stages.
// Synthetic manifest/tool inputs here do not qualify signature verification or
// the constructor's canonical manifest loading; public wiring remains pending.
func helmReleaseFixture(t *testing.T) *HelmRelease {
	t.Helper()
	fence, _ := fenceFixture(t)
	tool := fence.kubectl
	tool.Kind, tool.Version = Helm, "v4.3.0"
	fence.commands.verified[tool] = struct{}{}
	plan := upgrade.Plan{Target: "v1.2.2", Previous: "v1.2.1", Mode: upgrade.Execute, Identity: fence.options.Identity, Timeout: time.Minute, CleanupTimeout: time.Second}
	path := filepath.Join(fence.commands.options.Directory, "release-manifest")
	if err := os.WriteFile(path, []byte("synthetic manifest bytes; no signature proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, hash, err := privateBoundInput(path)
	if err != nil {
		t.Fatal(err)
	}
	input := upgrade.HelmInputs{Tool: upgrade.HelmToolBinding{Path: tool.Path, Version: tool.Version, SHA256: tool.SHA256}, Plan: plan, ChartReference: "oci://ghcr.io/alphabravo-oss/charts/astronomer", ManifestFile: path, RuntimeSources: map[string]string{}}
	input.Manifest.Release.Version = plan.Target
	for _, row := range []struct{ name, repo string }{{"server", "astronomer-go-server"}, {"worker", "astronomer-go-worker"}, {"agent", "astronomer-go-agent"}, {"migrate", "astronomer-go-migrate"}, {"frontend", "astronomer-frontend"}, {"shell", "astronomer-shell"}, {"dr", "astronomer-dr"}} {
		input.Manifest.Astronomer.Images = append(input.Manifest.Astronomer.Images, releasecontract.Artifact{Name: row.name, Kind: "container_image", Reference: "ghcr.io/alphabravo-oss/" + row.repo + "@sha256:" + strings.Repeat("a", 64)})
	}
	for _, role := range []string{"utilities.busybox", "postgres", "redis", "dex", "managementRestoreDrill.sidecar", "managementLogging"} {
		source := "fixture.invalid/" + strings.ToLower(strings.ReplaceAll(role, ".", "-")) + ":exact"
		input.RuntimeSources[role] = source
		input.Manifest.Astronomer.RuntimeImages = append(input.Manifest.Astronomer.RuntimeImages, releasecontract.RuntimeImage{SourceReference: source, Reference: strings.TrimSuffix(source, ":exact") + "@sha256:" + strings.Repeat("b", 64)})
	}
	arguments := make(map[upgrade.Stage][]string, 3)
	arguments[upgrade.DryRun], err = input.DryRunArguments()
	if err != nil {
		t.Fatal(err)
	}
	arguments[upgrade.Apply], err = input.ApplyArguments()
	if err != nil {
		t.Fatal(err)
	}
	arguments[upgrade.Rollback], err = input.RollbackArguments(fence.options.Revision)
	if err != nil {
		t.Fatal(err)
	}
	fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		return captureFixture(t, spec, []byte("private synthetic CLI acknowledgment")), nil
	}
	return &HelmRelease{fence: fence, tool: tool, plan: plan, manifestFile: path, manifestIdentity: identity, manifestHash: hash, arguments: arguments, execute: fence.commands.Execute}
}

func TestHelmLifecycleCallsOwnedCommandsWithInitiallyBoundInputs(t *testing.T) {
	release := helmReleaseFixture(t)
	calls := 0
	release.fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		if len(spec.Args) < 5 || spec.Args[0] != "--kubeconfig" || spec.Args[1] != release.fence.options.Kubeconfig || spec.Args[2] != "--kube-context" || spec.Args[3] != release.plan.Identity.Context {
			t.Fatal("owned explicit input/context was lost")
		}
		return captureFixture(t, spec, []byte("private acknowledgment")), nil
	}
	for _, stage := range []upgrade.Stage{upgrade.DryRun, upgrade.Apply, upgrade.Rollback} {
		outcome, err := release.RunStage(context.Background(), stage)
		if err != nil || !outcome.Known || outcome.Applied != (stage == upgrade.Apply) {
			t.Fatal("joined stage acknowledgment differs")
		}
	}
	if calls != 3 {
		t.Fatal("a lifecycle command was missing or replayed")
	}
	if _, err := release.RunStage(context.Background(), upgrade.ReplaceDatabase); err == nil || calls != 3 {
		t.Fatal("Helm lifecycle granted unrelated database mutation")
	}
}

func TestHelmLifecyclePreservesUnknownApplyFailureAndAcknowledgedOwnerLoss(t *testing.T) {
	release := helmReleaseFixture(t)
	childError := &process.Error{Result: process.Result{Kind: process.ChildFailed, ExitCode: 7, ChildExitCode: 7, Started: true, LeaderReaped: true}}
	release.fence.commands.run = func(_ context.Context, _ process.Spec) (process.Result, error) { return childError.Result, childError }
	outcome, err := release.RunStage(context.Background(), upgrade.Apply)
	if !errors.Is(err, childError) || outcome.Known || outcome.Applied {
		t.Fatal("failed CLI atomic rollback was treated as a known non-mutation")
	}
	release = helmReleaseFixture(t)
	release.fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		result := captureFixture(t, spec, []byte("private applied acknowledgment"))
		release.fence.execute = func(context.Context, Tool, []string) ([]byte, error) {
			return nil, errors.New("owned namespace was recreated")
		}
		return result, nil
	}
	outcome, err = release.RunStage(context.Background(), upgrade.Apply)
	if err == nil || !outcome.Known || !outcome.Applied {
		t.Fatal("acknowledged mutation was lost when the post-command owner check failed")
	}
}

func TestHelmLifecycleRejectsInputChangesBeforeAnyStageCommand(t *testing.T) {
	for _, change := range []string{"manifest", "kubeconfig"} {
		t.Run(change, func(t *testing.T) {
			release := helmReleaseFixture(t)
			path := release.manifestFile
			if change == "kubeconfig" {
				path = release.fence.options.Kubeconfig
			}
			if err := os.WriteFile(path, []byte("changed private input"), 0o600); err != nil {
				t.Fatal(err)
			}
			release.fence.commands.run = func(context.Context, process.Spec) (process.Result, error) {
				t.Fatal("changed input reached Helm")
				return process.Result{}, nil
			}
			outcome, err := release.RunStage(context.Background(), upgrade.Apply)
			if err == nil || !outcome.Known || outcome.Applied {
				t.Fatal("changed input admitted or falsely reported mutation")
			}
		})
	}
}

func TestHelmLifecycleConstructorRejectsUnverifiedManifestBytes(t *testing.T) {
	release := helmReleaseFixture(t)
	options := HelmReleaseOptions{Plan: release.plan, ManifestFile: release.manifestFile, ManifestSHA256: "sha256:" + strings.Repeat("0", 64)}
	if _, err := NewHelmRelease(context.Background(), release.fence, release.tool, options); err == nil {
		t.Fatal("unbound manifest bytes admitted")
	}
	if _, err := NewHelmRelease(nil, release.fence, release.tool, options); err == nil {
		t.Fatal("nil construction context admitted")
	}
	// Matching bytes are insufficient: the canonical closed contract must load.
	hash := sha256.Sum256([]byte("synthetic manifest bytes; no signature proof"))
	options.ManifestSHA256 = "sha256:" + hex.EncodeToString(hash[:])
	if _, err := NewHelmRelease(context.Background(), release.fence, release.tool, options); err == nil {
		t.Fatal("matching digest silently bypassed canonical manifest validation")
	}
}

func TestHelmLifecycleConstructorRejectsRelativeManifestBeforeChildren(t *testing.T) {
	release := helmReleaseFixture(t)
	calls := 0
	release.fence.commands.run = func(context.Context, process.Spec) (process.Result, error) {
		calls++
		return process.Result{}, errors.New("unexpected command")
	}
	release.fence.execute = func(context.Context, Tool, []string) ([]byte, error) {
		calls++
		return nil, errors.New("unexpected ownership command")
	}
	options := HelmReleaseOptions{Plan: release.plan, ManifestFile: filepath.Base(release.manifestFile), ManifestSHA256: "sha256:" + hex.EncodeToString(release.manifestHash[:])}
	if _, err := NewHelmRelease(context.Background(), release.fence, release.tool, options); err == nil || calls != 0 {
		t.Fatal("relative manifest admitted or reached a child command")
	}
}

func TestHelmLifecycleConstructorLoadsCanonicalManifestAndCopiesDisposableLease(t *testing.T) {
	fence, originalLease := fenceFixture(t)
	tool := fence.kubectl
	tool.Kind, tool.Version = Helm, "v4.3.0"
	fence.commands.verified[tool] = struct{}{}
	// The observation fixture owns its original lease; the caller receives a copy.
	callerLease := *originalLease
	expectedLease := callerLease
	plan := upgrade.Plan{Target: "v1.2.2", Previous: "v1.2.1", Mode: upgrade.Execute, Identity: fence.options.Identity, Timeout: time.Minute, CleanupTimeout: time.Second, Replace: &callerLease}
	manifest, sources := canonicalHelmManifestFixture(plan.Target)
	if _, err := manifest.Validate(plan.Target); err != nil {
		t.Fatal("invalid canonical constructor fixture:", err)
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fence.commands.options.Directory, "canonical-release-manifest")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	release, err := NewHelmRelease(context.Background(), fence, tool, HelmReleaseOptions{Plan: plan, ManifestFile: path, ManifestSHA256: "sha256:" + hex.EncodeToString(hash[:]), ChartReference: "oci://ghcr.io/alphabravo-oss/charts/astronomer", RuntimeSources: sources})
	if err != nil {
		t.Fatal(err)
	}
	callerLease.Owner, callerLease.DatabasePodUID = "changed-owner", "changed-pod"
	callerLease.DatabaseName, callerLease.RestoreDatabaseName = "changed_live", "changed_restore"
	callerLease.NamespaceUID = "changed-namespace"
	if release.plan.Replace == &callerLease || *release.plan.Replace != expectedLease {
		t.Fatal("constructor retained caller-owned mutable lease")
	}
	calls := 0
	fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		return captureFixture(t, spec, []byte("private synthetic acknowledgment")), nil
	}
	outcome, err := release.RunStage(context.Background(), upgrade.DryRun)
	if err != nil || !outcome.Known || outcome.Applied || calls != 1 {
		t.Fatal("canonical bound lifecycle changed after caller lease mutation")
	}
}

// This is a shape-valid synthetic contract, not signed artifacts or a cluster
// qualification. The constructor loads these exact private bytes itself.
func canonicalHelmManifestFixture(version string) (releasecontract.Manifest, map[string]string) {
	var manifest releasecontract.Manifest
	hash := "sha256:" + strings.Repeat("a", 64)
	artifact := func(name, repository, kind string) releasecontract.Artifact {
		reference := repository + "@" + hash
		return releasecontract.Artifact{Name: name, Kind: kind, Reference: reference, ContentDigest: hash, Evidence: releasecontract.Evidence{Signature: "cosign://" + reference, SBOM: "cosign-attestation://" + reference + "#spdxjson", Provenance: "cosign-attestation://" + reference + "#slsaprovenance"}}
	}
	manifest.SchemaVersion = 1
	manifest.Release.Version, manifest.Release.InstallMode = version, "fresh_and_upgrade"
	manifest.Release.SigningPolicy.CertificateOIDCIssuer = "https://token.actions.githubusercontent.com"
	manifest.Release.SigningPolicy.CertificateIdentity = "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + version
	manifest.Compatibility.Kubernetes.MinimumMinor, manifest.Compatibility.Kubernetes.MaximumMinor = "1.36", "1.36"
	manifest.Compatibility.AgentProtocol.Name = "astronomer.delivery"
	manifest.Compatibility.AgentProtocol.Minimum, manifest.Compatibility.AgentProtocol.Maximum = protocol.MinimumDeliveryProtocolVersion, protocol.MaximumDeliveryProtocolVersion
	manifest.Compatibility.AgentProtocol.RequiredCapabilities = protocol.RequiredConnectCapabilities()
	manifest.Compatibility.Agent.MinimumCompatibleVersion = protocol.MinimumCompatibleAgentVersion
	manifest.Compatibility.Agent.MinimumSupportedVersion = protocol.MinimumSupportedAgentVersion
	manifest.Compatibility.Agent.MaximumSupportedVersionExclusive = protocol.MaximumSupportedAgentVersionExclusive
	manifest.Compatibility.Agent.TunnelProtocolMinimum, manifest.Compatibility.Agent.TunnelProtocolMaximum = protocol.MinimumTunnelProtocolVersion, protocol.MaximumTunnelProtocolVersion
	manifest.Compatibility.Agent.HeartbeatSchemaMinimum, manifest.Compatibility.Agent.HeartbeatSchemaMaximum = protocol.MinimumHeartbeatSchemaVersion, protocol.MaximumHeartbeatSchemaVersion
	manifest.Compatibility.PostgreSQL.SupportedMajors = []int{17, 18}
	manifest.Compatibility.PostgreSQL.MinimumUpgradeSchema, manifest.Compatibility.PostgreSQL.TargetSchema, manifest.Compatibility.PostgreSQL.ReversibleThroughSchema = 66, 75, 66
	manifest.Flux.Version, manifest.Flux.APIs = "v2.9.6", []string{"fixture-source", "fixture-kustomize", "fixture-helm"}
	manifest.BuiltInBundles.CatalogDigest = hash
	manifest.Charlie.CapabilityDisclosureDigest, manifest.Charlie.QualifiedVersion = hash, "v1.0.63"
	manifest.Charlie.SigningPolicy.CertificateOIDCIssuer = "https://token.actions.githubusercontent.com"
	manifest.Charlie.SigningPolicy.CertificateIdentity = "https://github.com/alphabravo-oss/charlie/.github/workflows/release.yml@refs/tags/v1.0.63"
	manifest.BuiltInBundles.Components = append(manifest.BuiltInBundles.Components, struct {
		Slug         string   `json:"slug"`
		Chart        string   `json:"chart"`
		ChartVersion string   `json:"chart_version"`
		ChartDigest  string   `json:"chart_digest"`
		Images       []string `json:"images"`
	}{Slug: "synthetic-built-in", Chart: "fixture-chart", ChartVersion: "1.0.0", ChartDigest: hash})
	for _, row := range []struct{ name, repo string }{{"server", "astronomer-go-server"}, {"worker", "astronomer-go-worker"}, {"agent", "astronomer-go-agent"}, {"migrate", "astronomer-go-migrate"}, {"frontend", "astronomer-frontend"}, {"shell", "astronomer-shell"}, {"dr", "astronomer-dr"}} {
		manifest.Astronomer.Images = append(manifest.Astronomer.Images, artifact(row.name, "ghcr.io/alphabravo-oss/"+row.repo, "container_image"))
	}
	sources := map[string]string{}
	for _, role := range []string{"utilities.busybox", "postgres", "redis", "dex", "managementRestoreDrill.sidecar", "managementLogging"} {
		repository := "fixture.invalid/" + strings.ToLower(strings.ReplaceAll(role, ".", "-"))
		source := repository + ":exact"
		sources[role] = source
		manifest.Astronomer.RuntimeImages = append(manifest.Astronomer.RuntimeImages, releasecontract.RuntimeImage{SourceReference: source, Reference: repository + "@" + hash})
	}
	manifest.Astronomer.Chart = artifact("chart", "ghcr.io/alphabravo-oss/charts/astronomer", "helm_chart")
	manifest.Flux.Distribution = artifact("flux", "fixture.invalid/flux", "oci_artifact")
	manifest.BuiltInBundles.Artifact = artifact("bundles", "fixture.invalid/bundles", "oci_artifact")
	manifest.Charlie.Artifact = artifact("charlie", "fixture.invalid/charlie", "oci_artifact")
	for _, name := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
		manifest.Flux.Controllers = append(manifest.Flux.Controllers, artifact(name, "fixture.invalid/"+name, "container_image"))
	}
	return manifest, sources
}
