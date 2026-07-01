package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// CountOutputsByCluster / CountPipelinesByCluster complete the loggingFakeQuerier
// implementation of LoggingQuerier once the cluster-scoped count methods were
// added to the interface. They are defined here (rather than in the primary
// fake file) so this regression test owns the new behaviour it exercises.
func (q *loggingFakeQuerier) CountOutputsByCluster(_ context.Context, clusterID pgtype.UUID) (int64, error) {
	var n int64
	for _, o := range q.outputs {
		if o.ClusterID == clusterID {
			n++
		}
	}
	return n, nil
}

func (q *loggingFakeQuerier) CountPipelinesByCluster(_ context.Context, clusterID uuid.UUID) (int64, error) {
	var n int64
	for _, p := range q.pipelines {
		if p.ClusterID == clusterID {
			n++
		}
	}
	return n, nil
}

// TestListOutputs_ClusterScopedTotal verifies the cluster-scoped outputs list
// reports the per-cluster total, not a global count. Before the fix ListOutputs
// called CountLoggingOutputs (SELECT count(*) FROM logging_outputs), so a
// cluster with 2 outputs alongside a busy cluster with 40 reported total=42 and
// advertised phantom next pages.
func TestListOutputs_ClusterScopedTotal(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)

	clusterA := uuid.New()
	clusterB := uuid.New()
	seedOutput(t, q, clusterA)
	seedOutput(t, q, clusterA)
	for i := 0; i < 40; i++ {
		seedOutput(t, q, clusterB)
	}

	resp := doListOutputs(t, h, clusterA)
	if resp.Count != 2 {
		t.Fatalf("cluster A outputs total = %d, want 2 (global count leaked)", resp.Count)
	}
	if resp.Next != nil {
		t.Fatalf("cluster A outputs advertised a next page (%q) despite only 2 rows", *resp.Next)
	}
}

// TestListPipelines_ClusterScopedTotal is the pipeline analogue.
func TestListPipelines_ClusterScopedTotal(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)

	clusterA := uuid.New()
	clusterB := uuid.New()
	seedPipeline(t, q, clusterA)
	seedPipeline(t, q, clusterA)
	for i := 0; i < 40; i++ {
		seedPipeline(t, q, clusterB)
	}

	resp := doListPipelines(t, h, clusterA)
	if resp.Count != 2 {
		t.Fatalf("cluster A pipelines total = %d, want 2 (global count leaked)", resp.Count)
	}
	if resp.Next != nil {
		t.Fatalf("cluster A pipelines advertised a next page (%q) despite only 2 rows", *resp.Next)
	}
}

func seedOutput(t *testing.T, q *loggingFakeQuerier, clusterID uuid.UUID) {
	t.Helper()
	if _, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          "out-" + uuid.NewString(),
		OutputType:    "stdout",
		Configuration: json.RawMessage(`{}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed output: %v", err)
	}
}

func seedPipeline(t *testing.T, q *loggingFakeQuerier, clusterID uuid.UUID) {
	t.Helper()
	if _, err := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{
		Name:       "pipe-" + uuid.NewString(),
		ClusterID:  clusterID,
		Namespaces: json.RawMessage(`[]`),
		Labels:     json.RawMessage(`{}`),
		Filters:    json.RawMessage(`{}`),
		Enabled:    true,
	}); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}
}

func doListOutputs(t *testing.T, h *LoggingHandler, clusterID uuid.UUID) paginatedResponse {
	t.Helper()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/logging/outputs/", nil)
	req = req.WithContext(addRouteCtx(req.Context(), rc))
	rec := httptest.NewRecorder()
	h.ListOutputs(rec, req)
	return decodePaginated(t, rec)
}

func doListPipelines(t *testing.T, h *LoggingHandler, clusterID uuid.UUID) paginatedResponse {
	t.Helper()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/logging/pipelines/", nil)
	req = req.WithContext(addRouteCtx(req.Context(), rc))
	rec := httptest.NewRecorder()
	h.ListPipelines(rec, req)
	return decodePaginated(t, rec)
}

func decodePaginated(t *testing.T, rec *httptest.ResponseRecorder) paginatedResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp paginatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
