package delivery

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestObservedInventoryFluxSchemas(t *testing.T) {
	for _, kind := range []string{"Kustomization", "HelmRelease"} {
		t.Run(kind, func(t *testing.T) {
			// Both bundled Flux CRDs define ResourceRef as {id, v}, where id is
			// <namespace>_<name>_<group>_<kind>, including empty namespace/group.
			object := &unstructured.Unstructured{Object: map[string]any{
				"kind": kind,
				"status": map[string]any{"inventory": map[string]any{"entries": []any{
					map[string]any{"id": "workload_app_apps_Deployment", "v": "v1"},
					map[string]any{"id": "workload_app__Service", "v": "v1"},
					map[string]any{"id": "_workload__Namespace", "v": "v1"},
					map[string]any{"id": "_system:reader_rbac.authorization.k8s.io_ClusterRole", "v": "v1"},
				}}},
			}}
			want := protocol.DeliveryInventory{Entries: 4, Resources: []protocol.DeliveryResourceIdentity{
				{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "workload", Name: "app"},
				{APIVersion: "v1", Kind: "Service", Namespace: "workload", Name: "app"},
				{APIVersion: "v1", Kind: "Namespace", Name: "workload"},
				{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "system:reader"},
			}}
			if got := observedInventory(object); !reflect.DeepEqual(got, want) {
				t.Fatalf("inventory = %#v, want %#v", got, want)
			}
		})
	}
}

func TestObservedInventoryRejectsMalformedEntries(t *testing.T) {
	invalid := []any{
		nil, int64(42), "apps_v1_Deployment_workload_app",
		map[string]any{}, map[string]any{"id": int64(42), "v": "v1"},
		map[string]any{"id": "workload_app_apps_Deployment", "v": int64(1)},
	}
	for _, id := range []string{"workload_app_apps", "workload_app_apps_Deployment_extra", "workload__apps_Deployment", "workload_app_apps_", "workload_app_apps_Deploy\nment", "workload_app_apps_" + strings.Repeat("K", 129), "workload_app_bad/group_Deployment", "bad namespace_app_apps_Deployment", "workload_bad/name_apps_Deployment", "workload_bad\x00name_apps_Deployment", "workload_" + strings.Repeat("a", 254) + "_apps_Deployment"} {
		invalid = append(invalid, map[string]any{"id": id, "v": "v1"})
	}
	for _, version := range []string{"", "apps/v1", "v1\n", "v 1", "../v1", strings.Repeat("v", 254)} {
		invalid = append(invalid, map[string]any{"id": "workload_app_apps_Deployment", "v": version})
	}
	for i, entry := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"inventory": map[string]any{"entries": []any{entry, map[string]any{"id": "workload_good__Service", "v": "v1"}}}}}}
			got := observedInventory(object)
			if got.Entries != 2 || len(got.Resources) != 1 || got.Resources[0].Name != "good" || got.Ready != 0 || got.Failed != 0 {
				t.Fatalf("mixed inventory = %#v", got)
			}
		})
	}
}

func TestObservedInventoryBoundsResourcesButPreservesTotal(t *testing.T) {
	entries := []any{map[string]any{"id": "invalid", "v": "v1"}}
	for i := 0; i < protocol.MaxDeliveryInventoryEntries+3; i++ {
		entries = append(entries, map[string]any{"id": fmt.Sprintf("workload_app-%d_apps_Deployment", i), "v": "v1"})
	}
	object := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"inventory": map[string]any{"entries": entries}}}}
	got := observedInventory(object)
	if got.Entries != len(entries) || len(got.Resources) != protocol.MaxDeliveryInventoryEntries || got.Resources[len(got.Resources)-1].Name != fmt.Sprintf("app-%d", protocol.MaxDeliveryInventoryEntries-1) {
		t.Fatalf("bounded inventory = %#v", got)
	}
}

func TestNormalizedFluxInventorySatisfiesProtocol(t *testing.T) {
	assignment := gitAssignment()
	source, reconciler := observedObjects(assignment)
	observed, err := NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	status := protocol.DeliveryStatusV2{ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: "11111111-1111-4111-8111-111111111111", SessionSequence: 1, SnapshotGeneration: 1, SnapshotETag: testDigest, Deployments: []protocol.DeliveryDeploymentStatusV2{observed}}
	status.StatusDigest = status.SemanticDigest()
	if err := status.Validate(); err != nil {
		t.Fatalf("normalized Flux inventory rejected by protocol: %v", err)
	}
	if len(observed.Inventory.Resources) != 2 {
		t.Fatalf("missing decoded identities: %#v", observed.Inventory)
	}
}
