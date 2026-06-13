package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type fakeOwnershipQuerier struct {
	row        sqlc.FleetOwnership
	err        error
	clusterSet []sqlc.SetClusterOwnershipParams
	projectSet []sqlc.SetProjectOwnershipParams
}

func (f *fakeOwnershipQuerier) GetClusterOwnership(context.Context, uuid.UUID) (sqlc.FleetOwnership, error) {
	return f.row, f.err
}

func (f *fakeOwnershipQuerier) GetProjectOwnership(context.Context, uuid.UUID) (sqlc.FleetOwnership, error) {
	return f.row, f.err
}

func (f *fakeOwnershipQuerier) SetClusterOwnership(_ context.Context, arg sqlc.SetClusterOwnershipParams) (sqlc.FleetOwnership, error) {
	f.clusterSet = append(f.clusterSet, arg)
	f.row.ID = arg.ID
	f.row.ManagedBy = arg.ManagedBy
	f.row.ExternalRefApiVersion = arg.ExternalRefApiVersion
	f.row.ExternalRefKind = arg.ExternalRefKind
	f.row.ExternalRefNamespace = arg.ExternalRefNamespace
	f.row.ExternalRefName = arg.ExternalRefName
	f.row.ObservedGeneration = arg.ObservedGeneration
	return f.row, nil
}

func (f *fakeOwnershipQuerier) SetProjectOwnership(_ context.Context, arg sqlc.SetProjectOwnershipParams) (sqlc.FleetOwnership, error) {
	f.projectSet = append(f.projectSet, arg)
	f.row.ID = arg.ID
	f.row.ManagedBy = arg.ManagedBy
	f.row.ExternalRefApiVersion = arg.ExternalRefApiVersion
	f.row.ExternalRefKind = arg.ExternalRefKind
	f.row.ExternalRefNamespace = arg.ExternalRefNamespace
	f.row.ExternalRefName = arg.ExternalRefName
	f.row.ObservedGeneration = arg.ObservedGeneration
	return f.row, nil
}

func TestClusterUpdateBlockedByOwnership_CRDOwned(t *testing.T) {
	msg, err := clusterUpdateBlockedByOwnership(context.Background(), &fakeOwnershipQuerier{row: crdOwnedFleetRow("Cluster", "prod-us-east")}, uuid.New())
	if err != nil {
		t.Fatalf("clusterUpdateBlockedByOwnership: %v", err)
	}
	if msg == "" || !strings.Contains(msg, "managed by CRD") || !strings.Contains(msg, "management.astronomer.io/v1alpha1/Cluster astronomer-mgmt/prod-us-east") {
		t.Fatalf("unexpected conflict message: %q", msg)
	}
}

func TestProjectUpdateBlockedByOwnership_CRDOwned(t *testing.T) {
	msg, err := projectUpdateBlockedByOwnership(context.Background(), &fakeOwnershipQuerier{row: crdOwnedFleetRow("Project", "platform")}, uuid.New())
	if err != nil {
		t.Fatalf("projectUpdateBlockedByOwnership: %v", err)
	}
	if msg == "" || !strings.Contains(msg, "managed by CRD") || !strings.Contains(msg, "management.astronomer.io/v1alpha1/Project astronomer-mgmt/platform") {
		t.Fatalf("unexpected conflict message: %q", msg)
	}
}

func TestUpdateBlockedByOwnership_NonCRDOrNoQuerier(t *testing.T) {
	apiOwned := &fakeOwnershipQuerier{row: sqlc.FleetOwnership{ManagedBy: "api"}}
	if msg, err := clusterUpdateBlockedByOwnership(context.Background(), apiOwned, uuid.New()); err != nil || msg != "" {
		t.Fatalf("api-owned cluster got msg=%q err=%v", msg, err)
	}
	if msg, err := projectUpdateBlockedByOwnership(context.Background(), apiOwned, uuid.New()); err != nil || msg != "" {
		t.Fatalf("api-owned project got msg=%q err=%v", msg, err)
	}
	if msg, err := clusterUpdateBlockedByOwnership(context.Background(), struct{}{}, uuid.New()); err != nil || msg != "" {
		t.Fatalf("missing cluster ownership querier got msg=%q err=%v", msg, err)
	}
	if msg, err := projectUpdateBlockedByOwnership(context.Background(), struct{}{}, uuid.New()); err != nil || msg != "" {
		t.Fatalf("missing project ownership querier got msg=%q err=%v", msg, err)
	}
}

func TestTransferClusterOwnershipToAPI_CRDOwned(t *testing.T) {
	id := uuid.New()
	q := &fakeOwnershipQuerier{row: crdOwnedFleetRow("Cluster", "prod-us-east")}

	prev, updated, transferred, err := transferClusterOwnershipToAPI(context.Background(), q, id)
	if err != nil {
		t.Fatalf("transferClusterOwnershipToAPI: %v", err)
	}
	if !transferred {
		t.Fatal("expected transfer to be true")
	}
	if prev.ManagedBy != "crd" || updated.ManagedBy != "api" {
		t.Fatalf("managed_by prev=%q updated=%q", prev.ManagedBy, updated.ManagedBy)
	}
	if updated.ExternalRefName != "" || updated.ExternalRefKind != "" || updated.ObservedGeneration != 0 {
		t.Fatalf("ownership metadata not cleared: %+v", updated)
	}
	if len(q.clusterSet) != 1 {
		t.Fatalf("cluster ownership set calls = %d, want 1", len(q.clusterSet))
	}
}

func TestTransferProjectOwnershipToAPI_CRDOwned(t *testing.T) {
	id := uuid.New()
	q := &fakeOwnershipQuerier{row: crdOwnedFleetRow("Project", "platform")}

	prev, updated, transferred, err := transferProjectOwnershipToAPI(context.Background(), q, id)
	if err != nil {
		t.Fatalf("transferProjectOwnershipToAPI: %v", err)
	}
	if !transferred {
		t.Fatal("expected transfer to be true")
	}
	if prev.ManagedBy != "crd" || updated.ManagedBy != "api" {
		t.Fatalf("managed_by prev=%q updated=%q", prev.ManagedBy, updated.ManagedBy)
	}
	if updated.ExternalRefName != "" || updated.ExternalRefKind != "" || updated.ObservedGeneration != 0 {
		t.Fatalf("ownership metadata not cleared: %+v", updated)
	}
	if len(q.projectSet) != 1 {
		t.Fatalf("project ownership set calls = %d, want 1", len(q.projectSet))
	}
}

func TestTransferOwnershipToAPI_AlreadyAPIOwnedIsIdempotent(t *testing.T) {
	id := uuid.New()
	q := &fakeOwnershipQuerier{row: sqlc.FleetOwnership{ID: id, ManagedBy: "api"}}
	_, updated, transferred, err := transferClusterOwnershipToAPI(context.Background(), q, id)
	if err != nil {
		t.Fatalf("transferClusterOwnershipToAPI: %v", err)
	}
	if transferred || updated.ManagedBy != "api" || len(q.clusterSet) != 0 {
		t.Fatalf("expected idempotent no-op, transferred=%t updated=%+v calls=%d", transferred, updated, len(q.clusterSet))
	}
}

func TestTransferOwnershipToAPI_RejectsSystemOwned(t *testing.T) {
	id := uuid.New()
	q := &fakeOwnershipQuerier{row: sqlc.FleetOwnership{ID: id, ManagedBy: "system"}}
	if _, _, _, err := transferClusterOwnershipToAPI(context.Background(), q, id); err == nil {
		t.Fatal("expected cluster system-owned transfer to fail")
	}
	if _, _, _, err := transferProjectOwnershipToAPI(context.Background(), q, id); err == nil {
		t.Fatal("expected project system-owned transfer to fail")
	}
	if len(q.clusterSet) != 0 || len(q.projectSet) != 0 {
		t.Fatalf("unexpected set calls: cluster=%d project=%d", len(q.clusterSet), len(q.projectSet))
	}
}

func crdOwnedFleetRow(kind, name string) sqlc.FleetOwnership {
	return sqlc.FleetOwnership{
		ManagedBy:             "crd",
		ExternalRefApiVersion: "management.astronomer.io/v1alpha1",
		ExternalRefKind:       kind,
		ExternalRefNamespace:  "astronomer-mgmt",
		ExternalRefName:       name,
	}
}
