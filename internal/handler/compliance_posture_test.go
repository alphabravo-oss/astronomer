package handler

// Compliance posture aggregator unit tests (T1.2).
//
// What we pin here is the scoring math, not the HTTP wiring (the
// routes file is the contract for the latter). The fake querier
// returns a small, hand-curated fleet so the expected scores are
// computable on a pocket calculator: any future weight tweak or
// formula change will fail this test loudly.

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakePostureQuerier returns canned data per cluster.
type fakePostureQuerier struct {
	clusters []sqlc.Cluster
	// cis maps cluster_id → (passed, failed, completedAt). Missing
	// keys yield zero rows so the cluster falls into the "unknown" 50
	// bucket.
	cis map[uuid.UUID]struct {
		Passed      int32
		Failed      int32
		CompletedAt time.Time
	}
	// vulns maps cluster_id → (critical, high, reportCount).
	vulns map[uuid.UUID]struct {
		Critical    int64
		High        int64
		ReportCount int64
	}
	// netpol maps cluster_id → true when a policy row exists.
	netpol map[uuid.UUID]bool
}

func (f *fakePostureQuerier) ListClusters(_ context.Context, _ sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return f.clusters, nil
}

func (f *fakePostureQuerier) ListScansByClusterAndType(_ context.Context, arg sqlc.ListScansByClusterAndTypeParams) ([]sqlc.SecurityScanResult, error) {
	if arg.ScanType != "cis" {
		return nil, nil
	}
	v, ok := f.cis[arg.ClusterID]
	if !ok {
		return nil, nil
	}
	return []sqlc.SecurityScanResult{{
		ID:          uuid.New(),
		ClusterID:   arg.ClusterID,
		ScanType:    "cis",
		Status:      "completed",
		Passed:      v.Passed,
		Failed:      v.Failed,
		CompletedAt: pgtype.Timestamptz{Time: v.CompletedAt, Valid: true},
	}}, nil
}

func (f *fakePostureQuerier) AggregateClusterVulnerabilities(_ context.Context, id uuid.UUID) (sqlc.AggregateClusterVulnerabilitiesRow, error) {
	v, ok := f.vulns[id]
	if !ok {
		return sqlc.AggregateClusterVulnerabilitiesRow{}, nil
	}
	return sqlc.AggregateClusterVulnerabilitiesRow{
		Critical:    v.Critical,
		High:        v.High,
		ReportCount: v.ReportCount,
	}, nil
}

func (f *fakePostureQuerier) ListClusterSecurityPolicies(_ context.Context, _ sqlc.ListClusterSecurityPoliciesParams) ([]sqlc.ClusterSecurityPolicy, error) {
	out := make([]sqlc.ClusterSecurityPolicy, 0, len(f.netpol))
	for id, has := range f.netpol {
		if has {
			out = append(out, sqlc.ClusterSecurityPolicy{ClusterID: id})
		}
	}
	return out, nil
}

// TestPosture_DeterministicScore is the acceptance test from T1.2:
// "Score is deterministic for a given DB snapshot (unit test)."
// Two clusters, hand-computed scores, frozen clock so CIS staleness
// is reproducible.
func TestPosture_DeterministicScore(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	clusterA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	clusterB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	q := &fakePostureQuerier{
		clusters: []sqlc.Cluster{
			{ID: clusterA, Name: "alpha"},
			{ID: clusterB, Name: "bravo"},
		},
		cis: map[uuid.UUID]struct {
			Passed      int32
			Failed      int32
			CompletedAt time.Time
		}{
			// 80/100 passed → cis=80
			clusterA: {Passed: 80, Failed: 20, CompletedAt: now.Add(-5 * 24 * time.Hour)},
			// 50/50 passed → cis=50
			clusterB: {Passed: 50, Failed: 50, CompletedAt: now.Add(-5 * 24 * time.Hour)},
		},
		vulns: map[uuid.UUID]struct {
			Critical    int64
			High        int64
			ReportCount int64
		}{
			// 0 crit, 10 high → penalty=10 → vulns=90
			clusterA: {Critical: 0, High: 10, ReportCount: 5},
			// 4 crit, 0 high → penalty=20 → vulns=80
			clusterB: {Critical: 4, High: 0, ReportCount: 5},
		},
		netpol: map[uuid.UUID]bool{
			clusterA: true,  // → netpol=100
			clusterB: false, // → netpol=0
		},
	}

	// Audit-retention 13 months → audit sub-score 100.
	h := NewCompliancePostureHandler(q, 13).withNow(func() time.Time { return now })
	out := h.compute(context.Background(), q.clusters)

	// Per-cluster overall scores:
	//   alpha = 80*0.30 + 90*0.30 + 100*0.25 +   100*0.15 = 24 + 27 + 25 + 15 = 91.0
	//   bravo = 50*0.30 + 80*0.30 +   0*0.25 +   100*0.15 = 15 + 24 +  0 + 15 = 54.0
	// Fleet sub-scores (unweighted mean): cis=65, vulns=85, netpol=50.
	// Fleet overall = 65*0.30 + 85*0.30 + 50*0.25 + 100*0.15
	//               = 19.5 + 25.5 + 12.5 + 15.0 = 72.5
	wantOverall := 72.5
	if math.Abs(out.OverallScore-wantOverall) > 1e-6 {
		t.Errorf("overall_score = %v, want %v", out.OverallScore, wantOverall)
	}
	if math.Abs(out.CISScore-65.0) > 1e-6 {
		t.Errorf("cis_score = %v, want 65", out.CISScore)
	}
	if math.Abs(out.VulnsScore-85.0) > 1e-6 {
		t.Errorf("vulns_score = %v, want 85", out.VulnsScore)
	}
	if math.Abs(out.NetPolScore-50.0) > 1e-6 {
		t.Errorf("netpol_score = %v, want 50", out.NetPolScore)
	}
	if out.AuditScore != 100.0 {
		t.Errorf("audit_score = %v, want 100", out.AuditScore)
	}
	if out.ClusterCount != 2 || len(out.Clusters) != 2 {
		t.Errorf("ClusterCount/Clusters mismatch: %d / %d", out.ClusterCount, len(out.Clusters))
	}
}

func TestPosture_EmptyFleet(t *testing.T) {
	q := &fakePostureQuerier{clusters: nil}
	h := NewCompliancePostureHandler(q, 13)
	out := h.compute(context.Background(), nil)
	if out.ClusterCount != 0 || len(out.Clusters) != 0 {
		t.Errorf("expected empty fleet, got %+v", out)
	}
	// audit-only contribution: 100 × 0.15 = 15.
	if math.Abs(out.OverallScore-15.0) > 1e-6 {
		t.Errorf("overall = %v, want 15", out.OverallScore)
	}
}

func TestPosture_StaleCISFallsBackTo50(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	cid := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	q := &fakePostureQuerier{
		clusters: []sqlc.Cluster{{ID: cid, Name: "stale"}},
		cis: map[uuid.UUID]struct {
			Passed      int32
			Failed      int32
			CompletedAt time.Time
		}{
			// 60 days old → falls outside cisStaleWindow → 50
			cid: {Passed: 80, Failed: 20, CompletedAt: now.Add(-60 * 24 * time.Hour)},
		},
		vulns:  map[uuid.UUID]struct{ Critical, High, ReportCount int64 }{},
		netpol: map[uuid.UUID]bool{cid: true},
	}
	h := NewCompliancePostureHandler(q, 13).withNow(func() time.Time { return now })
	out := h.compute(context.Background(), q.clusters)
	if math.Abs(out.CISScore-50.0) > 1e-6 {
		t.Errorf("stale CIS cluster sub-score = %v, want 50", out.CISScore)
	}
}

func TestPosture_HTTPEndpoint_ReturnsEnvelopedJSON(t *testing.T) {
	q := &fakePostureQuerier{clusters: nil}
	h := NewCompliancePostureHandler(q, 13)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/compliance/posture/", nil)
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Data PostureScores `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.RetentionMonths != 13 {
		t.Errorf("retention_months = %d, want 13", env.Data.RetentionMonths)
	}
}
