package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type targetEmptyArrayQueries struct {
	TargetQueries
	TargetMutationTx
	t *testing.T
}

func (q *targetEmptyArrayQueries) GetComponentBundleVersion(context.Context, sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	return sqlc.ComponentBundleVersion{State: "ready", VerificationStatus: "verified", Renderer: "helm", Scope: "project"}, nil
}

func (q *targetEmptyArrayQueries) CreateDeliveryTarget(_ context.Context, p sqlc.CreateDeliveryTargetParams) (sqlc.DeliveryTarget, error) {
	if p.OverrideSetIds == nil {
		q.t.Fatal("nil override IDs would violate the PostgreSQL NOT NULL constraint")
	}
	return sqlc.DeliveryTarget{ID: uuid.New(), ProjectID: p.ProjectID, Name: p.Name,
		BundleVersionID: p.BundleVersionID, Placement: p.Placement, RolloutPolicy: p.RolloutPolicy,
		ReconciliationPolicy: p.ReconciliationPolicy, MaintenanceWindowPolicy: p.MaintenanceWindowPolicy,
		Overrides: p.Overrides, OverrideSetIds: p.OverrideSetIds, Generation: 1, ResourceVersion: 1}, nil
}

func (q *targetEmptyArrayQueries) UpdateDeliveryTargetCAS(context.Context, sqlc.UpdateDeliveryTargetCASParams) (sqlc.DeliveryTarget, error) {
	panic("unexpected UpdateDeliveryTargetCAS")
}

func (q *targetEmptyArrayQueries) RequestDeliveryTargetDeletionCAS(context.Context, sqlc.RequestDeliveryTargetDeletionCASParams) (sqlc.RequestDeliveryTargetDeletionCASRow, error) {
	panic("unexpected RequestDeliveryTargetDeletionCAS")
}

func (q *targetEmptyArrayQueries) MarkDeliveryTargetOrphaned(context.Context, sqlc.MarkDeliveryTargetOrphanedParams) (sqlc.MarkDeliveryTargetOrphanedRow, error) {
	panic("unexpected MarkDeliveryTargetOrphaned")
}

func (q *targetEmptyArrayQueries) UpsertAuditOutbox(_ context.Context, p sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{ID: p.ID, Action: p.Action}, nil
}

func TestCreateTargetAcceptsOmittedAndEmptyOverrideSets(t *testing.T) {
	for _, suffix := range []string{"", `,"override_set_ids":[]`, `,"override_set_ids":null`} {
		t.Run(suffix, func(t *testing.T) {
			q := &targetEmptyArrayQueries{t: t}
			h := NewTargetHandler(q, nil, nil)
			h.SetRunTx(func(_ context.Context, fn func(TargetMutationTx) error) error { return fn(q) })
			payload := `{"project_id":"` + uuid.NewString() + `","name":"scanner","bundle_version_id":"` + uuid.NewString() + `","placement":{"all_clusters":true},"reconciliation_policy":{"interval":"5m","retry_interval":"30s","timeout":"10m","drift":"repair"}` + suffix + `}`
			r := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/targets/", strings.NewReader(payload))
			w := httptest.NewRecorder()
			h.Create(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var body struct {
				Data struct {
					IDs json.RawMessage `json:"override_set_ids"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || string(body.Data.IDs) != "[]" {
				t.Fatalf("response must contain an empty array: %s (error=%v)", w.Body.String(), err)
			}
		})
	}
}

func TestTargetPatchPreservesEmptyAndClearsExistingOverrideSets(t *testing.T) {
	for _, clear := range []bool{false, true} {
		row := sqlc.DeliveryTarget{Name: "scanner", BundleVersionID: uuid.New(),
			Placement: json.RawMessage(`{"all_clusters":true}`), RolloutPolicy: json.RawMessage(`{}`),
			ReconciliationPolicy:    json.RawMessage(`{"interval":"5m","retry_interval":"30s","timeout":"10m","drift":"repair"}`),
			MaintenanceWindowPolicy: json.RawMessage(`{}`), Overrides: json.RawMessage(`{}`), OverrideSetIds: []uuid.UUID{}}
		patch := updateTargetRequest{}
		if clear {
			row.OverrideSetIds = []uuid.UUID{uuid.New()}
			empty := []uuid.UUID{}
			patch.OverrideSetIDs = &empty
		}
		merged, err := mergeTargetUpdate(row, patch)
		if err != nil || merged.OverrideSetIDs == nil || len(merged.OverrideSetIDs) != 0 {
			t.Fatalf("clear=%v override IDs=%v error=%v", clear, merged.OverrideSetIDs, err)
		}
	}
}
