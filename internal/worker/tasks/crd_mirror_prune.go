// Sprint 069 — CRD-mirror v2 periodic prune.
//
// The agent re-sends every mirrored object on reconnect (full resync),
// so any row whose last_seen_at is older than the StaleRetention window
// is unambiguously gone from the cluster. This periodic task drops
// those rows across all five mirrored_* tables.
//
// Cadence: every 30 minutes. Worst case end-to-end "an operator
// deleted a resource → UI no longer shows it" is therefore
// StaleRetention (1h) + 30m = 90m, with the typical case being closer
// to a single resync cycle (~10m) because the agent's DeleteFunc fires
// immediately on the live delete event.

package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
)

// CrdMirrorPruneStaleType is the periodic task identifier registered with
// the asynq scheduler. Matches the convention used by sister cleanup
// tasks (resource_kind:action).
const CrdMirrorPruneStaleType = "crd_mirror:prune_stale"

// NewCrdMirrorPruneStaleTask is the constructor used both by the
// scheduler (one entry per cron tick) and by tests (ad-hoc enqueue).
func NewCrdMirrorPruneStaleTask() *asynq.Task {
	return asynq.NewTask(CrdMirrorPruneStaleType, nil, asynq.MaxRetry(2))
}

// HandleCrdMirrorPruneStale walks the five mirrored_* tables and drops
// rows whose last_seen_at is older than crd.StaleRetention. Cron: every
// 30 minutes. Held under the periodic-task leader lock so only one
// replica drops rows at a time even when several workers are deployed.
func HandleCrdMirrorPruneStale(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CrdMirrorPruneStaleType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "crd mirror prune runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(crd.MirrorQuerier)
		if !ok {
			return fmt.Errorf("crd mirror prune not supported by runtime querier")
		}
		before := time.Now().Add(-crd.StaleRetention)
		counts, err := crd.PruneStaleAll(ctx, q, before)
		if err != nil {
			return fmt.Errorf("crd mirror prune: %w", err)
		}
		// Log a single line with the per-kind counts so the daily
		// triage SLO dashboard can pick it up without scraping the
		// counter directly.
		runtimeLogger().InfoContext(ctx, "crd mirror prune sweep complete",
			"ingress_classes", counts[crd.KindIngressClass],
			"gateway_classes", counts[crd.KindGatewayClass],
			"network_policies", counts[crd.KindNetworkPolicy],
			"resource_quotas", counts[crd.KindResourceQuota],
			"limit_ranges", counts[crd.KindLimitRange],
			"cutoff", before.Format(time.RFC3339),
		)
		return nil
	})
}
