package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/catalogapp"
	"github.com/google/uuid"
)

type operationDeliveryObserver struct {
	CatalogApplicationDelivery
	target, rollout uuid.UUID
	status          catalogapp.Status
	err             error
	calls           int
	deletion        bool
}

func (f *operationDeliveryObserver) RolloutStatus(_ context.Context, target, rollout uuid.UUID) (catalogapp.Status, error) {
	f.target = target
	f.rollout = rollout
	f.calls++
	return f.status, f.err
}
func (f *operationDeliveryObserver) DeletionStatus(_ context.Context, target uuid.UUID) (catalogapp.Status, error) {
	f.target = target
	f.deletion = true
	f.calls++
	return f.status, f.err
}

func TestCatalogOperationOutcomeUsesOwnRolloutAndPreservesJournalFailure(t *testing.T) {
	target, rollout := uuid.New(), uuid.New()
	for _, tc := range []struct {
		journal, phase, want string
		calls                int
	}{{"failed", "ready", "failed", 0}, {"pending", "ready", "pending", 0}, {"running", "ready", "running", 0}, {"completed", "ready", "completed", 1}, {"completed", "failed", "failed", 1}, {"completed", "pending", "running", 1}} {
		t.Run(tc.journal+tc.phase, func(t *testing.T) {
			observer := &operationDeliveryObserver{status: catalogapp.Status{Phase: tc.phase}}
			h := &CatalogHandler{delivery: observer}
			op := sqlc.CatalogOperation{TargetType: "installed_chart", TargetKey: uuid.NewString(), OperationType: "upgrade", Status: tc.journal}
			resp := catalogOperationResponse(op)
			resp["events"] = []map[string]any{{"stage": "rollout", "detail": map[string]any{"targetId": target.String(), "rolloutId": rollout.String()}}}
			h.enrichCatalogOperationDeliveryStatus(context.Background(), op, resp)
			if resp["status"] != tc.want || observer.calls != tc.calls || resp["journalStatus"] != tc.journal {
				t.Fatalf("wrong projection: %+v observer=%+v", resp, observer)
			}
			if observer.calls > 0 && (observer.target != target || observer.rollout != rollout) {
				t.Fatal("queried a different operation outcome")
			}
		})
	}
}

func TestCatalogOperationOutcomeUnavailableIsExplicit(t *testing.T) {
	for _, hasEvent := range []bool{false, true} {
		observer := &operationDeliveryObserver{err: errors.New("read unavailable")}
		h := &CatalogHandler{delivery: observer}
		op := sqlc.CatalogOperation{TargetType: "installed_chart", OperationType: "upgrade", Status: "completed"}
		resp := catalogOperationResponse(op)
		if hasEvent {
			resp["events"] = []map[string]any{{"stage": "rollout", "detail": map[string]any{"targetId": uuid.NewString(), "rolloutId": uuid.NewString()}}}
		}
		h.enrichCatalogOperationDeliveryStatus(context.Background(), op, resp)
		if resp["deliveryPhase"] != "unknown" || resp["deliveryObservationError"] == "" || resp["deliveryObservedAt"] != nil {
			t.Fatalf("unavailable observation claimed success %+v", resp)
		}
	}
}

func TestCatalogUninstallOutcomeUsesDeletionIdentity(t *testing.T) {
	target := uuid.New()
	for _, phase := range []string{"pending", "removed"} {
		observer := &operationDeliveryObserver{status: catalogapp.Status{Phase: phase}}
		h := &CatalogHandler{delivery: observer}
		op := sqlc.CatalogOperation{TargetType: "installed_chart", OperationType: "uninstall", Status: "completed"}
		resp := catalogOperationResponse(op)
		resp["events"] = []map[string]any{{"stage": "deletion", "detail": map[string]any{"targetId": target.String()}}}
		h.enrichCatalogOperationDeliveryStatus(context.Background(), op, resp)
		expected := "running"
		if phase == "removed" {
			expected = "completed"
		}
		if !observer.deletion || observer.target != target || resp["status"] != expected {
			t.Fatalf("uninstall borrowed ready deployment: %+v %+v", resp, observer)
		}
	}
}
