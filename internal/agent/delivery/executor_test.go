package delivery

import (
	"context"
	"encoding/json"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func newExecutorFixture(t *testing.T) (*Executor, *fake.FakeDynamicClient) {
	t.Helper()
	listKinds := make(map[schema.GroupVersionResource]string, len(deliveryResources))
	for gvk, mapping := range deliveryResources {
		listKinds[mapping.resource] = gvk.Kind + "List"
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
	client.PrependReactor("patch", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		object := &unstructured.Unstructured{}
		if err := json.Unmarshal(patch.GetPatch(), &object.Object); err != nil {
			return true, nil, err
		}
		resource := action.GetResource()
		existing, err := client.Tracker().Get(resource, action.GetNamespace(), object.GetName())
		if apierrors.IsNotFound(err) {
			if err := client.Tracker().Create(resource, object, action.GetNamespace()); err != nil {
				return true, nil, err
			}
			return true, object, nil
		}
		if err != nil {
			return true, nil, err
		}
		object.SetResourceVersion(existing.(metav1.Object).GetResourceVersion())
		if err := client.Tracker().Update(resource, object, action.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, object, nil
	})
	executor, err := NewExecutor(client)
	if err != nil {
		t.Fatal(err)
	}
	return executor, client
}

func TestExecutorApplyPruneAndFencedDeletion(t *testing.T) {
	ctx := context.Background()
	executor, _ := newExecutorFixture(t)
	assignment := gitAssignment()
	materialization, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Apply(ctx, materialization); err != nil {
		t.Fatal(err)
	}
	existing, err := executor.Existing(ctx, assignment, materialization)
	if err != nil {
		t.Fatal(err)
	}
	if len(existing) != len(materialization.Objects)-1 {
		t.Fatalf("got %d existing deployment objects, want %d", len(existing), len(materialization.Objects)-1)
	}

	rotated := assignment
	rotated.Generation++
	rotated.SpecDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	rotated.Credential = nil
	rotated.Source.CredentialSecret = ""
	rotated.Source.TrustSecret = ""
	rotated.Source.Verify = nil
	next, err := BuildAssignment(rotated, testCapabilities(), ValidationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Apply(ctx, next); err != nil {
		t.Fatal(err)
	}
	existing, err = executor.Existing(ctx, rotated, next)
	if err != nil {
		t.Fatal(err)
	}
	prune, err := PlanPrune(rotated, next, existing, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(prune) != 2 {
		t.Fatalf("expected stale authentication and trust Secrets, got %#v", prune)
	}
	if err := executor.Prune(ctx, prune); err != nil {
		t.Fatal(err)
	}

	accepted := AcceptAssignment(rotated, next)
	tombstone := protocol.DeliveryDeletionV2{DeploymentID: rotated.DeploymentID, Generation: rotated.Generation, SpecDigest: rotated.SpecDigest}
	removed, err := executor.BeginDeletion(ctx, accepted.boundaryAssignment(), tombstone, accepted.materializationBoundary(), accepted.Objects)
	if err != nil || removed {
		t.Fatalf("first deletion stage: removed=%v err=%v", removed, err)
	}
	removed, err = executor.BeginDeletion(ctx, accepted.boundaryAssignment(), tombstone, accepted.materializationBoundary(), accepted.Objects)
	if err != nil || removed {
		t.Fatalf("second deletion stage: removed=%v err=%v", removed, err)
	}
	removed, err = executor.BeginDeletion(ctx, accepted.boundaryAssignment(), tombstone, accepted.materializationBoundary(), accepted.Objects)
	if err != nil || !removed {
		t.Fatalf("final deletion stage: removed=%v err=%v", removed, err)
	}
}

func TestExecutorRefusesUnknownKindAndPartialApplyPrune(t *testing.T) {
	executor, _ := newExecutorFixture(t)
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingWebhookConfiguration",
		"metadata": map[string]any{"name": "escape"},
	}}
	if err := executor.Apply(context.Background(), Materialization{Objects: []*unstructured.Unstructured{object}}); err == nil {
		t.Fatal("expected unknown kind to be refused")
	}
	assignment := gitAssignment()
	materialization, _ := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
	if _, err := PlanPrune(assignment, materialization, nil, false); err == nil {
		t.Fatal("expected prune after partial apply to be refused")
	}
}
