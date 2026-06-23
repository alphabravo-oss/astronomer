package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeApiserverAuditStore struct {
	inserted []sqlc.InsertApiserverAuditEventParams
}

func (f *fakeApiserverAuditStore) InsertApiserverAuditEvent(_ context.Context, arg sqlc.InsertApiserverAuditEventParams) error {
	f.inserted = append(f.inserted, arg)
	return nil
}

func (f *fakeApiserverAuditStore) ListApiserverAuditEventsByCluster(_ context.Context, _ sqlc.ListApiserverAuditEventsByClusterParams) ([]sqlc.ApiserverAuditEvent, error) {
	return nil, nil
}

func (f *fakeApiserverAuditStore) CountApiserverAuditEventsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

// Ingest must promote the audit.k8s.io fields it indexes, preserve the raw
// event, and skip events lacking an auditID — that skip-vs-accept split is
// the load-bearing logic.
func TestApiserverAuditIngest(t *testing.T) {
	store := &fakeApiserverAuditStore{}
	h := NewApiserverAuditHandler(store)
	clusterID := uuid.New()

	body := map[string]any{
		"events": []map[string]any{
			{
				"auditID":        "abc-123",
				"stage":          "ResponseComplete",
				"verb":           "delete",
				"user":           map[string]any{"username": "alice"},
				"objectRef":      map[string]any{"resource": "secrets", "namespace": "kube-system"},
				"responseStatus": map[string]any{"code": 200},
				"stageTimestamp": "2026-06-22T10:00:00Z",
			},
			{ // no auditID -> skipped
				"verb": "get",
			},
		},
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/apiserver-audit/", bytes.NewReader(raw))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.Ingest(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var wrapped struct {
		Data ingestApiserverAuditResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := wrapped.Data
	if resp.Accepted != 1 || resp.Skipped != 1 {
		t.Fatalf("expected accepted=1 skipped=1, got %+v", resp)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(store.inserted))
	}
	got := store.inserted[0]
	if got.ClusterID != clusterID {
		t.Errorf("cluster_id mismatch: %s != %s", got.ClusterID, clusterID)
	}
	if got.AuditID != "abc-123" || got.Verb != "delete" || got.Username != "alice" ||
		got.Resource != "secrets" || got.Namespace != "kube-system" || got.StatusCode != 200 {
		t.Errorf("promoted fields wrong: %+v", got)
	}
	if got.EventTime.Year() != 2026 || got.EventTime.Month() != 6 {
		t.Errorf("event_time not parsed from stageTimestamp: %v", got.EventTime)
	}
}
