package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// batchAuditStore implements the base store plus the optional batch surface so
// PersistAuditEvents should write the whole valid set in one multi-row INSERT
// instead of one round-trip per event.
type batchAuditStore struct {
	batches [][]sqlc.InsertApiserverAuditEventParams
	perRow  []sqlc.InsertApiserverAuditEventParams
}

func (s *batchAuditStore) InsertApiserverAuditEvent(_ context.Context, arg sqlc.InsertApiserverAuditEventParams) error {
	s.perRow = append(s.perRow, arg)
	return nil
}

func (s *batchAuditStore) InsertApiserverAuditEventsBatch(_ context.Context, events []sqlc.InsertApiserverAuditEventParams) error {
	// Copy so later mutation of the caller's slice can't alter what we assert on.
	batch := append([]sqlc.InsertApiserverAuditEventParams(nil), events...)
	s.batches = append(s.batches, batch)
	return nil
}

func (s *batchAuditStore) ListApiserverAuditEventsByCluster(context.Context, sqlc.ListApiserverAuditEventsByClusterParams) ([]sqlc.ApiserverAuditEvent, error) {
	return nil, nil
}

func (s *batchAuditStore) CountApiserverAuditEventsByCluster(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func TestPersistAuditEvents_UsesBatchInsert(t *testing.T) {
	store := &batchAuditStore{}
	h := NewApiserverAuditHandler(store)
	clusterID := uuid.New()

	events := []json.RawMessage{
		json.RawMessage(`{"auditID":"a1","verb":"delete"}`),
		json.RawMessage(`{"auditID":"a2","verb":"get"}`),
		json.RawMessage(`{"auditID":"a3","verb":"list"}`),
		json.RawMessage(`{"verb":"watch"}`), // no auditID -> skipped, not batched
	}

	accepted, skipped, err := h.PersistAuditEvents(context.Background(), clusterID, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted != 3 || skipped != 1 {
		t.Fatalf("expected accepted=3 skipped=1, got accepted=%d skipped=%d", accepted, skipped)
	}
	if len(store.perRow) != 0 {
		t.Errorf("batch-capable store must not receive per-row inserts, got %d", len(store.perRow))
	}
	if len(store.batches) != 1 {
		t.Fatalf("expected exactly 1 batch insert call, got %d", len(store.batches))
	}
	if len(store.batches[0]) != 3 {
		t.Errorf("expected 3 rows in the single batch, got %d", len(store.batches[0]))
	}
	for _, row := range store.batches[0] {
		if row.ClusterID != clusterID {
			t.Errorf("batched row has wrong cluster_id: %s", row.ClusterID)
		}
	}
}

// cappedAuditStore adds the optional bounded-count surface. List must prefer it
// over the exact COUNT(*) so the total for this high-volume view doesn't turn
// into an ever-slower full scan.
type cappedAuditStore struct {
	*fakeApiserverAuditStore
	cappedCalls int
	cappedArg   int32
	exactCalls  int
}

func (s *cappedAuditStore) CountApiserverAuditEventsByClusterCapped(_ context.Context, _ uuid.UUID, maxRows int32) (int64, error) {
	s.cappedCalls++
	s.cappedArg = maxRows
	return 42, nil
}

func (s *cappedAuditStore) CountApiserverAuditEventsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	s.exactCalls++
	return 999999, nil
}

func TestApiserverAuditList_PrefersCappedCount(t *testing.T) {
	store := &cappedAuditStore{fakeApiserverAuditStore: &fakeApiserverAuditStore{}}
	h := NewApiserverAuditHandler(store)
	clusterID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/apiserver-audit/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.cappedCalls != 1 {
		t.Errorf("expected List to use the capped count once, got %d", store.cappedCalls)
	}
	if store.exactCalls != 0 {
		t.Errorf("expected List to skip the exact COUNT(*), got %d calls", store.exactCalls)
	}
	if store.cappedArg != auditCountCap {
		t.Errorf("expected cap=%d passed to capped count, got %d", auditCountCap, store.cappedArg)
	}
}
