// T6.069 — periodic populator for the astronomer_crd_mirror_rows
// gauge.
//
// The gauge is declared in internal/crd/ingest_v2.go but had no
// populator: ingest-time increments live in IngestsTotal (a counter),
// and PruneStale runs on its own cadence without computing the
// current row count. This task fans out a single UNION-ALL SQL query
// every minute and Sets the gauge so it reflects truth without
// requiring callers to fight with counter math.

package tasks

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// CrdMirrorGaugePopulateType is the periodic task identifier.
const CrdMirrorGaugePopulateType = "crd_mirror:gauge_populate"

// NewCrdMirrorGaugePopulateTask returns the periodic task wrapper.
func NewCrdMirrorGaugePopulateTask() *asynq.Task {
	return asynq.NewTask(CrdMirrorGaugePopulateType, nil, asynq.MaxRetry(2))
}

// HandleCrdMirrorGaugePopulate fetches per-(kind, cluster) row counts
// and rewrites the gauge series. Cluster IDs (not names) are used as
// the label value because the worker package can't always resolve the
// cluster row cheaply; the prometheus join with the cluster_info
// metric handles the name lookup at query time.
func HandleCrdMirrorGaugePopulate(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CrdMirrorGaugePopulateType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "crd mirror gauge populate runtime not configured, skipping")
			return nil
		}
		// We need the concrete *Queries.CountMirroredRowsByKind method —
		// the runtime querier interface doesn't include it (the gauge
		// populator is the only caller). The runtime exposes *sqlc.Queries
		// in production; in tests we fall back to a no-op.
		q, ok := runtimeDeps.Queries.(mirrorCountQuerier)
		if !ok {
			runtimeLogger().DebugContext(ctx, "runtime querier does not support CountMirroredRowsByKind, skipping gauge populate")
			return nil
		}
		rows, err := q.CountMirroredRowsByKind(ctx)
		if err != nil {
			return fmt.Errorf("count mirrored rows: %w", err)
		}
		// Reset existing series so a cluster whose row count dropped to
		// zero doesn't keep emitting a stale value. Prometheus gauges
		// without an update keep their last value forever.
		crd.Rows.Reset()
		for _, r := range rows {
			crd.Rows.WithLabelValues(r.Kind, r.ClusterID.String()).Set(float64(r.Count))
		}
		runtimeLogger().InfoContext(ctx, "crd mirror gauge populated", "series", len(rows))
		return nil
	})
}

// mirrorCountQuerier is the narrow surface we expect from the runtime
// querier. *sqlc.Queries satisfies it via crd_mirror_v2_counts.sql.go.
type mirrorCountQuerier interface {
	CountMirroredRowsByKind(ctx context.Context) ([]sqlc.MirrorRowCount, error)
}
