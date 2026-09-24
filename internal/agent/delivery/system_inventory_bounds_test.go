package delivery

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestInvalidOptionalObservationDoesNotBreakDelivery(t *testing.T) {
	components := boundSystemComponents([]protocol.SystemComponent{{ID: "bad", Name: "observed", ReadyReplicas: -1}})
	if err := (protocol.DeliveryControllerInventory{SystemComponents: components}).Validate(); err != nil {
		t.Fatal(err)
	}
	if components[0].Health != "unknown" || !strings.Contains(components[0].Detail, "excluded") {
		t.Fatal("missing explicit unavailable observation")
	}
}

func TestInventoryDetailBoundsRemainValidUTF8(t *testing.T) {
	value := inventoryText(strings.Repeat("é\n", 300), 512)
	if len(value) > 512 || strings.Contains(value, "\n") || !strings.HasSuffix(value, "...") {
		t.Fatal("detail was not bounded")
	}
}

func TestSystemInventoryImageAndVolumeBounds(t *testing.T) {
	component := protocol.SystemComponent{ID: "storage", Name: "Storage", Health: "unknown"}
	for i := 129; i > 0; i-- {
		component.Images = append(component.Images, fmt.Sprintf("image-%03d", i))
		component.Volumes = append(component.Volumes, protocol.SystemVolumeObservation{Name: fmt.Sprintf("volume-%03d", i), Namespace: "default"})
	}
	bounded := boundSystemComponents([]protocol.SystemComponent{component})
	if err := (protocol.DeliveryControllerInventory{SystemComponents: bounded}).Validate(); err != nil {
		t.Fatal(err)
	}
	got := bounded[0]
	if len(got.Images) != 32 || len(got.Volumes) != 128 || got.Images[0] != "image-001" || got.Volumes[0].Name != "volume-001" {
		t.Fatal("expected deterministic, independently bounded nested observations")
	}
	if !strings.Contains(got.Detail, "32 of 129 images") || !strings.Contains(got.Detail, "128 of 129 volumes") {
		t.Fatal("both truncations must be disclosed")
	}
}

func TestSystemInventoryNestedBounds(t *testing.T) {
	for _, count := range []int{128, 129, 500} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
			objects := make([]runtime.Object, count)
			for i := range objects {
				objects[i] = &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
					"metadata": map[string]any{"name": fmt.Sprintf("cert-%04d", i), "namespace": "default"},
				}}
			}
			client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "CertificateList"}, objects...)
			probe := &ClusterProbe{dynamic: client}
			component, ok := probe.inspectCertificates(context.Background())
			if !ok {
				t.Fatal("certificate inventory missing")
			}
			if component.DesiredReplicas != int32(count) {
				t.Fatal("aggregate total lost")
			}
			inventory := protocol.DeliveryControllerInventory{SystemComponents: boundSystemComponents([]protocol.SystemComponent{component})}
			if err := inventory.Validate(); err != nil {
				t.Fatal(err)
			}
			if got := len(inventory.SystemComponents[0].Resources); got != 128 {
				t.Fatalf("resources = %d", got)
			}
			if count > 128 && inventory.SystemComponents[0].Detail == component.Detail {
				t.Fatal("truncation must be disclosed")
			}
			if inventory.SystemComponents[0].Resources[0].Name != "cert-0000" {
				t.Fatal("sample must be deterministic")
			}
		})
	}
}

func TestGatewayObjectCountDoesNotClaimHA(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	objects := []runtime.Object{}
	for _, name := range []string{"a", "b"} {
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway"}}
		obj.SetName(name)
		obj.SetNamespace(metav1.NamespaceDefault)
		objects = append(objects, obj)
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "GatewayList"})
	for _, object := range objects {
		if _, err := client.Resource(gvr).Namespace(metav1.NamespaceDefault).Create(context.Background(), object.(*unstructured.Unstructured), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	probe := &ClusterProbe{dynamic: client}
	component, ok := probe.inspectGateways(context.Background())
	if !ok || component.HighAvailability || component.DesiredReplicas != 2 {
		t.Fatal("object count is not a topology observation")
	}
}
