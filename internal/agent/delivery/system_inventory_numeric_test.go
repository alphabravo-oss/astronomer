package delivery

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func numericLonghornVolume(replicas, size, used any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "longhorn.io/v1beta2", "kind": "Volume",
		"metadata": map[string]any{"name": "fixture", "namespace": "longhorn-system"},
		"spec":     map[string]any{"numberOfReplicas": replicas, "size": size},
		"status":   map[string]any{"robustness": "healthy", "actualSize": used},
	}}
}

func TestDynamicInventoryInvalidNumericCannotBecomeHealthy(t *testing.T) {
	cases := []struct {
		name                 string
		replicas, size, used any
	}{
		{"replica-positive-wrap", int64(4294967298), int64(1024), int64(512)},
		{"replica-over-max", int64(math.MaxInt32) + 1, int64(1024), int64(512)},
		{"replica-negative", int64(-1), int64(1024), int64(512)},
		{"replica-fractional", float64(2.5), int64(1024), int64(512)},
		{"size-overflow-string", int64(2), "9223372036854775808", int64(512)},
		{"size-negative", int64(2), int64(-1), int64(512)},
		{"size-fractional", int64(2), float64(1024.5), int64(512)},
		{"used-invalid-string", int64(2), int64(1024), "not-a-count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{numericLonghornVolume(tc.replicas, tc.size, tc.used)}}
			if component, ok := longhornVolumesComponent(list); ok {
				t.Fatalf("invalid numeric observation projected as usable: health=%s replicas=%d HA=%t provisioned=%d used=%d", component.Health, component.StorageReplicaCount, component.HighAvailability, component.StorageProvisionedBytes, component.StorageUsedBytes)
			}
		})
	}
}

func TestDynamicInventoryAggregateOverflowCannotWrapPositive(t *testing.T) {
	t.Run("volume-bytes", func(t *testing.T) {
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			numericLonghornVolume(int64(2), int64(math.MaxInt64), int64(math.MaxInt64)),
			numericLonghornVolume(int64(2), int64(math.MaxInt64), int64(math.MaxInt64)),
			numericLonghornVolume(int64(2), int64(4), int64(4)),
		}}
		if component, ok := longhornVolumesComponent(list); ok {
			t.Fatalf("aggregate overflow projected as usable: provisioned=%d used=%d", component.StorageProvisionedBytes, component.StorageUsedBytes)
		}
	})
	t.Run("node-disk-bytes", func(t *testing.T) {
		disks := map[string]any{}
		for index, value := range []int64{math.MaxInt64, math.MaxInt64, 4} {
			disks[fmt.Sprint(index)] = map[string]any{"storageMaximum": value, "storageScheduled": value, "storageAvailable": value}
		}
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"status": map[string]any{"diskStatus": disks}}}}}
		if component, ok := longhornNodesComponent(list); ok {
			t.Fatalf("aggregate overflow projected as usable: provisioned=%d used=%d", component.StorageProvisionedBytes, component.StorageUsedBytes)
		}
	})
	t.Run("gateway-attached-routes", func(t *testing.T) {
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"status": map[string]any{"listeners": []any{map[string]any{"attachedRoutes": int64(math.MaxInt64)}, map[string]any{"attachedRoutes": int64(math.MaxInt64)}, map[string]any{"attachedRoutes": int64(4)}}}}}}}
		if _, ok := gatewaysComponent(list); ok {
			t.Fatal("aggregate attached-route overflow projected as usable")
		}
	})
}

func TestDynamicInventoryValidNumericProjectionPreservesValues(t *testing.T) {
	cases := []struct {
		name                 string
		replicas, size, used any
		wantReplicas         int32
		wantSize, wantUsed   int64
	}{
		{"int64", int64(2), int64(1024), int64(512), 2, 1024, 512},
		{"integer-string", "2", "1024", "512", 2, 1024, 512},
		{"integral-float", float64(2), float64(1024), float64(512), 2, 1024, 512},
		{"wire-maximum", int64(math.MaxInt32), int64(math.MaxInt64), int64(math.MaxInt64), math.MaxInt32, math.MaxInt64, math.MaxInt64},
		{"optional-missing", nil, nil, nil, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := numericLonghornVolume(tc.replicas, tc.size, tc.used)
			if tc.name == "optional-missing" {
				delete(item.Object["spec"].(map[string]any), "numberOfReplicas")
				delete(item.Object["spec"].(map[string]any), "size")
				delete(item.Object["status"].(map[string]any), "actualSize")
			}
			component, ok := longhornVolumesComponent(&unstructured.UnstructuredList{Items: []unstructured.Unstructured{item}})
			if !ok || component.StorageReplicaCount != tc.wantReplicas || component.StorageProvisionedBytes != tc.wantSize || component.StorageUsedBytes != tc.wantUsed || component.Health != "healthy" {
				t.Fatalf("valid numeric projection changed: usable=%t replicas=%d bytes=%d/%d health=%s", ok, component.StorageReplicaCount, component.StorageProvisionedBytes, component.StorageUsedBytes, component.Health)
			}
		})
	}
}

func TestDynamicInventoryInvalidProjectionRemainsExplicitlyUnavailable(t *testing.T) {
	dynamic := dynamicFixture()
	dynamic.PrependReactor("list", "volumes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{numericLonghornVolume(int64(4294967298), int64(1024), int64(512))}}, nil
	})
	probe := &ClusterProbe{dynamic: dynamic, platformScope: true}
	snapshot, _ := probe.readDynamicInventory(context.Background(), time.Now().UTC())
	for _, component := range snapshot.components {
		if component.Name == "Longhorn volumes" && component.Kind == "InventoryAvailability" && component.Health == "unavailable" && component.Observation != nil && component.Observation.State == protocol.ObservationUnavailable && component.Observation.ObservedAt == nil {
			return
		}
	}
	t.Fatal("invalid numeric observation was omitted or relabeled current instead of explicit unavailable")
}
