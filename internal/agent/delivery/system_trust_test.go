package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

//nolint:paralleltest // mutates the package-level trustBundlePath
func TestTrustBundleMountsAgentCADirectoryIntoSourceController(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\nA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := trustBundlePath
	trustBundlePath = bundle
	t.Cleanup(func() { trustBundlePath = previous })

	single := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
	client := fake.NewSimpleClientset(single)
	manager := &SystemManager{client: client}
	object := systemObject(kustomizationGVK, systemObjectName, nil, map[string]any{"spec": map[string]any{}})
	objects := []*unstructured.Unstructured{object}
	if err := manager.configureControllerPlacement(t.Context(), objects); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent: a second reconcile must not fail on the existing ConfigMap
		object.Object["spec"] = map[string]any{}
		if err := manager.configureControllerPlacement(t.Context(), objects); err != nil {
			t.Fatal(err)
		}
		if err := manager.configureTrustBundle(t.Context(), objects); err != nil {
			t.Fatal(err)
		}
	}
	patches, _, _ := unstructured.NestedSlice(object.Object, "spec", "patches")
	if len(patches) != 4 {
		t.Fatalf("want 3 placement + 1 trust patch, got %d", len(patches))
	}
	trust, _ := patches[3].(map[string]any)["patch"].(string)
	for _, want := range []string{"name: source-controller", "mountPath: /etc/ssl/certs", "readOnly: true", "name: " + TrustBundleConfigMap} {
		if !strings.Contains(trust, want) {
			t.Fatalf("trust patch missing %q:\n%s", want, trust)
		}
	}
	cm, err := client.CoreV1().ConfigMaps(DeliverySystemNamespace).Get(t.Context(), TrustBundleConfigMap, metav1.GetOptions{})
	if err != nil || !strings.Contains(cm.Data[trustBundleKey], "BEGIN CERTIFICATE") {
		t.Fatalf("trust bundle ConfigMap not written: %v", err)
	}
}

//nolint:paralleltest // mutates the package-level trustBundlePath
func TestTrustBundleAbsentLeavesKustomizationUntouched(t *testing.T) {
	previous := trustBundlePath
	trustBundlePath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { trustBundlePath = previous })
	manager := &SystemManager{client: fake.NewSimpleClientset()}
	object := systemObject(kustomizationGVK, systemObjectName, nil, map[string]any{"spec": map[string]any{}})
	if err := manager.configureTrustBundle(t.Context(), []*unstructured.Unstructured{object}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := unstructured.NestedSlice(object.Object, "spec", "patches"); found {
		t.Fatal("no bundle must mean no patch")
	}
}
