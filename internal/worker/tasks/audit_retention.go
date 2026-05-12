package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

type auditRetentionQuerier interface {
	ListAuditLogPartitions(ctx context.Context) ([]string, error)
	DropAuditLogPartition(ctx context.Context, name string) error
}

// jwtRevocationPurger is the optional sub-interface satisfied by
// implementations that also need to GC expired JWT deny-list rows. The
// production *sqlc.Queries satisfies both this and auditRetentionQuerier;
// tests that exercise only the partition path can implement just the
// outer interface. Wiring it under the same daily cron keeps the
// retention story uniform without introducing a second scheduler entry.
type jwtRevocationPurger interface {
	PurgeExpiredJWTRevocations(ctx context.Context) (int64, error)
}

// EnforceAuditLogRetentionType is the periodic task identifier for pruning old
// monthly audit_log partitions after they age out of the configured window.
const EnforceAuditLogRetentionType = "audit_log:enforce_retention"

func NewEnforceAuditLogRetentionTask() *asynq.Task {
	return asynq.NewTask(EnforceAuditLogRetentionType, nil, asynq.MaxRetry(2))
}

func HandleEnforceAuditLogRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, EnforceAuditLogRetentionType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "audit retention runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(auditRetentionQuerier)
		if !ok {
			return fmt.Errorf("audit retention not supported by runtime querier")
		}
		return enforceAuditLogRetention(ctx, q, time.Now().UTC(), runtimeDeps.AuditLogRetentionMonths)
	})
}

func enforceAuditLogRetention(ctx context.Context, q auditRetentionQuerier, now time.Time, retentionMonths int) error {
	toDrop, err := auditLogPartitionsToDrop(ctx, q, now, retentionMonths)
	if err != nil {
		return err
	}
	for _, name := range toDrop {
		if err := q.DropAuditLogPartition(ctx, name); err != nil {
			return fmt.Errorf("drop audit_log partition %s: %w", name, err)
		}
	}
	runtimeLogger().InfoContext(ctx, "enforced audit_log partition retention", "retention_months", normalizeAuditLogRetentionMonths(retentionMonths), "dropped_partitions", len(toDrop))

	// JWT revocation GC piggy-backs on the same nightly cron. The deny
	// list is bounded by the access-token lifetime (~1h to 7d) so
	// rows naturally fall off; this just GCs them so the table doesn't
	// keep accumulating tombstones from old logouts forever.
	if purger, ok := q.(jwtRevocationPurger); ok {
		if purged, err := purger.PurgeExpiredJWTRevocations(ctx); err != nil {
			// Don't fail the whole task — partition retention
			// already ran successfully, and the deny list will
			// re-attempt tomorrow.
			runtimeLogger().WarnContext(ctx, "purge expired jwt revocations failed", "error", err)
		} else if purged > 0 {
			runtimeLogger().InfoContext(ctx, "purged expired jwt revocations", "rows", purged)
		}
	}
	return nil
}

func auditLogPartitionsToDrop(ctx context.Context, q auditRetentionQuerier, now time.Time, retentionMonths int) ([]string, error) {
	partitions, err := q.ListAuditLogPartitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list audit_log partitions: %w", err)
	}
	cutoff := auditLogRetentionCutoff(now, retentionMonths)
	drop := make([]string, 0, len(partitions))
	for _, name := range partitions {
		monthStart, ok := parseAuditLogPartitionMonth(name)
		if !ok {
			continue
		}
		if monthStart.Before(cutoff) {
			drop = append(drop, name)
		}
	}
	return drop, nil
}

func auditLogRetentionCutoff(now time.Time, retentionMonths int) time.Time {
	months := normalizeAuditLogRetentionMonths(retentionMonths)
	currentMonth := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	return currentMonth.AddDate(0, -(months - 1), 0)
}

func normalizeAuditLogRetentionMonths(months int) int {
	if months <= 0 {
		return 13
	}
	return months
}

func parseAuditLogPartitionMonth(name string) (time.Time, bool) {
	var year, month int
	if _, err := fmt.Sscanf(name, "audit_log_%04d_%02d", &year, &month); err != nil {
		return time.Time{}, false
	}
	if month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), true
}
