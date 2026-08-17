package delivery

import (
	"context"
	"strings"
	"testing"

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
			AgentImageRepositories: []string{"registry.example.test/astronomer/agent"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, dynamicClient
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
