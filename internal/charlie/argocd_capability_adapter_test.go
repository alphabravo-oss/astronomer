package charlie

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func argoApplication(name string, owned bool) *unstructured.Unstructured {
	labels := map[string]any{}
	if owned {
		labels["astronomer.io/platform-owned"] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": "astronomer", "labels": labels},
		"spec":     map[string]any{"source": map[string]any{"repoURL": "https://SENTINEL.invalid", "targetRevision": "SENTINEL"}},
		"status":   map[string]any{"health": map[string]any{"status": "Healthy"}, "sync": map[string]any{"status": "Synced", "revision": "abc123"}},
	}}
}

func argoAdapterFixture(t *testing.T, application *unstructured.Unstructured) *ArgoCDCapabilityAdapter {
	t.Helper()
	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClient(scheme, application)
	adapter, err := NewArgoCDCapabilityAdapter(client, "astronomer", "astronomer-self-manage")
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestArgoStatusIsSanitizedAndExactApplicationOnly(t *testing.T) {
	adapter := argoAdapterFixture(t, argoApplication("astronomer-self-manage", true))
	descriptor, _ := capabilityByName("astronomer.argocd.self_management_status")
	result, err := adapter.Execute(context.Background(), descriptor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "SENTINEL") || !strings.Contains(string(result), "revision_digest") {
		t.Fatalf("unsafe Argo status: %s", result)
	}

	unowned := argoAdapterFixture(t, argoApplication("astronomer-self-manage", false))
	if _, err := unowned.Execute(context.Background(), descriptor, nil); err == nil {
		t.Fatal("unowned Application was readable")
	}
}

func TestArgoSyncHasNoPruneForceOrRevisionAndVerifies(t *testing.T) {
	adapter := argoAdapterFixture(t, argoApplication("astronomer-self-manage", true))
	descriptor, _ := capabilityByName("astronomer.argocd.self_management_sync")
	args := rawArguments(t, map[string]any{"resource_id": "resource-a", "application": "astronomer-self-manage", "operation_id": "action-a"})
	result, err := adapter.Execute(context.Background(), descriptor, args)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, args, result)
	if err != nil || !verified {
		t.Fatalf("sync verification=%v err=%v", verified, err)
	}
	application, err := adapter.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer").Get(context.Background(), "astronomer-self-manage", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, _ := unstructured.NestedMap(application.Object, "operation")
	serialized := string(mustJSON(t, operation))
	for _, forbidden := range []string{"targetRevision", "revision", "force", `"prune":true`} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("sync operation includes %s: %s", forbidden, serialized)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := runtime.Encode(unstructured.UnstructuredJSONScheme, &unstructured.Unstructured{Object: map[string]any{"value": value}})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
