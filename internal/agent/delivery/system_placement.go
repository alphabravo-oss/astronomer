package delivery

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// A single physical node cannot provide host redundancy. Keep the signed Flux
// artifact unchanged and express this narrow placement override through Flux's
// own patch transformer. Count registered nodes, not Ready nodes: a temporary
// node outage must not collapse an HA installation to the compact profile.
func (m *SystemManager) configureControllerPlacement(ctx context.Context, objects []*unstructured.Unstructured) error {
	nodes, err := m.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 2})
	if err != nil {
		return fmt.Errorf("determine delivery controller placement: %w", err)
	}
	if len(nodes.Items) != 1 || nodes.Continue != "" {
		return nil
	}
	for _, object := range objects {
		if object.GroupVersionKind() != kustomizationGVK {
			continue
		}
		patches := make([]any, 0, 3)
		for _, name := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
			patches = append(patches, map[string]any{
				"target": map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "name": name},
				"patch":  "- op: replace\n  path: /spec/replicas\n  value: 1\n",
			})
		}
		if err := unstructured.SetNestedSlice(object.Object, patches, "spec", "patches"); err != nil {
			return err
		}
	}
	return nil
}
