package delivery

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/client-go/dynamic/fake"
)

type memoryCheckpointStore struct{ value checkpoint }

func (s *memoryCheckpointStore) Load(context.Context) (checkpoint, error) { return s.value, nil }
func (s *memoryCheckpointStore) Save(_ context.Context, value checkpoint) error {
	s.value = value
	return nil
}

type staticCapabilityProbe struct {
	inventory    protocol.DeliveryControllerInventory
	capabilities Capabilities
}

func (p staticCapabilityProbe) Inspect(context.Context) (protocol.DeliveryControllerInventory, Capabilities, error) {
	return p.inventory, p.capabilities, nil
}

func newRuntimeFixture(t *testing.T) (*Runtime, *memoryCheckpointStore) {
	t.Helper()
	executor, _ := newExecutorFixture(t)
	store := &memoryCheckpointStore{value: emptyCheckpoint()}
	runtime, err := NewRuntime(RuntimeConfig{ClusterID: "44444444-4444-4444-8444-444444444444"}, executor, store, staticCapabilityProbe{
		inventory: protocol.DeliveryControllerInventory{
			FluxVersion: "v2.9.3", Components: map[string]string{},
			APIVersions:       []string{"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"},
			KubernetesVersion: "v1.35.0", DistributionDigest: testDigest, Ready: true,
		},
		capabilities: testCapabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.checkpoint = emptyCheckpoint()
	return runtime, store
}

func canonicalSnapshot(t *testing.T, generation int64, assignments []protocol.DeliveryAssignmentV2, deletions []protocol.DeliveryDeletionV2) protocol.DeliveryStateResponseV2 {
	t.Helper()
	snapshot := protocol.DeliveryStateResponseV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, SnapshotGeneration: generation,
		FullSnapshot: true, Assignments: assignments, Deletions: deletions, CredentialEpoch: generation,
	}
	etag, err := snapshot.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ETag = etag
	return snapshot
}

func TestRuntimeValidatesWholeSnapshotBeforeWritesAndZeroesCredentials(t *testing.T) {
	runtime, store := newRuntimeFixture(t)
	valid := gitAssignment()
	invalid := gitAssignment()
	invalid.DeploymentID = "not-a-uuid"
	snapshot := canonicalSnapshot(t, 1, []protocol.DeliveryAssignmentV2{valid, invalid}, nil)
	if err := runtime.processSnapshot(context.Background(), snapshot, testCapabilities()); err == nil {
		t.Fatal("expected invalid full snapshot to be rejected")
	}
	if len(runtime.executor.client.(*fake.FakeDynamicClient).Actions()) != 0 {
		t.Fatal("invalid full snapshot caused Kubernetes side effects")
	}
	if store.value.SnapshotGeneration != 0 || len(store.value.Assignments) != 0 {
		t.Fatal("invalid snapshot changed checkpoint")
	}
	for _, value := range valid.Credential.Data {
		for _, b := range value {
			if b != 0 {
				t.Fatal("credential byte was not zeroed after snapshot handling")
			}
		}
	}
}

func TestRuntimeApplyRestartCheckpointAndStagedDeletion(t *testing.T) {
	runtime, store := newRuntimeFixture(t)
	assignment := gitAssignment()
	snapshot := canonicalSnapshot(t, 1, []protocol.DeliveryAssignmentV2{assignment}, nil)
	if err := runtime.processSnapshot(context.Background(), snapshot, testCapabilities()); err != nil {
		t.Fatal(err)
	}
	if runtime.checkpoint.SnapshotGeneration != 1 || len(runtime.checkpoint.Assignments) != 1 {
		t.Fatalf("unexpected accepted checkpoint: %#v", runtime.checkpoint)
	}
	encoded := strings.Builder{}
	for _, accepted := range store.value.Assignments {
		encoded.WriteString(accepted.ControlNamespace)
		for _, object := range accepted.Objects {
			encoded.WriteString(object.String())
		}
	}
	for _, forbidden := range []string{"PRIVATE", "example.test", "ENVIRONMENT"} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("accepted checkpoint leaked %q", forbidden)
		}
	}

	tombstone := protocol.DeliveryDeletionV2{DeploymentID: assignment.DeploymentID, Generation: assignment.Generation, SpecDigest: assignment.SpecDigest}
	deletion := canonicalSnapshot(t, 2, nil, []protocol.DeliveryDeletionV2{tombstone})
	for stage := 0; stage < 2; stage++ {
		if err := runtime.processSnapshot(context.Background(), deletion, testCapabilities()); err == nil || !strings.Contains(err.Error(), "in progress") {
			t.Fatalf("stage %d should remain in progress, got %v", stage, err)
		}
		if runtime.checkpoint.SnapshotGeneration != 1 {
			t.Fatal("deletion was acknowledged before all fenced objects disappeared")
		}
	}
	if err := runtime.processSnapshot(context.Background(), deletion, testCapabilities()); err != nil {
		t.Fatal(err)
	}
	if runtime.checkpoint.SnapshotGeneration != 2 || len(runtime.checkpoint.Assignments) != 0 {
		t.Fatalf("deletion was not durably acknowledged: %#v", runtime.checkpoint)
	}
	if runtime.transient[assignment.DeploymentID].Phase != "removed" {
		t.Fatal("removed status was not retained for server acknowledgement")
	}
}
