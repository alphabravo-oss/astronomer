package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// histVulnQuerier embeds the base stub (satisfying ImageVulnQuerier) and adds
// the ImageVulnHistoryQuerier methods so ReportHistory's type assertion passes.
type histVulnQuerier struct {
	*stubVulnQuerier
	snapshots []sqlc.ImageVulnerabilityReportSnapshot
}

func (q *histVulnQuerier) ListImageVulnerabilityHistoryForReport(_ context.Context, arg sqlc.ListImageVulnerabilityHistoryForReportParams) ([]sqlc.ImageVulnerabilityReportSnapshot, error) {
	// The real query is scoped only by report_id (globally unique) — the
	// cluster scoping is the handler's job, which is exactly what we test.
	out := []sqlc.ImageVulnerabilityReportSnapshot{}
	for _, s := range q.snapshots {
		if s.ReportID == arg.ReportID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (q *histVulnQuerier) ListImageVulnerabilityHistoryForCluster(_ context.Context, _ sqlc.ListImageVulnerabilityHistoryForClusterParams) ([]sqlc.ListImageVulnerabilityHistoryForClusterRow, error) {
	return nil, nil
}

func (q *histVulnQuerier) CompareImageVulnerabilitySnapshotsForCluster(_ context.Context, _ sqlc.CompareImageVulnerabilitySnapshotsForClusterParams) (sqlc.CompareImageVulnerabilitySnapshotsForClusterRow, error) {
	return sqlc.CompareImageVulnerabilitySnapshotsForClusterRow{}, nil
}

func reportHistoryReq(clusterID, reportID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/vulnerabilities/reports/"+reportID.String()+"/history/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID.String())
	rc.URLParams.Add("report_id", reportID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}

// TestImageVulnHandler_ReportHistoryScopedToURLCluster proves that a report_id
// belonging to another cluster does not leak its snapshot history through a
// {cluster_id} the caller is authorized for (cross-cluster IDOR).
func TestImageVulnHandler_ReportHistoryScopedToURLCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	reportID := uuid.New()

	q := &histVulnQuerier{
		stubVulnQuerier: newStubVulnQuerier(),
		snapshots: []sqlc.ImageVulnerabilityReportSnapshot{
			{ID: uuid.New(), ReportID: reportID, ClusterID: clusterB, CriticalCount: 7, ScannedAt: time.Now()},
			{ID: uuid.New(), ReportID: reportID, ClusterID: clusterB, CriticalCount: 3, ScannedAt: time.Now().Add(-time.Hour)},
		},
	}
	h := NewImageVulnHandler(q)

	// Request under cluster A: report belongs to cluster B -> no rows leak.
	rec := httptest.NewRecorder()
	h.ReportHistory(rec, reportHistoryReq(clusterA, reportID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeReportHistory(t, rec)
	if got != 0 {
		t.Fatalf("cross-cluster report history leaked %d snapshots, want 0", got)
	}

	// Request under the owning cluster B: rows are returned as normal.
	rec = httptest.NewRecorder()
	h.ReportHistory(rec, reportHistoryReq(clusterB, reportID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeReportHistory(t, rec); got != 2 {
		t.Fatalf("owning cluster should see 2 snapshots, got %d", got)
	}
}

func decodeReportHistory(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var env struct {
		Data struct {
			Snapshots  []map[string]any `json:"snapshots"`
			TotalCount int              `json:"total_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode report history: %v raw=%s", err, rec.Body.String())
	}
	if env.Data.TotalCount != len(env.Data.Snapshots) {
		t.Fatalf("total_count %d != len(snapshots) %d", env.Data.TotalCount, len(env.Data.Snapshots))
	}
	return len(env.Data.Snapshots)
}
