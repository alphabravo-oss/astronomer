package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestDatabaseProjectionCompilationDataBindsActualAssignmentFields(t *testing.T) {
	// These are canonical compilation DATA, not physical proof or DB acceptance.
	expected := builtin.CatalogComponentExpectation{ProjectID: uuid.New(), TargetID: uuid.New()}
	expected.Source.Type = model.SourceHelmOCI
	expected.Source.URL = "oci://registry.invalid/scanner"
	expected.Source.Trust = model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa.pub"}
	expected.Version.ArtifactDigest = "sha256:" + strings.Repeat("a", 64)
	expected.Version.RequestedRevision = "1.2.3"
	expected.Version.ReconciliationPolicy = json.RawMessage(`{"interval":"5m","retry_interval":"30s","timeout":"10m","prune":true,"wait":true,"drift":"repair"}`)
	raw := json.RawMessage(`{"kind":"helm","helm":{"chart":"trivy-operator","chart_version":"1.2.3","release_name":"scanner","target_namespace":"scanner","values":{"image":{"repository":"registry.invalid/scanner","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"install_retries":3,"upgrade_retries":3}}`)
	effective, err := rollout.ResolveUnconfiguredDeployment(model.DigestBytes([]byte("locked bundle")), raw)
	if err != nil {
		t.Fatal(err)
	}
	deployment := uuid.New()
	assignment, err := databaseProjectionExpectedAssignment(expected, deployment, 4, effective)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Source.Kind != protocol.DeliverySourceHelmOCI || assignment.Source.Chart != "trivy-operator" || assignment.Scope != protocol.DeliveryScopePlatform || assignment.Renderer.Kind != protocol.DeliveryRendererHelm || assignment.Renderer.Helm == nil || assignment.Renderer.Helm.Chart != "trivy-operator" || assignment.Renderer.Helm.Version != "1.2.3" || assignment.Renderer.Helm.TargetNamespace != "scanner" || !bytes.Contains(assignment.Renderer.Helm.Values, []byte("registry.invalid/scanner")) {
		t.Fatal("expected assignment is not the canonical source and Helm projection")
	}
	for name, mutate := range map[string]func(*builtin.CatalogComponentExpectation, *rollout.UnconfiguredDeploymentIntent){
		"renderer": func(_ *builtin.CatalogComponentExpectation, e *rollout.UnconfiguredDeploymentIntent) {
			e.Renderer = json.RawMessage(`{"kind":"helm"}`)
		},
		"policy": func(e *builtin.CatalogComponentExpectation, _ *rollout.UnconfiguredDeploymentIntent) {
			e.Version.ReconciliationPolicy = json.RawMessage(`{}`)
		},
		"trust": func(e *builtin.CatalogComponentExpectation, _ *rollout.UnconfiguredDeploymentIntent) {
			e.Source.Trust = model.TrustPolicy{}
		},
		"source-scheme": func(e *builtin.CatalogComponentExpectation, _ *rollout.UnconfiguredDeploymentIntent) {
			e.Source.Type = model.SourceHelmHTTP
		},
	} {
		t.Run("invalid-"+name, func(t *testing.T) {
			changed, changedEffective := expected, effective
			mutate(&changed, &changedEffective)
			if _, err := databaseProjectionExpectedAssignment(changed, deployment, 4, changedEffective); err == nil {
				t.Fatal("invalid compilation input bypassed canonical validation")
			}
		})
	}
	response := protocol.DeliveryStateResponseV2{Assignments: []protocol.DeliveryAssignmentV2{assignment}}
	if !databaseProjectionAssignmentMatches(response, assignment) {
		t.Fatal("canonical matcher data rejected")
	}
	mutations := map[string]func(*protocol.DeliveryAssignmentV2){
		"deployment":           func(a *protocol.DeliveryAssignmentV2) { a.DeploymentID = uuid.NewString() },
		"project":              func(a *protocol.DeliveryAssignmentV2) { a.ProjectID = uuid.NewString() },
		"generation":           func(a *protocol.DeliveryAssignmentV2) { a.Generation++ },
		"effective-spec":       func(a *protocol.DeliveryAssignmentV2) { a.SpecDigest = "sha256:" + strings.Repeat("d", 64) },
		"configuration":        func(a *protocol.DeliveryAssignmentV2) { a.ConfigurationDigest = "sha256:" + strings.Repeat("e", 64) },
		"source":               func(a *protocol.DeliveryAssignmentV2) { a.Source.URL = "oci://caller.invalid/scanner" },
		"source-kind":          func(a *protocol.DeliveryAssignmentV2) { a.Source.Kind = protocol.DeliverySourceHelmHTTP },
		"source-chart":         func(a *protocol.DeliveryAssignmentV2) { a.Source.Chart = "different" },
		"chart-digest":         func(a *protocol.DeliveryAssignmentV2) { a.Source.Digest = "sha256:" + strings.Repeat("f", 64) },
		"chart-version":        func(a *protocol.DeliveryAssignmentV2) { a.Source.Revision = "1.2.4" },
		"source-verification":  func(a *protocol.DeliveryAssignmentV2) { a.Source.Verify = nil },
		"trust-reference":      func(a *protocol.DeliveryAssignmentV2) { a.Source.TrustSecret = "different" },
		"credential-reference": func(a *protocol.DeliveryAssignmentV2) { a.Source.CredentialSecret = "caller-secret" },
		"action":               func(a *protocol.DeliveryAssignmentV2) { a.Action = protocol.DeliveryActionSuspend },
		"platform-scope":       func(a *protocol.DeliveryAssignmentV2) { a.Scope = protocol.DeliveryScopeNamespace },
		"renderer-kind":        func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Kind = protocol.DeliveryRendererKustomize },
		"helm-chart":           func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.Chart = "different" },
		"helm-version":         func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.Version = "1.2.4" },
		"helm-namespace":       func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.TargetNamespace = "different" },
		"helm-values": func(a *protocol.DeliveryAssignmentV2) {
			a.Renderer.Helm.Values = json.RawMessage(`{"image":"changed"}`)
		},
		"helm-release": func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.ReleaseName = "different" },
		"helm-applier": func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.ServiceAccount = "different" },
		"helm-retries": func(a *protocol.DeliveryAssignmentV2) { a.Renderer.Helm.UpgradeRetries++ },
		"policy":       func(a *protocol.DeliveryAssignmentV2) { a.Policy.Timeout = "1s" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := assignment
			helm := *assignment.Renderer.Helm
			changed.Renderer.Helm = &helm
			mutate(&changed)
			if databaseProjectionAssignmentMatches(protocol.DeliveryStateResponseV2{Assignments: []protocol.DeliveryAssignmentV2{changed}}, assignment) {
				t.Fatal("changed assignment renewed database projection")
			}
		})
	}
	response.Assignments = append(response.Assignments, assignment)
	if databaseProjectionAssignmentMatches(response, assignment) {
		t.Fatal("duplicate target assignment accepted")
	}
	response.Assignments = nil
	if databaseProjectionAssignmentMatches(response, assignment) {
		t.Fatal("absent target assignment accepted")
	}
	withCredential := assignment
	withCredential.Credential = &protocol.DeliveryCredentialMaterial{Version: 1}
	if !databaseProjectionAssignmentMatches(protocol.DeliveryStateResponseV2{Assignments: []protocol.DeliveryAssignmentV2{withCredential}}, assignment) || withCredential.Credential == nil {
		t.Fatal("hydration bytes became witness input or matcher mutated response")
	}
}

func TestDatabaseProjectionRequiresConstructorOwnedPhysicalProof(t *testing.T) {
	if _, err := newDatabaseSnapshotProjection(context.Background(), nil); err == nil {
		t.Fatal("nil physical owner accepted")
	}
	var zero databaseSnapshotProjection
	if err := zero.revalidateOwnedSnapshot(context.Background()); err == nil {
		t.Fatal("zero participant granted current physical authority")
	}
	if _, err := NewWithDatabaseAuthority(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("incomplete provider constructor accepted")
	}
}

func TestDatabaseDurableBindingSeparatesTrustMirrorAndScannerIntent(t *testing.T) {
	var snapshot distribution.DatabaseAuthoritySnapshot
	snapshot.Base.Binding = distribution.DatabaseUpdateBinding{BaseRelease: "1.2.3", BaseDistributionDigest: "sha256:" + strings.Repeat("a", 64), CatalogDigest: "sha256:" + strings.Repeat("b", 64), Profile: "builtins", CandidateSourceCommit: strings.Repeat("c", 40), Scanner: distribution.DatabaseScannerCompatibility{ImageDigest: "sha256:" + strings.Repeat("d", 64), Version: "1.0.0", MainSchema: 2, JavaSchema: 1}}
	baseline := databaseSnapshotBindingFingerprint(snapshot)
	mutations := map[string]func(*distribution.DatabaseAuthoritySnapshot){
		"vendor": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.TrustMaterialFingerprint = "sha256:" + strings.Repeat("e", 64)
		},
		"vendor-material": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.VendorTrustFingerprint = "sha256:" + strings.Repeat("e", 64)
		},
		"vendor-origin": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.VendorTrustOriginFingerprint = "sha256:" + strings.Repeat("f", 64)
		},
		"operator-policy": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.MirrorPolicyFingerprint = "sha256:" + strings.Repeat("8", 64)
		},
		"operator": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.MirrorTrustFingerprint = "sha256:" + strings.Repeat("f", 64)
		},
		"operator-origin": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.MirrorTrustOriginFingerprint = "sha256:" + strings.Repeat("1", 64)
		},
		"mirror": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.Binding.MirrorEndorsementDigest = "sha256:" + strings.Repeat("2", 64)
		},
		"effective-distribution": func(s *distribution.DatabaseAuthoritySnapshot) { s.Base.DistributionIdentity = "changed" },
		"roots": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.RegistryRootsFingerprint = "sha256:" + strings.Repeat("3", 64)
		},
		"policy": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.RegistryPolicyFingerprint = "sha256:" + strings.Repeat("4", 64)
		},
		"root-origin": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.RegistryRootOriginFingerprint = "sha256:" + strings.Repeat("5", 64)
		},
		"scanner": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.Binding.Scanner.ImageDigest = "sha256:" + strings.Repeat("6", 64)
		},
		"enabled": func(s *distribution.DatabaseAuthoritySnapshot) { s.Base.ScanningEnabled = true },
		"source":  func(s *distribution.DatabaseAuthoritySnapshot) { s.Base.ChartSourceURL = "oci://changed.invalid/chart" },
		"chart": func(s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.ChartDigest = "sha256:" + strings.Repeat("7", 64)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			mutate(&changed)
			if databaseSnapshotBindingFingerprint(changed) == baseline {
				t.Fatal("changed authority field retained durable binding")
			}
		})
	}
}
