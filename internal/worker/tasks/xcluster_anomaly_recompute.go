package tasks

// P1 item 5/22 — cross-cluster ("fleet-wide") anomaly baseline
// recompute.
//
// Where the per-cluster recompute (anomaly_baseline_recompute.go)
// answers "is this cluster deviating from its own history?", this
// path answers "is this cluster an outlier vs. the rest of the
// fleet?". It groups every per-cluster anomaly_baselines row by
// (metric_name, window_seconds), treats each cluster's `mean` as one
// fleet datapoint, computes the fleet mean/stddev via the same
// internal/anomaly stat pass the per-cluster path uses, and records
// the set of clusters whose mean deviates from the fleet mean by more
// than `stddev_mult` population stddevs.
//
// Cadence: every 5m, leader-elected, same as the per-cluster path.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/anomaly"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// XClusterAnomalyRecomputeType is the periodic-task identifier.
const XClusterAnomalyRecomputeType = "anomaly:xcluster_recompute"

// xclusterMinClusters gates the cold-start false-positive: with fewer
// than this many contributing clusters the fleet stddev is
// meaningless, so we record the aggregate but flag no outliers.
const xclusterMinClusters = 3

// xclusterDefaultStddevMult is how many population stddevs from the
// fleet mean a cluster's per-cluster mean must be to count as an
// outlier. Mirrors the per-cluster default (3.0).
const xclusterDefaultStddevMult = 3.0

// xclusterRecomputeQuerier is the narrow interface this task needs.
// ListAnomalyBaselines is shared with the per-cluster recompute path.
type xclusterRecomputeQuerier interface {
	ListAnomalyBaselines(ctx context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error)
	UpsertXClusterAnomalyBaseline(ctx context.Context, arg sqlc.UpsertXClusterAnomalyBaselineParams) (sqlc.XclusterAnomalyBaseline, error)
}

// NewXClusterAnomalyRecomputeTask returns the asynq task that drives
// the periodic fleet-wide recompute.
func NewXClusterAnomalyRecomputeTask() *asynq.Task {
	return asynq.NewTask(XClusterAnomalyRecomputeType, nil, asynq.MaxRetry(2))
}

// HandleXClusterAnomalyRecompute is the asynq handler.
func HandleXClusterAnomalyRecompute(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, XClusterAnomalyRecomputeType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "xcluster anomaly recompute runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(xclusterRecomputeQuerier)
		if !ok {
			return fmt.Errorf("xcluster anomaly recompute not supported by runtime querier")
		}
		return RunXClusterAnomalyRecompute(ctx, q)
	})
}

// xclusterGroupKey identifies a fleet-baseline bucket.
type xclusterGroupKey struct {
	metric string
	window int32
}

// xclusterMember is one cluster's contribution to a fleet bucket.
type xclusterMember struct {
	clusterID uuid.UUID
	mean      float64
}

// RunXClusterAnomalyRecompute is the testable core of the periodic
// handler. It pages over every per-cluster baseline, groups by
// (metric, window), and upserts one fleet-wide baseline per group.
func RunXClusterAnomalyRecompute(ctx context.Context, q xclusterRecomputeQuerier) error {
	groups := map[xclusterGroupKey][]xclusterMember{}

	const pageSize = int32(200)
	for offset := int32(0); ; offset += pageSize {
		page, err := q.ListAnomalyBaselines(ctx, sqlc.ListAnomalyBaselinesParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("list baselines: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, b := range page {
			// Skip clusters that haven't accumulated any samples
			// yet — a mean of 0 from an empty baseline would drag
			// the fleet mean and produce spurious outliers.
			if b.SampleCount <= 0 {
				continue
			}
			key := xclusterGroupKey{metric: b.MetricName, window: b.WindowSeconds}
			groups[key] = append(groups[key], xclusterMember{clusterID: b.ClusterID, mean: b.Mean})
		}
		if int32(len(page)) < pageSize {
			break
		}
	}

	now := time.Now().UTC()
	for key, members := range groups {
		samples := make([]anomaly.Sample, 0, len(members))
		for _, m := range members {
			samples = append(samples, anomaly.Sample{Value: m.mean, Time: now})
		}
		stats := anomaly.Compute(samples)

		outliers := []string{}
		// Only flag once the fleet is large enough for the stddev to
		// mean something, and only when there's spread to measure.
		if len(members) >= xclusterMinClusters && stats.Stddev > 0 {
			threshold := xclusterDefaultStddevMult * stats.Stddev
			for _, m := range members {
				if math.Abs(m.mean-stats.Mean) > threshold {
					outliers = append(outliers, m.clusterID.String())
				}
			}
		}
		encoded, err := json.Marshal(outliers)
		if err != nil {
			return fmt.Errorf("encode outliers: %w", err)
		}

		if _, err := q.UpsertXClusterAnomalyBaseline(ctx, sqlc.UpsertXClusterAnomalyBaselineParams{
			MetricName:        key.metric,
			WindowSeconds:     key.window,
			ClusterCount:      int32(len(members)),
			FleetMean:         stats.Mean,
			FleetStddev:       stats.Stddev,
			FleetMin:          stats.Min,
			FleetMax:          stats.Max,
			StddevMult:        xclusterDefaultStddevMult,
			OutlierClusterIds: encoded,
		}); err != nil {
			return fmt.Errorf("upsert xcluster baseline (%s): %w", key.metric, err)
		}
	}
	return nil
}
