package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// ApiserverAuditRetentionType is the periodic task identifier for pruning
// apiserver_audit_events rows older than the retention window. Daily cadence;
// leader-gated so only one worker replica runs the DELETE.
const ApiserverAuditRetentionType = "apiserver_audit:enforce_retention"

// apiserverAuditRetention is the default retention window. apiserver_audit_events
// is append-only and fleet-wide (one row per apiserver request across every
// managed cluster), so it grows unbounded unless a sweeper prunes it. 30 days
// matches the typical kube-apiserver audit backend retention.
const apiserverAuditRetention = 30 * 24 * time.Hour

// apiserverAuditPurger is the optional sub-interface satisfied by the production
// *sqlc.Queries. Declared locally (like audit_retention.go's jwtRevocationPurger)
// so the RuntimeQuerier surface doesn't have to grow and test fakes only
// implement what they exercise.
type apiserverAuditPurger interface {
	PruneApiserverAuditEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// NewApiserverAuditRetentionTask returns the periodic task. MaxRetry(2) mirrors
// the audit_log retention task — a transient failure retries a couple times,
// then the next day's tick is the backstop.
func NewApiserverAuditRetentionTask() *asynq.Task {
	return asynq.NewTask(ApiserverAuditRetentionType, nil, asynq.MaxRetry(2))
}

// HandleApiserverAuditRetention prunes apiserver audit rows older than the
// retention window. Leader-gated; cooperative DB lease keeps multiple worker
// pods from racing on the same DELETE.
func HandleApiserverAuditRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ApiserverAuditRetentionType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "apiserver audit retention runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(apiserverAuditPurger)
		if !ok {
			return fmt.Errorf("apiserver audit retention not supported by runtime querier")
		}
		cutoff := time.Now().UTC().Add(-apiserverAuditRetention)
		pruned, err := q.PruneApiserverAuditEventsBefore(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("prune apiserver audit events: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "pruned apiserver audit events",
			"rows", pruned,
			"cutoff", cutoff.Format(time.RFC3339),
			"retention", apiserverAuditRetention.String(),
		)
		return nil
	})
}
