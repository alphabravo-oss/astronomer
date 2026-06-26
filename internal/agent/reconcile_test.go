package agent

import (
	"context"
	"encoding/json"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// configMapGVR is the namespaced resource the reconcile tests exercise.
var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// staticResolver resolves only the kinds the tests use, namespaced.
func staticResolver(t *testing.T) GVRResolver {
	t.Helper()
	return func(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
		switch gvk.Kind {
		case "ConfigMap":
			return configMapGVR, true, nil
		case "Secret":
			return schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, true, nil
		case "Namespace":
			// cluster-scoped
			return schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, false, nil
		default:
			return schema.GroupVersionResource{}, false, &apierrors.StatusError{ErrStatus: metav1.Status{Message: "no mapping for " + gvk.String()}}
		}
	}
}

func newFakeDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		{Version: "v1", Resource: "secrets"}:    "SecretList",
		{Version: "v1", Resource: "namespaces"}: "NamespaceList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
}

func cmManifest(ns, name string) protocol.DesiredManifest {
	content := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: " + ns + "\ndata:\n  k: v\n"
	return protocol.DesiredManifest{Name: "baseline-" + name, Kind: "BaselineComponent", Namespace: ns, Content: content}
}

func getCM(t *testing.T, dyn dynamic.Interface, ns, name string) (*unstructured.Unstructured, error) {
	t.Helper()
	return dyn.Resource(configMapGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
}

// existingManagedCM builds a pre-existing managed ConfigMap object (carries the
// managed-by label) for prune tests.
func existingManagedCM(ns, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				argolabels.ManagedByLabelKey: argolabels.ManagedByLabelValue,
			},
		},
		"data": map[string]any{"k": "v"},
	}}
	return obj
}

// existingUnmanagedCM builds a pre-existing ConfigMap WITHOUT the managed-by
// label — prune must never touch it.
func existingUnmanagedCM(ns, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"data": map[string]any{"k": "v"},
	}}
	return obj
}

// TestReconcileAppliesWithManagedLabel: applying a desired set creates the
// objects with the managed-by label in an astronomer-* namespace.
func TestReconcileAppliesWithManagedLabel(t *testing.T) {
	dyn := newFakeDyn()
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)

	desired := protocol.DesiredStateResponsePayload{
		ClusterID: "cluster-1",
		Revision:  "sha256:abc",
		Manifests: []protocol.DesiredManifest{
			cmManifest("astronomer-monitoring", "kube-state-metrics"),
			cmManifest("astronomer-system", "agent-config"),
		},
	}

	status := h.Reconcile(context.Background(), desired)
	if !status.Success {
		t.Fatalf("expected success, got %+v", status)
	}
	if status.Revision != "sha256:abc" {
		t.Fatalf("revision not echoed: %q", status.Revision)
	}
	if len(status.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(status.Results))
	}

	for _, tc := range []struct{ ns, name string }{
		{"astronomer-monitoring", "kube-state-metrics"},
		{"astronomer-system", "agent-config"},
	} {
		obj, err := getCM(t, dyn, tc.ns, tc.name)
		if err != nil {
			t.Fatalf("expected %s/%s applied: %v", tc.ns, tc.name, err)
		}
		if got := obj.GetLabels()[argolabels.ManagedByLabelKey]; got != argolabels.ManagedByLabelValue {
			t.Fatalf("%s/%s missing managed-by label, got %q", tc.ns, tc.name, got)
		}
	}
}

// TestReconcileIsIdempotent: applying the same set twice updates rather than
// errors, and keeps the managed-by label.
func TestReconcileIsIdempotent(t *testing.T) {
	dyn := newFakeDyn()
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)
	desired := protocol.DesiredStateResponsePayload{
		Manifests: []protocol.DesiredManifest{cmManifest("astronomer-system", "agent-config")},
	}
	if st := h.Reconcile(context.Background(), desired); !st.Success {
		t.Fatalf("first apply failed: %+v", st)
	}
	if st := h.Reconcile(context.Background(), desired); !st.Success {
		t.Fatalf("second apply failed: %+v", st)
	}
	if _, err := getCM(t, dyn, "astronomer-system", "agent-config"); err != nil {
		t.Fatalf("object missing after re-apply: %v", err)
	}
}

// TestReconcileRejectsNonOwnedNamespace: a manifest targeting a
// non-astronomer namespace is rejected and never applied.
func TestReconcileRejectsNonOwnedNamespace(t *testing.T) {
	dyn := newFakeDyn()
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)

	desired := protocol.DesiredStateResponsePayload{
		Manifests: []protocol.DesiredManifest{
			cmManifest("kube-system", "evil"),       // forbidden namespace
			cmManifest("astronomer-system", "good"), // allowed
		},
	}
	status := h.Reconcile(context.Background(), desired)
	if status.Success {
		t.Fatalf("expected overall failure due to forbidden namespace")
	}

	// The forbidden object must NOT exist.
	if _, err := getCM(t, dyn, "kube-system", "evil"); !apierrors.IsNotFound(err) {
		t.Fatalf("forbidden object should not have been applied; err=%v", err)
	}
	// The allowed object must exist (other manifests still process).
	if _, err := getCM(t, dyn, "astronomer-system", "good"); err != nil {
		t.Fatalf("allowed object should have applied: %v", err)
	}

	// Verify the failing result entry is the forbidden one.
	var sawForbiddenFail bool
	for _, r := range status.Results {
		if r.Name == "baseline-evil" && !r.Applied && r.Error != "" {
			sawForbiddenFail = true
		}
	}
	if !sawForbiddenFail {
		t.Fatalf("expected a failing result for the forbidden manifest, got %+v", status.Results)
	}
}

// TestReconcileRejectsClusterScoped: a cluster-scoped resource (Namespace) is
// refused — pull reconcile applies namespaced resources only.
func TestReconcileRejectsClusterScoped(t *testing.T) {
	dyn := newFakeDyn()
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)
	desired := protocol.DesiredStateResponsePayload{
		Manifests: []protocol.DesiredManifest{{
			Name:      "ns-obj",
			Namespace: "astronomer-system",
			Content:   "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: astronomer-system\n",
		}},
	}
	status := h.Reconcile(context.Background(), desired)
	if status.Success {
		t.Fatalf("expected failure for cluster-scoped resource")
	}
}

// TestPruneRemovesDroppedManagedObject: prune removes a managed object that was
// dropped from the desired set, but never an unmanaged object and never one
// outside the astronomer-* namespaces.
func TestPruneRemovesDroppedManagedObject(t *testing.T) {
	// Pre-seed:
	//  - astronomer-system/keep      managed, stays in desired set -> kept
	//  - astronomer-system/stale     managed, dropped from desired  -> PRUNED
	//  - astronomer-system/unmanaged unmanaged                      -> kept
	//  - kube-system/outside         managed but outside owned ns   -> kept
	dyn := newFakeDyn(
		existingManagedCM("astronomer-system", "keep"),
		existingManagedCM("astronomer-system", "stale"),
		existingUnmanagedCM("astronomer-system", "unmanaged"),
		existingManagedCM("kube-system", "outside"),
	)
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)

	// Desired set only contains "keep".
	desired := protocol.DesiredStateResponsePayload{
		Manifests: []protocol.DesiredManifest{cmManifest("astronomer-system", "keep")},
	}
	status := h.Reconcile(context.Background(), desired)
	if !status.Success {
		t.Fatalf("apply failed: %+v", status)
	}
	if status.Pruned != 1 {
		t.Fatalf("expected exactly 1 pruned, got %d (%+v)", status.Pruned, status.Results)
	}

	ctx := context.Background()

	// stale must be GONE.
	if _, err := getCM(t, dyn, "astronomer-system", "stale"); !apierrors.IsNotFound(err) {
		t.Fatalf("stale managed object should have been pruned; err=%v", err)
	}
	// keep must remain.
	if _, err := getCM(t, dyn, "astronomer-system", "keep"); err != nil {
		t.Fatalf("desired object was wrongly pruned: %v", err)
	}
	// unmanaged must remain (never touched).
	if _, err := getCM(t, dyn, "astronomer-system", "unmanaged"); err != nil {
		t.Fatalf("unmanaged object must NOT be pruned: %v", err)
	}
	// outside owned namespaces must remain even though managed-labeled.
	if _, err := dyn.Resource(configMapGVR).Namespace("kube-system").Get(ctx, "outside", metav1.GetOptions{}); err != nil {
		t.Fatalf("managed object outside owned namespaces must NOT be pruned: %v", err)
	}
}

// TestApplyResponseSkipsOnServerError: a desired-state response carrying an
// Error must NOT apply anything.
func TestApplyResponseSkipsOnServerError(t *testing.T) {
	dyn := newFakeDyn()
	h := newReconcileHandler(dyn, staticResolver(t), "cluster-1", nil)

	payload, _ := json.Marshal(protocol.DesiredStateResponsePayload{
		Manifests: []protocol.DesiredManifest{cmManifest("astronomer-system", "should-not-apply")},
	})
	msg := &protocol.Message{Type: protocol.MsgDesiredStateResponse, Payload: payload, Error: "render failed"}

	var sent []*protocol.Message
	sendFn := func(m *protocol.Message) error { sent = append(sent, m); return nil }
	h.applyResponse(context.Background(), msg, sendFn)

	if _, err := getCM(t, dyn, "astronomer-system", "should-not-apply"); !apierrors.IsNotFound(err) {
		t.Fatalf("nothing should be applied on server error; err=%v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("no status should be sent when skipping on server error, got %d", len(sent))
	}
}
