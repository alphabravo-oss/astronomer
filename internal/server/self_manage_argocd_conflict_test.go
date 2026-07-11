package server

import (
	"context"
	"testing"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Argo's application controller writes status on its own cadence, so the
// Get→Update in ensureSelfManagedAstronomerApplication races the controller and
// can lose to a Conflict. The fix wraps it in retry.RetryOnConflict; this test
// makes the first Update return a Conflict and asserts the reconcile still
// succeeds (retried) rather than surfacing the Conflict.
func TestEnsureSelfManagedAstronomerApplication_RetriesOnConflict(t *testing.T) {
	existing := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]any{
				"name":            localArgoApplicationName,
				"namespace":       localArgoNamespace,
				"resourceVersion": "1",
			},
			"spec": map[string]any{},
		},
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{argocdApplicationGVR: "ApplicationList"},
		existing,
	)

	var updates int
	dyn.PrependReactor("update", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			// One Conflict, then let the default reactor handle the retry.
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "argoproj.io", Resource: "applications"},
				localArgoApplicationName,
				context.DeadlineExceeded,
			)
		}
		return false, nil, nil
	})

	cluster := sqlc.Cluster{ID: uuid.New(), Name: "local", ApiServerUrl: "https://kubernetes.default.svc"}

	if err := ensureSelfManagedAstronomerApplication(context.Background(), dyn, cluster, "server:\n  replicaCount: 1\n"); err != nil {
		t.Fatalf("ensureSelfManagedAstronomerApplication returned %v; want nil (Conflict should be retried)", err)
	}
	if updates < 2 {
		t.Fatalf("Update attempts = %d, want >= 2 (first Conflict should trigger a retry)", updates)
	}
}
