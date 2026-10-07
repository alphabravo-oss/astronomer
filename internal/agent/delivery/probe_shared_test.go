package delivery

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clientfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type snapshotFixture map[observation.Kind]observation.Snapshot

func (s snapshotFixture) ObservationSnapshot(kind observation.Kind) observation.Snapshot {
	return s[kind]
}
func sharedProbeFixture(t *testing.T) (*ClusterProbe, *clientfake.Clientset, snapshotFixture) {
	t.Helper()
	client := clientfake.NewClientset()
	discovery := &discoveryfake.FakeDiscovery{Fake: &ktesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: "v1.35.2"}}
	for _, api := range expectedFluxAPIs {
		discovery.Resources = append(discovery.Resources, &metav1.APIResourceList{GroupVersion: api})
	}
	source := snapshotFixture{}
	at := time.Now().UTC().Add(-time.Minute)
	for _, kind := range observation.Kinds() {
		source[kind] = observation.Snapshot{Kind: kind, Observation: protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}}
	}
	snapshot := source[observation.Deployments]
	snapshot.Deployments = []*appsv1.Deployment{readyController(t, "source-controller", false), readyController(t, "kustomize-controller", true), readyController(t, "helm-controller", false)}
	source[observation.Deployments] = snapshot
	probe, err := NewClusterProbe(client, discovery, true)
	if err != nil {
		t.Fatal(err)
	}
	probe.WithObservationSource(source)
	// Explicit context-aware fake seam: production never calls uncancellable
	// discovery methods from the modern refresh path.
	probe.observations.readDiscovery = func(ctx context.Context) discoverySnapshot {
		result := probe.readDiscovery(ctx, false)
		at := probe.observations.now().UTC()
		result.observation = protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}
		if err := ctx.Err(); err != nil {
			result.observation = errorObservation(err)
		}
		return result
	}
	return probe, client, source
}
func TestSharedProbeTenInspectionsUseNoTypedReads(t *testing.T) {
	probe, client, source := sharedProbeFixture(t)
	for range 10 {
		inventory, _, err := probe.InspectObserved(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !inventory.Ready {
			t.Fatalf("valid cached controllers not ready: %+v", inventory)
		}
		if !inventory.Observation.ObservedAt.Equal(*source[observation.Deployments].Observation.ObservedAt) {
			t.Fatal("source timestamp replaced")
		}
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("ten modern inspections made typed calls: %v", client.Actions())
	}
}

func TestSharedProbeDeniedOptionalAndDeletedController(t *testing.T) {
	probe, client, source := sharedProbeFixture(t)
	denied := source[observation.Claims]
	denied.Observation = protocol.DeliveryObservation{State: protocol.ObservationDenied}
	source[observation.Claims] = denied
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Ready {
		t.Fatal("optional denied PVC inventory disabled valid Flux")
	}
	found := false
	for _, component := range inventory.SystemComponents {
		if component.ID == "cluster/observation/persistentvolumeclaim" {
			found = true
			if component.Observation.State != protocol.ObservationDenied || component.Health == "healthy" {
				t.Fatal("denied optional kind hidden")
			}
		}
	}
	if !found {
		t.Fatal("denied and absent are indistinguishable")
	}
	deleted := source[observation.Deployments]
	deleted.Deployments = deleted.Deployments[1:]
	source[observation.Deployments] = deleted
	inventory, _, err = probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready {
		t.Fatal("deleted controller remained ready")
	}
	if len(client.Actions()) != 0 {
		t.Fatal("missing/denied store fell back to API")
	}
}

func TestSharedProbeStaleSourceNeverReadyAndKeepsTimestamp(t *testing.T) {
	for _, state := range []protocol.ObservationState{protocol.ObservationStale, protocol.ObservationDenied, protocol.ObservationUnsynced, protocol.ObservationDisconnected} {
		t.Run(string(state), func(t *testing.T) {
			probe, client, source := sharedProbeFixture(t)
			snapshot := source[observation.Deployments]
			snapshot.Observation.State = state
			source[observation.Deployments] = snapshot
			inventory, capabilities, err := probe.InspectObserved(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if inventory.Ready || capabilities.NamespaceScope || capabilities.PlatformScope {
				t.Fatal("noncurrent controller source enabled delivery")
			}
			if inventory.Observation.State != state || !inventory.Observation.ObservedAt.Equal(*snapshot.Observation.ObservedAt) {
				t.Fatal("source freshness changed")
			}
			if len(client.Actions()) != 0 {
				t.Fatal("noncurrent source silently probed API")
			}
		})
	}
}

func TestSharedProbeWarmStandbyAndOldCurrentTimestamp(t *testing.T) {
	probe, _, source := sharedProbeFixture(t)
	snapshot := source[observation.Deployments]
	sourceController := snapshot.Deployments[0]
	replicas := int32(2)
	sourceController.Spec.Replicas = &replicas
	sourceController.Status.Conditions = nil
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil || !inventory.Ready {
		t.Fatal("warm standby regression", err)
	}
	old := time.Now().Add(-5 * time.Minute)
	snapshot.Observation.ObservedAt = &old
	source[observation.Deployments] = snapshot
	inventory, _, err = probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || inventory.Observation.State != protocol.ObservationStale {
		t.Fatal("historical current label bypassed aging")
	}
}

func TestSharedProbeSamplesRevocationAfterRefreshIO(t *testing.T) {
	probe, _, source := sharedProbeFixture(t)
	read := probe.observations.readDiscovery
	probe.observations.readDiscovery = func(ctx context.Context) discoverySnapshot {
		result := read(ctx)
		snapshot := source[observation.Deployments]
		snapshot.Observation.State = protocol.ObservationDenied
		source[observation.Deployments] = snapshot
		return result
	}
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || inventory.Observation.State != protocol.ObservationDenied {
		t.Fatal("pre-I/O snapshot hid revocation during bounded refresh")
	}
}
