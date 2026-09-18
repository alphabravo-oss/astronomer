package delivery

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func systemReleaseFixture() protocol.DeliverySystemReleaseV2 {
	return protocol.DeliverySystemReleaseV2{
		Generation: 2, Version: "v1.0.0", ArtifactURL: "oci://registry.example.test/astronomer/system",
		ArtifactDigest: "sha256:" + strings.Repeat("a", 64), DistributionDigest: "sha256:" + strings.Repeat("b", 64),
		AgentVersion: "v1.0.0", AgentImage: "registry.example.test/astronomer/agent@sha256:" + strings.Repeat("c", 64),
		MinimumKubernetes: "v1.33.0", MaximumKubernetes: "v1.35.99", CRDStorageVersion: "v1",
		Interval: "5m", Timeout: "15m", Suspend: true,
		Verification: protocol.DeliverySystemVerification{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{
			Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/release/.github/workflows/release.yaml@refs/tags/v1.0.0",
		}}},
	}
}

func systemManagerFixture(t *testing.T, release protocol.DeliverySystemReleaseV2) (*SystemManager, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	objects := systemObjects(release)
	// The bootstrap manifest creates these two resources suspended. Starting
	// from them also exercises the ownership fence before server-side apply.
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects[len(objects)-2], objects[len(objects)-1])
	dynamicClient.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		for _, object := range objects {
			if object.GetName() == patch.GetName() && object.GetNamespace() == patch.GetNamespace() {
				return true, object.DeepCopy(), nil
			}
		}
		return false, nil, nil
	})
	client := kubernetesfake.NewClientset()
	client.Discovery().(*fake.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.34.3"}
	manager, err := NewSystemManager(dynamicClient, client, SystemManagerConfig{
		CurrentAgentVersion: "1.0.0",
		TrustPolicy: SystemTrustPolicy{
			OIDCIdentities:         release.Verification.OIDCIdentities,
			KeyFingerprints:        protocol.DeliverySystemKeyFingerprints(release.Verification.DeliverySystemPublicKeySet()),
			AgentImageRepositories: []string{"registry.example.test/astronomer/agent"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, dynamicClient
}

func testSystemPublicKey(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
}

func TestSystemManagerReconcilesOnlyFixedSuspendedObjects(t *testing.T) {
	release := systemReleaseFixture()
	manager, dynamicClient := systemManagerFixture(t, release)
	complete, err := manager.Reconcile(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("a suspended, already-bootstrapped release should reconcile completely")
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetNamespace() != DeliverySystemNamespace || (action.GetVerb() != "get" && action.GetVerb() != "patch") {
			t.Fatalf("unexpected system action: %#v", action)
		}
	}
}

func TestSystemManagerRejectsUntrustedIdentityBeforeMutation(t *testing.T) {
	release := systemReleaseFixture()
	manager, dynamicClient := systemManagerFixture(t, release)
	manager.config.TrustPolicy.OIDCIdentities = []protocol.DeliveryOIDCIdentity{{Issuer: "https://issuer.example.test", Subject: "different"}}
	if _, err := manager.Reconcile(context.Background(), release); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("expected untrusted identity rejection, got %v", err)
	}
	if len(dynamicClient.Actions()) != 0 {
		t.Fatal("untrusted release caused Kubernetes actions")
	}
}

func TestSystemManagerAcceptsPinnedKeyOverlapAndRejectsUnknownKey(t *testing.T) {
	oldKey, nextKey := testSystemPublicKey(t), testSystemPublicKey(t)
	release := systemReleaseFixture()
	release.Verification = protocol.DeliverySystemVerification{Provider: "cosign", PublicKeys: [][]byte{oldKey, nextKey}}
	manager, dynamicClient := systemManagerFixture(t, release)
	if _, err := manager.Reconcile(context.Background(), release); err != nil {
		t.Fatalf("overlapping, enrolled keyring rejected: %v", err)
	}
	if len(dynamicClient.Actions()) == 0 {
		t.Fatal("trusted keyring did not reconcile")
	}

	unknown := testSystemPublicKey(t)
	release.Verification.PublicKeys = [][]byte{oldKey, unknown}
	manager, dynamicClient = systemManagerFixture(t, release)
	manager.config.TrustPolicy.KeyFingerprints = []string{protocol.DeliverySystemKeyFingerprint(oldKey)}
	if _, err := manager.Reconcile(context.Background(), release); err == nil || !strings.Contains(err.Error(), "not pinned at enrollment") {
		t.Fatalf("expected unpinned key rejection, got %v", err)
	}
	assertNoKubernetesMutations(t, dynamicClient.Actions())
}

func TestSystemManagerDoesNotDowngradeNewerAgentForPinnedSystemRelease(t *testing.T) {
	release := systemReleaseFixture()
	manager, _ := systemManagerFixture(t, release)
	manager.config.CurrentAgentVersion = "v1.2.0"
	// This old image repository is deliberately not trusted. The running agent
	// is newer than the signed release's agent image, so that image must not be
	// pulled or considered for self-upgrade.
	manager.config.TrustPolicy.AgentImageRepositories = nil

	complete, err := manager.Reconcile(context.Background(), release)
	if err != nil {
		t.Fatalf("newer agent should reconcile the pinned system release: %v", err)
	}
	if !complete {
		t.Fatal("suspended system release should complete with a newer running agent")
	}
	for _, action := range manager.client.(*kubernetesfake.Clientset).Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("system release attempted to mutate the newer agent: %#v", action)
		}
	}
}

func TestSystemManagerRequiresAllowlistedImageForForwardUpgrade(t *testing.T) {
	release := systemReleaseFixture()
	manager, dynamicClient := systemManagerFixture(t, release)
	manager.config.CurrentAgentVersion = "v0.9.0"
	manager.config.TrustPolicy.AgentImageRepositories = nil

	if _, err := manager.Reconcile(context.Background(), release); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("expected untrusted forward image to be rejected, got %v", err)
	}
	assertNoKubernetesMutations(t, dynamicClient.Actions())
	assertNoKubernetesMutations(t, manager.client.(*kubernetesfake.Clientset).Actions())
}

func TestSystemManagerUpgradesAgentForNewerTrustedRelease(t *testing.T) {
	release := systemReleaseFixture()
	release.AgentVersion = "v1.1.0"
	release.AgentImage = "registry.example.test/astronomer/agent@sha256:" + strings.Repeat("d", 64)
	manager, _ := systemManagerFixture(t, release)
	manager.config.CurrentAgentVersion = "v1.0.0"
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: defaultAgentDeployment, Namespace: defaultAgentNamespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "agent", Image: "registry.example.test/astronomer/agent@sha256:" + strings.Repeat("c", 64)}},
		}}},
	}
	client := manager.client.(*kubernetesfake.Clientset)
	if _, err := client.AppsV1().Deployments(defaultAgentNamespace).Create(context.Background(), deployment, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create current agent deployment: %v", err)
	}

	complete, err := manager.Reconcile(context.Background(), release)
	if err != nil {
		t.Fatalf("trusted forward update failed: %v", err)
	}
	if complete {
		t.Fatal("agent image update should wait for the replacement pod to reconnect")
	}
	updated, err := client.AppsV1().Deployments(defaultAgentNamespace).Get(context.Background(), defaultAgentDeployment, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated agent deployment: %v", err)
	}
	if got := updated.Spec.Template.Spec.Containers[0].Image; got != release.AgentImage {
		t.Fatalf("agent image = %q, want %q", got, release.AgentImage)
	}
}

func TestSystemManagerDoesNotReplaceEmbeddedAgent(t *testing.T) {
	release := systemReleaseFixture()
	manager, dynamicClient := systemManagerFixture(t, release)
	manager.config.CurrentAgentVersion = "v0.9.0"
	manager.config.EmbeddedAgent = true

	if _, err := manager.Reconcile(context.Background(), release); err == nil || !strings.Contains(err.Error(), "upgrade the management plane first") {
		t.Fatalf("expected an actionable embedded-agent upgrade error, got %v", err)
	}
	assertNoKubernetesMutations(t, dynamicClient.Actions())
	assertNoKubernetesMutations(t, manager.client.(*kubernetesfake.Clientset).Actions())
}

func assertNoKubernetesMutations(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "delete-collection":
			t.Fatalf("unexpected Kubernetes mutation: %#v", action)
		}
	}
}

func TestSystemObjectsNeverAcceptWorkloadNames(t *testing.T) {
	release := systemReleaseFixture()
	release.Credential = &protocol.DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{".dockerconfigjson": []byte(`{"auths":{}}`)}}
	release.Verification = protocol.DeliverySystemVerification{
		Provider: "cosign", PublicKey: []byte("trusted-public-key"),
		KeyFingerprint: "sha256:1f5a12985b67e8840864fce8c03e6398a11c2928b483a8a82060f63a08e045ca",
	}
	objects := systemObjects(release)
	if len(objects) != 4 {
		t.Fatalf("system graph contains %d objects, want 4", len(objects))
	}
	for _, object := range objects {
		if object.GetNamespace() != DeliverySystemNamespace || object.GetLabels()[systemOwnershipLabel] != "true" || !strings.HasPrefix(object.GetName(), systemObjectName) {
			t.Fatalf("object escaped the fixed system boundary: %s %s/%s", object.GetKind(), object.GetNamespace(), object.GetName())
		}
	}
}

func TestSystemObjectsRenderOverlappingCosignKeysIntoFluxTrustSecret(t *testing.T) {
	oldKey, nextKey := testSystemPublicKey(t), testSystemPublicKey(t)
	release := systemReleaseFixture()
	release.Verification = protocol.DeliverySystemVerification{Provider: "cosign", PublicKeys: [][]byte{oldKey, nextKey}}
	objects := systemObjects(release)
	if len(objects) != 3 {
		t.Fatalf("system graph contains %d objects, want trust Secret, OCIRepository and Kustomization", len(objects))
	}
	var trustSecret *unstructured.Unstructured
	for _, object := range objects {
		if object.GetKind() == "Secret" && strings.HasSuffix(object.GetName(), "-trust") {
			trustSecret = object
		}
	}
	if trustSecret == nil {
		t.Fatal("keyring release did not produce a Flux trust Secret")
	}
	data, found, err := unstructured.NestedMap(trustSecret.Object, "data")
	if err != nil || !found || len(data) != 2 {
		t.Fatalf("Flux trust Secret data = %#v, found=%v, err=%v", data, found, err)
	}
	if _, ok := data["cosign.pub"]; !ok {
		t.Fatalf("first key is missing compatibility key cosign.pub: %#v", data)
	}
	if _, ok := data["cosign-"+strings.TrimPrefix(protocol.DeliverySystemKeyFingerprint(nextKey), "sha256:")+".pub"]; !ok {
		t.Fatalf("overlap key is missing from Flux trust Secret: %#v", data)
	}
}
