package kubeutil

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNamespacedNameFromMeta(t *testing.T) {
	key := NamespacedNameFromMeta(&metav1.ObjectMeta{Name: "app", Namespace: "team-a"})
	if key.Name != "app" || key.Namespace != "team-a" {
		t.Fatalf("unexpected key: %#v", key)
	}
}

func TestListGVK(t *testing.T) {
	list := ListGVK(ArgoApplicationSetGVK)
	if list.Group != "argoproj.io" || list.Version != "v1alpha1" || list.Kind != "ApplicationSetList" {
		t.Fatalf("unexpected list GVK: %#v", list)
	}
}

func TestAnnotateSpecHash(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"project": "default"},
	}}
	hash, err := AnnotateSpecHash(obj, "example.com/spec-hash")
	if err != nil {
		t.Fatalf("AnnotateSpecHash returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if obj.GetAnnotations()["example.com/spec-hash"] != hash {
		t.Fatalf("annotation was not set to hash: %#v", obj.GetAnnotations())
	}
}
