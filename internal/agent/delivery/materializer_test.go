package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	testDeploymentID = "11111111-1111-4111-8111-111111111111"
	testTargetID     = "22222222-2222-4222-8222-222222222222"
	testProjectID    = "33333333-3333-4333-8333-333333333333"
	testDigest       = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testArtifact     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testCapabilities() Capabilities {
	return Capabilities{
		SourceKinds: []protocol.DeliverySourceKind{
			protocol.DeliverySourceGit, protocol.DeliverySourceOCIArtifact,
			protocol.DeliverySourceHelmHTTP, protocol.DeliverySourceHelmOCI,
		},
		RendererKinds:          []protocol.DeliveryRendererKind{protocol.DeliveryRendererKustomize, protocol.DeliveryRendererHelm},
		FluxAPIVersions:        []string{"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"},
		NamespaceScope:         true,
		PlatformScope:          true,
		NoCrossNamespaceRefs:   true,
		NoRemoteKustomizeBases: true,
	}
}

func gitAssignment() protocol.DeliveryAssignmentV2 {
	names := Names(testProjectID, testDeploymentID)
	return protocol.DeliveryAssignmentV2{
		DeploymentID: testDeploymentID,
		TargetID:     testTargetID,
		ProjectID:    testProjectID,
		Generation:   7,
		SpecDigest:   testDigest,
		Action:       protocol.DeliveryActionApply,
		Scope:        protocol.DeliveryScopeNamespace,
		Source: protocol.DeliverySourceV2{
			Kind:             protocol.DeliverySourceGit,
			URL:              "ssh://git@example.test/platform/apps.git",
			Revision:         "0123456789abcdef0123456789abcdef01234567",
			Path:             "clusters/base",
			CredentialSecret: names.AuthSecret,
			TrustSecret:      names.TrustSecret,
			Verify:           &protocol.DeliveryVerifyV2{Provider: "pgp", Mode: "HEAD"},
		},
		Renderer: protocol.DeliveryRendererV2{Kind: protocol.DeliveryRendererKustomize, Kustomize: &protocol.DeliveryKustomizeRenderer{
			TargetNamespace: "workload", ServiceAccount: names.Applier, Prune: true, Wait: true,
			Patches:       []string{"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\nspec:\n  replicas: 2\n"},
			Substitutions: map[string]string{"ENVIRONMENT": "production"},
			HealthChecks:  []protocol.DeliveryHealthCheck{{APIVersion: "apps/v1", Kind: "Deployment", Name: "app", Namespace: "workload"}},
		}},
		Policy: protocol.DeliveryReconciliationPolicy{Interval: "10m", RetryInterval: "1m", Timeout: "10m", Drift: "enabled", Prune: true},
		Credential: &protocol.DeliveryCredentialMaterial{Version: 3, Data: map[string][]byte{
			"identity":      []byte("PRIVATE"),
			"known_hosts":   []byte("example.test ssh-ed25519 AAAA"),
			"trust.pgp.asc": []byte("PUBLIC"),
		}},
	}
}

func ociAssignment() protocol.DeliveryAssignmentV2 {
	assignment := gitAssignment()
	assignment.Source = protocol.DeliverySourceV2{
		Kind: protocol.DeliverySourceOCIArtifact, URL: "oci://registry.example.test/platform/apps",
		Revision: testArtifact, Digest: testArtifact, Path: "base",
		Verify: &protocol.DeliveryVerifyV2{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{Issuer: "https://issuer.example", Subject: "repo:platform/apps:ref:refs/heads/main"}}},
	}
	assignment.Credential = nil
	return assignment
}

func helmHTTPAssignment() protocol.DeliveryAssignmentV2 {
	names := Names(testProjectID, testDeploymentID)
	return protocol.DeliveryAssignmentV2{
		DeploymentID: testDeploymentID, TargetID: testTargetID, ProjectID: testProjectID,
		Generation: 7, SpecDigest: testDigest, Action: protocol.DeliveryActionApply, Scope: protocol.DeliveryScopeNamespace,
		Source: protocol.DeliverySourceV2{
			Kind: protocol.DeliverySourceHelmHTTP, URL: "https://charts.example.test", Revision: "4.12.1",
			Digest: testArtifact, Chart: "ingress-nginx", CredentialSecret: names.AuthSecret,
		},
		Renderer: protocol.DeliveryRendererV2{Kind: protocol.DeliveryRendererHelm, Helm: &protocol.DeliveryHelmRenderer{
			Chart: "ingress-nginx", Version: "4.12.1", ReleaseName: "ingress-nginx", TargetNamespace: "workload", ServiceAccount: names.Applier,
			Values: json.RawMessage(`{"controller":{"replicas":2}}`), InstallRetries: 3, UpgradeRetries: 2,
			UpgradeRemediation: "rollback", EnableTests: true, DriftMode: "enabled",
		}},
		Policy:     protocol.DeliveryReconciliationPolicy{Interval: "10m", RetryInterval: "1m", Timeout: "10m", Drift: "enabled", Prune: true},
		Credential: &protocol.DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{"username": []byte("robot"), "password": []byte("secret")}},
	}
}

func helmOCIAssignment() protocol.DeliveryAssignmentV2 {
	assignment := helmHTTPAssignment()
	names := Names(testProjectID, testDeploymentID)
	assignment.Scope = protocol.DeliveryScopePlatform
	assignment.Source = protocol.DeliverySourceV2{
		Kind: protocol.DeliverySourceHelmOCI, URL: "oci://registry.example.test/charts", Revision: "4.12.1",
		Digest: testArtifact, Chart: "ingress-nginx", TrustSecret: names.TrustSecret,
		Verify: &protocol.DeliveryVerifyV2{Provider: "notation"},
	}
	assignment.Credential = &protocol.DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{
		"trust.notation.policy.json": []byte(`{"version":"1.0"}`),
		"trust.notation.ca.pem":      []byte("CERTIFICATE"),
	}}
	return assignment
}

func TestBuildAssignmentGoldenVariants(t *testing.T) {
	t.Parallel()
	// Hashes cover the complete canonical Kubernetes object arrays. Focused
	// assertions below keep any deliberate golden update reviewable.
	tests := []struct {
		name       string
		assignment protocol.DeliveryAssignmentV2
		wantSource string
		wantRender string
		wantCount  int
		wantHash   string
	}{
		{name: "git-kustomize-auth-trust", assignment: gitAssignment(), wantSource: "GitRepository", wantRender: "Kustomization", wantCount: 8, wantHash: "8733bbfb0fe1f50c6bd05c29f0366b7b31f4eeaaf5b2f1772ea46fa6a4a8e09a"},
		{name: "oci-kustomize-keyless", assignment: ociAssignment(), wantSource: "OCIRepository", wantRender: "Kustomization", wantCount: 6, wantHash: "21072323c036cb95d15d2891ca5f73c65a15bbad5a5c573d1bf087a2d5992ab3"},
		{name: "helm-http-auth", assignment: helmHTTPAssignment(), wantSource: "HelmRepository", wantRender: "HelmRelease", wantCount: 7, wantHash: "b9bce4170c7b7a6994740c324d65c07d70c19dd11d0f157dbe4517cbcb49527f"},
		{name: "helm-oci-platform-trust", assignment: helmOCIAssignment(), wantSource: "OCIRepository", wantRender: "HelmRelease", wantCount: 6, wantHash: "4f0d4075c6f92289c516cf01a6c2e13c86eb75feb33cb419f8596fc8cdf8c9eb"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			materialization, err := BuildAssignment(test.assignment, testCapabilities(), ValidationPolicy{AllowPlatformScope: true})
			if err != nil {
				t.Fatalf("BuildAssignment() error = %v", err)
			}
			if len(materialization.Objects) != test.wantCount {
				t.Fatalf("object count = %d, want %d", len(materialization.Objects), test.wantCount)
			}
			if got := materialization.Objects[len(materialization.Objects)-2].GetKind(); got != test.wantSource {
				t.Errorf("source kind = %q, want %q", got, test.wantSource)
			}
			if got := materialization.Objects[len(materialization.Objects)-1].GetKind(); got != test.wantRender {
				t.Errorf("renderer kind = %q, want %q", got, test.wantRender)
			}
			encoded, err := json.Marshal(materialization.Objects)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(encoded)
			gotHash := hex.EncodeToString(sum[:])
			if gotHash != test.wantHash {
				t.Errorf("complete object golden hash = %s, want %s", gotHash, test.wantHash)
			}
			assertObjectBoundary(t, test.assignment, materialization)
		})
	}
}

func assertObjectBoundary(t *testing.T, assignment protocol.DeliveryAssignmentV2, materialization Materialization) {
	t.Helper()
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	if materialization.ControlNamespace != "astronomer-delivery-p-333333333333" || names.Base != "d-11111111111141118111111111111111" {
		t.Fatalf("unexpected deterministic names: %#v", names)
	}
	for index, object := range materialization.Objects {
		if index == 0 {
			if object.GetKind() != "Namespace" || object.GetLabels()[DeploymentIDLabel] != "" {
				t.Errorf("shared control namespace has deployment ownership: %#v", object.GetLabels())
			}
			continue
		}
		labels, annotations := object.GetLabels(), object.GetAnnotations()
		if labels[ManagedByLabel] != ManagedByValue || labels[DeploymentIDLabel] != assignment.DeploymentID || labels[ProjectIDHashLabel] != "333333333333" {
			t.Errorf("object %s has invalid labels: %#v", Identity(object), labels)
		}
		if annotations[SpecDigestAnnotation] != assignment.SpecDigest || annotations[GenerationAnnotation] != "7" {
			t.Errorf("object %s has invalid annotations: %#v", Identity(object), annotations)
		}
	}
}

func TestMaterializerExactSecurityShapes(t *testing.T) {
	t.Parallel()
	git, err := BuildAssignment(gitAssignment(), testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	names := Names(testProjectID, testDeploymentID)
	if got, _, _ := unstructured.NestedString(git.Objects[1].Object, "data", "identity"); got != "" {
		t.Fatal("Secret byte data unexpectedly exposed as a string")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(git.Objects[2].Object, "data", "pgp.asc"); !found {
		t.Fatal("trust wire key was not transformed to the Flux trust key")
	}
	role := git.Objects[4]
	encodedRole, _ := json.Marshal(role.Object)
	if strings.Contains(string(encodedRole), "clusterroles") || strings.Contains(string(encodedRole), "serviceaccounts") || strings.Contains(string(encodedRole), `"apiGroups":["*"]`) {
		t.Fatalf("namespace role contains escalation capability: %s", encodedRole)
	}
	kustomization := git.Objects[len(git.Objects)-1]
	if got, _, _ := unstructured.NestedString(kustomization.Object, "spec", "serviceAccountName"); got != names.Applier {
		t.Errorf("service account = %q, want %q", got, names.Applier)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(kustomization.Object, "spec", "sourceRef", "namespace"); found {
		t.Fatal("cross-namespace sourceRef was emitted")
	}

	platform, err := BuildAssignment(helmOCIAssignment(), testCapabilities(), ValidationPolicy{AllowPlatformScope: true})
	if err != nil {
		t.Fatal(err)
	}
	binding := platform.Objects[3]
	if binding.GetKind() != "ClusterRoleBinding" {
		t.Fatalf("platform object[3] = %s, want ClusterRoleBinding", binding.GetKind())
	}
	if got, _, _ := unstructured.NestedString(binding.Object, "roleRef", "name"); got != PlatformClusterRole {
		t.Errorf("platform roleRef = %q, want bootstrap allowlist %q", got, PlatformClusterRole)
	}
	ociSource := platform.Objects[len(platform.Objects)-2]
	if got, _, _ := unstructured.NestedString(ociSource.Object, "spec", "ref", "digest"); got != testArtifact {
		t.Errorf("OCI digest = %q, want exact resolved digest", got)
	}
	helmRelease := platform.Objects[len(platform.Objects)-1]
	if _, found, _ := unstructured.NestedMap(helmRelease.Object, "spec", "chartRef"); !found {
		t.Fatal("Helm OCI release did not use a digest-pinned OCIRepository chartRef")
	}
}

func TestBuildAssignmentDeterministicAndDoesNotAliasCredentials(t *testing.T) {
	t.Parallel()
	assignment := gitAssignment()
	first, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("equal assignments produced different materializations")
	}
	secretValue, _, _ := unstructured.NestedFieldNoCopy(first.Objects[1].Object, "data", "identity")
	secretValue.([]byte)[0] = 'X'
	if string(assignment.Credential.Data["identity"]) != "PRIVATE" {
		t.Fatal("materialized Secret aliases transport credential memory")
	}
}

func TestOCIRegistryCredentialUsesDockerSecretType(t *testing.T) {
	t.Parallel()
	assignment := ociAssignment()
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	assignment.Source.CredentialSecret = names.AuthSecret
	assignment.Credential = &protocol.DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{
		"dockerconfigjson": []byte(`{"auths":{"registry.example.test":{"auth":"cm9ib3Q6c2VjcmV0"}}}`),
	}}
	materialization, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(materialization.Objects[1].Object, "type"); got != "kubernetes.io/dockerconfigjson" {
		t.Errorf("Secret type = %q", got)
	}
	assignment.Credential.Data["username"] = []byte("robot")
	if err := ValidateAssignment(assignment, testCapabilities(), ValidationPolicy{}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mixed docker auth error = %v", err)
	}
}

func TestPlanPruneGuardsAndOrder(t *testing.T) {
	t.Parallel()
	assignment := ociAssignment()
	materialization, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	staleSource := managedObject(assignment, gitRepositoryGVK, names.ControlNamespace, names.Source, map[string]any{"spec": map[string]any{}})
	staleReconciler := managedObject(assignment, helmReleaseGVK, names.ControlNamespace, names.Base, map[string]any{"spec": map[string]any{}})
	foreign := staleSource.DeepCopy()
	foreign.SetName("foreign")
	foreign.SetLabels(map[string]string{ManagedByLabel: "somebody-else"})
	got, err := PlanPrune(assignment, materialization, []*unstructured.Unstructured{materialization.Objects[2], staleSource, foreign, staleReconciler}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "HelmRelease" || got[1].Kind != "GitRepository" {
		t.Fatalf("prune order = %#v, want reconciler then source", got)
	}
	if _, err := PlanPrune(assignment, materialization, nil, false); err == nil {
		t.Fatal("partial apply unexpectedly permitted prune")
	}
	unexpected := managedObject(assignment, secretGVK, "another-namespace", names.AuthSecret, nil)
	if _, err := PlanPrune(assignment, materialization, []*unstructured.Unstructured{unexpected}, true); err == nil {
		t.Fatal("namespace escape unexpectedly permitted prune")
	}
	unexpected = managedObject(assignment, namespaceGVK, "", names.Base, nil)
	if _, err := PlanPrune(assignment, materialization, []*unstructured.Unstructured{unexpected}, true); err == nil {
		t.Fatal("unexpected managed kind unexpectedly permitted prune")
	}
}

func TestPlanDeletionFencesAndOrphans(t *testing.T) {
	t.Parallel()
	assignment := gitAssignment()
	materialization, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	tombstone := protocol.DeliveryDeletionV2{DeploymentID: assignment.DeploymentID, Generation: assignment.Generation, SpecDigest: assignment.SpecDigest}
	got, err := PlanDeletion(assignment, tombstone, materialization)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(materialization.Objects)-1 || got[0].Kind != "Kustomization" || got[len(got)-1].Kind != "ServiceAccount" {
		t.Fatalf("safe reverse deletion order = %#v", got)
	}
	tombstone.Orphan = true
	if got, err = PlanDeletion(assignment, tombstone, materialization); err != nil || len(got) != 0 {
		t.Fatalf("orphan deletion = %#v, %v", got, err)
	}
	tombstone.Orphan = false
	tombstone.Generation++
	if _, err = PlanDeletion(assignment, tombstone, materialization); err == nil {
		t.Fatal("stale tombstone unexpectedly passed fence")
	}
}
