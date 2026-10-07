package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func dynamicFixture() *dynamicfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{}
	for _, kind := range dynamicInventoryKinds {
		kinds[kind.gvr] = "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds)
}
func TestSharedProbeCoalescesDiscoveryAndDynamicAcrossTenReads(t *testing.T) {
	probe, client, source := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	var discoveries atomic.Int32
	reader := probe.observations.readDiscovery
	probe.observations.readDiscovery = func(ctx context.Context) discoverySnapshot { discoveries.Add(1); return reader(ctx) }
	var group sync.WaitGroup
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, _, err := probe.InspectObserved(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if len(client.Actions()) != 0 || len(dynamic.Actions()) != 5 || discoveries.Load() != 1 {
		t.Fatalf("ten reads: typed=%d dynamic=%d discovery batches=%d", len(client.Actions()), len(dynamic.Actions()), discoveries.Load())
	}
	t.Log("10 modern probes: 0 typed LIST/GET; 1 discovery refresh; 5 dynamic LISTs once")
	snapshot := source[observation.Deployments]
	snapshot.Deployments[0].Generation++
	source[observation.Deployments] = snapshot
	if _, _, err := probe.InspectObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	if discoveries.Load() != 2 || len(dynamic.Actions()) != 10 {
		t.Fatal("controller revision did not invalidate bounded refresh")
	}
}
func TestSharedProbeDynamicPermissionLossReplacesHealthyEvidence(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	now := time.Now().UTC()
	probe.observations.now = func() time.Time { return now }
	if _, _, err := probe.InspectObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	dynamic.PrependReactor("list", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "", nil)
	})
	now = now.Add(2 * time.Minute)
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, component := range inventory.SystemComponents {
		if component.Kind == "InventoryAvailability" && component.Observation.State == protocol.ObservationDenied {
			seen++
			if component.Health == "healthy" {
				t.Fatal("revoked dynamic source healthy")
			}
		}
	}
	if seen != 5 {
		t.Fatalf("denied dynamic kinds=%d want5", seen)
	}
}
func TestRefreshSlotWaitersCancelWithoutSpawningAnotherRefresh(t *testing.T) {
	slot := refreshSlot[int]{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	refresh := func(ctx context.Context) (int, time.Duration) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 1, time.Minute
	}
	go func() { defer close(done); _, _ = slot.get(context.Background(), "same", time.Now, refresh) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := slot.get(ctx, "same", time.Now, refresh); !errors.Is(err, context.Canceled) {
		t.Fatal("waiting caller ignored cancellation", err)
	}
	close(release)
	<-done
	if calls.Load() != 1 {
		t.Fatal("canceled waiter spawned duplicate refresh")
	}
}
func TestSharedProbeMissingContextDiscoveryTransportFailsClosed(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	probe.observations.readDiscovery = func(ctx context.Context) discoverySnapshot { return probe.readDiscovery(ctx, true) }
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || inventory.Observation.State != protocol.ObservationUnavailable {
		t.Fatal("nil REST transport used uncancellable discovery fallback")
	}
}

func TestRefreshExpiryUsesStartNotCompletion(t *testing.T) {
	slot := refreshSlot[int]{}
	now := time.Now()
	calls := 0
	refresh := func(context.Context) (int, time.Duration) {
		calls++
		now = now.Add(2 * time.Second)
		return calls, time.Second
	}
	if _, err := slot.get(context.Background(), "same", func() time.Time { return now }, refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := slot.get(context.Background(), "same", func() time.Time { return now }, refresh); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("slow refresh extended expired result beyond its source deadline")
	}
}

func TestCertificateRefreshCrossingExpiryNeverReportsHealthy(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	now := time.Now().UTC()
	expiry := now.Add(time.Second)
	probe.observations.now = func() time.Time { return now }
	dynamic.PrependReactor("list", "certificates", func(ktesting.Action) (bool, runtime.Object, error) {
		now = now.Add(2 * time.Second)
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": map[string]any{"name": "cert", "namespace": "default"}, "status": map[string]any{"notAfter": expiry.Format(time.RFC3339Nano), "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}}}}, nil
	})
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range inventory.SystemComponents {
		if component.Kind == "CertificateInventory" {
			found = true
			if component.Health == "healthy" || component.Resources[0].Health == "healthy" {
				t.Fatal("refresh latency extended certificate health past expiry")
			}
		}
	}
	if !found {
		t.Fatal("certificate projection missing")
	}
}

type revisionFixture struct {
	snapshotFixture
	revision uint64
}

func (s *revisionFixture) DiscoveryRevision() uint64 { return s.revision }
func TestCRDRevisionInvalidatesDiscoveryAndAbsentDynamicSources(t *testing.T) {
	probe, _, source := sharedProbeFixture(t)
	revision := &revisionFixture{snapshotFixture: source}
	probe.observations.source = revision
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	var calls int
	reader := probe.observations.readDiscovery
	probe.observations.readDiscovery = func(ctx context.Context) discoverySnapshot { calls++; return reader(ctx) }
	if _, _, err := probe.InspectObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	revision.revision++
	if _, _, err := probe.InspectObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(dynamic.Actions()) != 10 {
		t.Fatal("CRD change did not invalidate previously absent source")
	}
}

func TestLonghornVolumeDetailsInheritSnapshotPermissionFailure(t *testing.T) {
	probe, _, source := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	class := source[observation.StorageClasses]
	expand := true
	class.StorageClasses = []*storagev1.StorageClass{{ObjectMeta: metav1.ObjectMeta{Name: "longhorn"}, Provisioner: "driver.longhorn.io", AllowVolumeExpansion: &expand}}
	source[observation.StorageClasses] = class
	claims := source[observation.Claims]
	name := "longhorn"
	claims.Claims = []*corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "default"}, Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &name}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}}
	source[observation.Claims] = claims
	dynamic.PrependReactor("list", "volumes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "longhorn.io/v1beta2", "kind": "Volume", "metadata": map[string]any{"name": "data", "namespace": "longhorn-system"}, "status": map[string]any{"robustness": "healthy"}}}}}, nil
	})
	dynamic.PrependReactor("list", "volumesnapshots", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "", nil)
	})
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range inventory.SystemComponents {
		if component.Kind == "VolumeInventory" {
			found = true
			if component.Health == "healthy" || component.Observation.State != protocol.ObservationDenied {
				t.Fatal("Longhorn row attached unverified snapshot counts under healthy metadata")
			}
		}
	}
	if !found {
		t.Fatal("Longhorn inventory missing")
	}
}

func TestModernBoundsPreserveObservationContract(t *testing.T) {
	now := time.Now().UTC()
	source := protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &now}
	components := boundSystemComponents([]protocol.SystemComponent{withObservation(protocol.SystemComponent{ID: "valid", Name: "valid", Health: "healthy"}, source), withObservation(protocol.SystemComponent{ID: "invalid", Name: "invalid", Health: "healthy", DesiredReplicas: -1}, source)})
	if components[0].ID != "valid" || components[0].Observation == nil {
		t.Fatal("standalone bounds check rejected valid modern component")
	}
	if components[1].Observation == nil || components[1].Observation.State != protocol.ObservationUnavailable {
		t.Fatal("invalid optional component broke negotiated contract")
	}
	if err := (protocol.DeliveryControllerInventory{Observation: &source, SystemComponents: components}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicCacheRetainsBoundedBackingArraysAndAggregateCounts(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	items := make([]unstructured.Unstructured, 300)
	for i := range items {
		items[i] = unstructured.Unstructured{Object: map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": map[string]any{"name": fmt.Sprintf("certificate-%03d", i), "namespace": "default"}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}}
	}
	dynamic.PrependReactor("list", "certificates", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: items}, nil
	})
	if _, _, err := probe.InspectObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	cache := &probe.observations.dynamic
	cache.mu.Lock()
	defer cache.mu.Unlock()
	found := false
	for _, component := range cache.value.components {
		if component.Kind == "CertificateInventory" {
			found = true
			if len(component.Resources) != 128 || cap(component.Resources) != 128 {
				t.Fatalf("retained resources len/cap=%d/%d", len(component.Resources), cap(component.Resources))
			}
			if component.DesiredReplicas != 300 || component.ReadyReplicas != 300 || !strings.Contains(component.Detail, "Showing 128 of 300") {
				t.Fatal("truncation lost aggregate evidence", component)
			}
		}
	}
	if !found {
		t.Fatal("certificate cache missing")
	}
}

func TestReturnedDynamicObservationCannotMutateRefreshCache(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	dynamic.PrependReactor("list", "gateways", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": map[string]any{"name": "gateway", "namespace": "default"}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Programmed", "status": "True"}}}}}}}, nil
	})
	first, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var original time.Time
	found := false
	for _, component := range first.SystemComponents {
		if component.Kind == "GatewayInventory" {
			found = true
			original = *component.Observation.ObservedAt
			component.Observation.State = protocol.ObservationDenied
			*component.Observation.ObservedAt = time.Time{}
			component.Resources[0].Name = "mutated"
		}
	}
	if !found {
		t.Fatal("gateway inventory missing")
	}
	second, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range second.SystemComponents {
		if component.Kind == "GatewayInventory" {
			if component.Observation.State != protocol.ObservationCurrent || !component.Observation.ObservedAt.Equal(original) || component.Resources[0].Name != "gateway" {
				t.Fatal("returned dynamic inventory mutated cache")
			}
			return
		}
	}
	t.Fatal("mutated cached observation disappeared")
}

func TestLaterDynamicRequestCannotExtendCertificateHealth(t *testing.T) {
	probe, _, _ := sharedProbeFixture(t)
	dynamic := dynamicFixture()
	probe.WithDynamicClient(dynamic)
	now := time.Now().UTC()
	expiry := now.Add(time.Second)
	probe.observations.now = func() time.Time { return now }
	dynamic.PrependReactor("list", "certificates", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": map[string]any{"name": "cert", "namespace": "default"}, "status": map[string]any{"notAfter": expiry.Format(time.RFC3339Nano), "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}}}}, nil
	})
	dynamic.PrependReactor("list", "gateways", func(ktesting.Action) (bool, runtime.Object, error) {
		now = now.Add(2 * time.Second)
		return true, &unstructured.UnstructuredList{}, nil
	})
	inventory, _, err := probe.InspectObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range inventory.SystemComponents {
		if component.Kind == "CertificateInventory" {
			if component.Health == "healthy" || component.ReadyReplicas != 0 || component.Resources[0].Health == "healthy" {
				t.Fatal("later request latency preserved expired certificate health")
			}
			return
		}
	}
	t.Fatal("certificate inventory missing")
}
