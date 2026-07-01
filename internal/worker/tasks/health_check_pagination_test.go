package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// hcPageQuerier embeds RuntimeQuerier and overrides only ListClusters,
// serving a fixed slice with real LIMIT/OFFSET semantics so we can prove the
// health-check sweep walks every page rather than the newest one.
type hcPageQuerier struct {
	RuntimeQuerier
	clusters []sqlc.Cluster
	calls    []sqlc.ListClustersParams
}

func (q *hcPageQuerier) ListClusters(_ context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	q.calls = append(q.calls, arg)
	start := int(arg.Offset)
	if start >= len(q.clusters) {
		return []sqlc.Cluster{}, nil
	}
	end := start + int(arg.Limit)
	if end > len(q.clusters) {
		end = len(q.clusters)
	}
	return append([]sqlc.Cluster(nil), q.clusters[start:end]...), nil
}

// TestHealthCheckTargets_PagesEntireFleet is the regression for the 500-row
// cap: a fleet larger than one page must be swept in full, or the oldest
// clusters (ORDER BY created_at DESC) never get their status re-evaluated and
// freeze at 'active' after their agents disconnect. Before the fix this
// returned only the first healthCheckPageSize rows.
func TestHealthCheckTargets_PagesEntireFleet(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	const total = healthCheckPageSize*2 + 37 // spans three pages
	all := make([]sqlc.Cluster, total)
	for i := range all {
		all[i] = sqlc.Cluster{ID: uuid.New()}
	}
	q := &hcPageQuerier{clusters: all}
	runtimeDeps = RuntimeDependencies{Queries: q}

	got, err := healthCheckTargets(context.Background(), "")
	if err != nil {
		t.Fatalf("healthCheckTargets: %v", err)
	}
	if len(got) != total {
		t.Fatalf("swept %d clusters, want %d (clusters beyond the first page must not be dropped)", len(got), total)
	}
	if len(q.calls) < 3 {
		t.Fatalf("expected pagination across >=3 pages, got %d ListClusters calls", len(q.calls))
	}
	if q.calls[0].Offset != 0 || q.calls[1].Offset != healthCheckPageSize {
		t.Fatalf("offsets did not advance by page size: %+v", q.calls)
	}
}
