package kubeutil

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var (
	ConfigMapGVK = schema.GroupVersionKind{
		Version: "v1",
		Kind:    "ConfigMap",
	}

	ArgoApplicationGVK = schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	}

	ArgoApplicationSetGVK = schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "ApplicationSet",
	}

	ArgoApplicationGVR = schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}

	ArgoApplicationSetGVR = schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applicationsets",
	}
)

func NamespacedNameFromMeta(meta metav1.Object) types.NamespacedName {
	if meta == nil {
		return types.NamespacedName{}
	}
	return types.NamespacedName{Name: meta.GetName(), Namespace: meta.GetNamespace()}
}

func NamespacedName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: namespace}
}

func ListGVK(gvk schema.GroupVersionKind) schema.GroupVersionKind {
	return gvk.GroupVersion().WithKind(gvk.Kind + "List")
}

func NewUnstructured(gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func SpecHash(obj *unstructured.Unstructured) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("hash Kubernetes object spec: object is nil")
	}
	payload, err := json.Marshal(obj.Object["spec"])
	if err != nil {
		return "", fmt.Errorf("hash Kubernetes object %s/%s spec: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:]), nil
}

func AnnotateSpecHash(obj *unstructured.Unstructured, annotation string) (string, error) {
	hash, err := SpecHash(obj)
	if err != nil {
		return "", err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[annotation] = hash
	obj.SetAnnotations(annotations)
	return hash, nil
}
