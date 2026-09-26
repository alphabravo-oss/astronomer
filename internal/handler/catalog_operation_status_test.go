package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/catalogapp"
	"github.com/google/uuid"
)

type operationStatusQuerier struct {
	*minimalCatalogQuerier
	events []sqlc.CatalogOperationEvent
	err    error
}

func (q *operationStatusQuerier) ListCatalogOperationEvents(_ context.Context, _ uuid.UUID) ([]sqlc.CatalogOperationEvent, error) {
	return q.events, q.err
}

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
		if resp["status"] != "running" || resp["deliveryPhase"] != "unknown" || resp["deliveryObservationError"] == "" || resp["deliveryObservedAt"] != nil {
			t.Fatalf("unavailable observation claimed success %+v", resp)
		}
	}
}

func TestCatalogOperationListProjectionUsesDeliveryOutcome(t *testing.T) {
	target, rollout := uuid.New(), uuid.New()
	detail, err := json.Marshal(map[string]any{"targetId": target.String(), "rolloutId": rollout.String()})
	if err != nil {
		t.Fatal(err)
	}
	q := &operationStatusQuerier{
		minimalCatalogQuerier: &minimalCatalogQuerier{},
		events:                []sqlc.CatalogOperationEvent{{Stage: "rollout", Detail: detail}},
	}
	h := &CatalogHandler{queries: q, delivery: &operationDeliveryObserver{status: catalogapp.Status{Phase: "pending"}}}
	op := sqlc.CatalogOperation{ID: uuid.New(), TargetType: "installed_chart", OperationType: "install", Status: "completed"}
	resp := h.catalogOperationResponseWithDelivery(context.Background(), op, false)
	if resp["status"] != "running" || resp["journalStatus"] != "completed" || resp["deliveryPhase"] != "pending" {
		t.Fatalf("list projection = %+v", resp)
	}
	if _, included := resp["events"]; included {
		t.Fatal("compact list projection included operation events")
	}
}

func TestCatalogOperationFailureSurfacesReconcilerMessage(t *testing.T) {
	target, rollout := uuid.New(), uuid.New()
	observer := &operationDeliveryObserver{status: catalogapp.Status{
		Phase: "failed", LastErrorCode: "reconciler_stalled", LastMessage: "Helm install failed because host port 9100 is occupied",
	}}
	h := &CatalogHandler{delivery: observer}
	op := sqlc.CatalogOperation{TargetType: "installed_chart", OperationType: "install", Status: "completed"}
	resp := catalogOperationResponse(op)
	resp["events"] = []map[string]any{{"stage": "rollout", "detail": map[string]any{"targetId": target.String(), "rolloutId": rollout.String()}}}
	h.enrichCatalogOperationDeliveryStatus(context.Background(), op, resp)
	if resp["status"] != "failed" || resp["deliveryMessage"] != observer.status.LastMessage {
		t.Fatalf("failure message was not projected: %+v", resp)
	}
	events := resp["events"].([]map[string]any)
	if got, _ := events[len(events)-1]["message"].(string); !strings.Contains(got, "9100") {
		t.Fatalf("event message = %q, want actionable reconciler detail", got)
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
