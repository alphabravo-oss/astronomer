package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ClusterTombstoneRetentionType is the periodic task identifier for purging
// cluster tombstones (decommissioned_at IS NOT NULL) older than the retention
// window. Daily cadence; leader-gated so only one worker replica runs the
// DELETEs.
const ClusterTombstoneRetentionType = "cluster_tombstone:enforce_retention"

// defaultClusterTombstoneRetentionDays is the fallback when the configured
// window (CLUSTER_TOMBSTONE_RETENTION_DAYS) is unset. Conservative: the
// tombstone is the last resort for naming a cluster's archived audit rows, so
// keep it around well past any plausible incident-review window.
const defaultClusterTombstoneRetentionDays = 90

// clusterTombstonePurger is the optional sub-interface satisfied by the
// production *sqlc.Queries. Declared locally (like apiserverAuditPurger) so
// the RuntimeQuerier surface doesn't have to grow and test fakes only
// implement what they exercise.
type clusterTombstonePurger interface {
	ListExpiredClusterTombstones(ctx context.Context, cutoff pgtype.Timestamptz) ([]sqlc.ListExpiredClusterTombstonesRow, error)
	DeleteCluster(ctx context.Context, id uuid.UUID) error
}

// NewClusterTombstoneRetentionTask returns the periodic task. MaxRetry(2)
// mirrors the other retention sweeps — a transient failure retries a couple
// times, then the next day's tick is the backstop.
func NewClusterTombstoneRetentionTask() *asynq.Task {
	return asynq.NewTask(ClusterTombstoneRetentionType, nil, asynq.MaxRetry(2))
}

// HandleClusterTombstoneRetention hard-deletes cluster tombstones older than
// the retention window. The ListExpiredClusterTombstones query enforces the
// "backfill first, purge second" rule at runtime: any tombstone whose
// audit_archive rows still lack archived_cluster_name is skipped, because the
// clusters row is then the only way to name those rows.
func HandleClusterTombstoneRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterTombstoneRetentionType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "cluster tombstone retention runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(clusterTombstonePurger)
		if !ok {
			return fmt.Errorf("cluster tombstone retention not supported by runtime querier")
		}
		days := runtimeDeps.ClusterTombstoneRetentionDays
		if days <= 0 {
			days = defaultClusterTombstoneRetentionDays
		}
		cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		rows, err := q.ListExpiredClusterTombstones(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
		if err != nil {
			return fmt.Errorf("list expired cluster tombstones: %w", err)
		}
		for _, row := range rows {
			if err := q.DeleteCluster(ctx, row.ID); err != nil {
				return fmt.Errorf("delete cluster tombstone %s: %w", row.ID, err)
			}
			emitTombstonePurgedAudit(ctx, row, days)
		}
		if len(rows) > 0 {
			runtimeLogger().InfoContext(ctx, "purged cluster tombstones",
				"rows", len(rows),
				"cutoff", cutoff.Format(time.RFC3339),
				"retention_days", days,
			)
		}
		return nil
	})
}

// emitTombstonePurgedAudit records one audit row per purged tombstone. Like
// emitVeleroOrphanAudit, failure to audit never fails the sweep.
func emitTombstonePurgedAudit(ctx context.Context, row sqlc.ListExpiredClusterTombstonesRow, retentionDays int) {
	name := row.DisplayName
	if name == "" {
		name = row.Name
	}
	payload, err := json.Marshal(map[string]any{
		"cluster_id":        row.ID.String(),
		"cluster_name":      name,
		"decommissioned_at": row.DecommissionedAt.Time.Format(time.RFC3339),
		"retention_days":    retentionDays,
	})
	if err != nil {
		return
	}
	_ = runtimeDeps.Queries.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source:       "worker",
		Action:       "cluster.tombstone.purged",
		ResourceType: "cluster",
		ResourceID:   row.ID.String(),
		ResourceName: name,
		Detail:       payload,
	})
}
