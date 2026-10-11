package delivery

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestControllerPlacementOnlyCompactsSingleNode(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		var nodes []runtime.Object
		for i := 0; i < count; i++ {
			nodes = append(nodes, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: string(rune('a' + i))}})
		}
		manager := &SystemManager{client: fake.NewSimpleClientset(nodes...)}
		object := systemObject(kustomizationGVK, systemObjectName, nil, map[string]any{"spec": map[string]any{}})
		if err := manager.configureControllerPlacement(t.Context(), []*unstructured.Unstructured{object}); err != nil {
			t.Fatal(err)
		}
		replicas, found, err := unstructured.NestedSlice(object.Object, "spec", "patches")
		if err != nil || found != (count == 1) {
			t.Fatalf("nodes=%d found=%v err=%v", count, found, err)
		}
		if count == 1 && len(replicas) != 3 {
			t.Fatal("all Flux controllers must be compacted")
		}
	}
}
