package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeXClusterQuerier implements xclusterRecomputeQuerier.
type fakeXClusterQuerier struct {
	baselines []sqlc.AnomalyBaseline
	upserts   []sqlc.UpsertXClusterAnomalyBaselineParams
}

func (f *fakeXClusterQuerier) ListAnomalyBaselines(_ context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error) {
	if int(arg.Offset) >= len(f.baselines) {
		return []sqlc.AnomalyBaseline{}, nil
	}
	out := f.baselines[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeXClusterQuerier) UpsertXClusterAnomalyBaseline(_ context.Context, arg sqlc.UpsertXClusterAnomalyBaselineParams) (sqlc.XclusterAnomalyBaseline, error) {
	f.upserts = append(f.upserts, arg)
	return sqlc.XclusterAnomalyBaseline{}, nil
}

// TestRunXClusterAnomalyRecompute_FlagsOutlierCluster verifies that a
// cluster whose per-cluster mean is far from the fleet mean is flagged
// as an outlier, while in-band clusters are not, and that empty
// (sample_count=0) baselines are ignored.
func TestRunXClusterAnomalyRecompute_FlagsOutlierCluster(t *testing.T) {
	const metric = "cluster_cpu_percent"
	const window = int32(86400)

	tight := make([]uuid.UUID, 10)
	for i := range tight {
		tight[i] = uuid.New()
	}
	outlier := uuid.New()
	empty := uuid.New()

	q := &fakeXClusterQuerier{}
	// Ten clusters clustered tightly around ~10%.
	for _, id := range tight {
		q.baselines = append(q.baselines, sqlc.AnomalyBaseline{
			ClusterID: id, MetricName: metric, WindowSeconds: window,
			SampleCount: 100, Mean: 10,
		})
	}
	// One wild outlier at 95%.
	q.baselines = append(q.baselines, sqlc.AnomalyBaseline{
		ClusterID: outlier, MetricName: metric, WindowSeconds: window,
		SampleCount: 100, Mean: 95,
	})
	// An empty baseline that must be ignored (mean 0, no samples).
	q.baselines = append(q.baselines, sqlc.AnomalyBaseline{
		ClusterID: empty, MetricName: metric, WindowSeconds: window,
		SampleCount: 0, Mean: 0,
	})

	if err := RunXClusterAnomalyRecompute(context.Background(), q); err != nil {
		t.Fatalf("RunXClusterAnomalyRecompute: %v", err)
	}

	if len(q.upserts) != 1 {
		t.Fatalf("want 1 fleet baseline upsert, got %d", len(q.upserts))
	}
	got := q.upserts[0]
	if got.MetricName != metric || got.WindowSeconds != window {
		t.Fatalf("unexpected group key: %s/%d", got.MetricName, got.WindowSeconds)
	}
	// 11 contributing clusters (the empty one is skipped).
	if got.ClusterCount != 11 {
		t.Fatalf("want cluster_count 11, got %d", got.ClusterCount)
	}

	var outliers []string
	if err := json.Unmarshal(got.OutlierClusterIds, &outliers); err != nil {
		t.Fatalf("unmarshal outliers: %v", err)
	}
	if len(outliers) != 1 || outliers[0] != outlier.String() {
		t.Fatalf("want only %s flagged, got %v", outlier, outliers)
	}
}

// TestRunXClusterAnomalyRecompute_ColdStartFlagsNothing verifies the
// min-clusters gate: with fewer than xclusterMinClusters contributors
// we record the aggregate but flag no outliers.
func TestRunXClusterAnomalyRecompute_ColdStartFlagsNothing(t *testing.T) {
	q := &fakeXClusterQuerier{
		baselines: []sqlc.AnomalyBaseline{
			{ClusterID: uuid.New(), MetricName: "m", WindowSeconds: 1, SampleCount: 10, Mean: 1},
			{ClusterID: uuid.New(), MetricName: "m", WindowSeconds: 1, SampleCount: 10, Mean: 100},
		},
	}
	if err := RunXClusterAnomalyRecompute(context.Background(), q); err != nil {
		t.Fatalf("RunXClusterAnomalyRecompute: %v", err)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(q.upserts))
	}
	var outliers []string
	if err := json.Unmarshal(q.upserts[0].OutlierClusterIds, &outliers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(outliers) != 0 {
		t.Fatalf("cold-start (2 clusters) must flag nothing, got %v", outliers)
	}
}
