package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeCRDOwnershipDriftQuerier struct {
	rows      []sqlc.FleetOwnership
	condition []sqlc.UpsertClusterConditionParams
}

func (f *fakeCRDOwnershipDriftQuerier) ListCRDOwnedClusters(context.Context, int32) ([]sqlc.FleetOwnership, error) {
	return f.rows, nil
}

func (f *fakeCRDOwnershipDriftQuerier) UpsertClusterCondition(_ context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	f.condition = append(f.condition, arg)
	return sqlc.ClusterCondition{ClusterID: arg.ClusterID, Type: arg.Type, Status: arg.Status, Reason: arg.Reason, Message: arg.Message}, nil
}

func TestCRDOwnershipDriftCheckMarksMissingClusterCR(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeCRDOwnershipDriftQuerier{rows: []sqlc.FleetOwnership{crdOwnedClusterRow(clusterID, "platform", "prod-east")}}
	deps := CRDOwnershipDriftDeps{
		Queries: q,
		Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}

	if err := checkCRDOwnedClusterRef(context.Background(), deps, q.rows[0]); err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(q.condition) != 1 {
		t.Fatalf("conditions = %d, want 1", len(q.condition))
	}
	got := q.condition[0]
	if got.Type != ConditionCRDOwnership || got.Status != "False" || got.Reason != "ExternalRefMissing" {
		t.Fatalf("condition = %+v, want missing CRD condition", got)
	}
}

func TestCRDOwnershipDriftCheckMarksPresentClusterCR(t *testing.T) {
	clusterID := uuid.New()
	row := crdOwnedClusterRow(clusterID, "platform", "prod-east")
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   crd.GroupVersion.Group,
		Version: crd.GroupVersion.Version,
		Kind:    "Cluster",
	})
	obj.SetNamespace("platform")
	obj.SetName("prod-east")
	q := &fakeCRDOwnershipDriftQuerier{rows: []sqlc.FleetOwnership{row}}
	deps := CRDOwnershipDriftDeps{
		Queries: q,
		Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj),
	}

	if err := checkCRDOwnedClusterRef(context.Background(), deps, row); err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(q.condition) != 1 {
		t.Fatalf("conditions = %d, want 1", len(q.condition))
	}
	got := q.condition[0]
	if got.Type != ConditionCRDOwnership || got.Status != "True" || got.Reason != "ExternalRefFound" {
		t.Fatalf("condition = %+v, want found CRD condition", got)
	}
}

func crdOwnedClusterRow(id uuid.UUID, namespace, name string) sqlc.FleetOwnership {
	return sqlc.FleetOwnership{
		ID:                    id,
		ManagedBy:             "crd",
		ExternalRefApiVersion: crd.GroupVersion.String(),
		ExternalRefKind:       "Cluster",
		ExternalRefNamespace:  namespace,
		ExternalRefName:       name,
		ObservedGeneration:    1,
	}
}

var _ = metav1.NamespaceDefault
