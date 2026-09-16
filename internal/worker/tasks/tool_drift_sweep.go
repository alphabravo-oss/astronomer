// Tool drift reconciliation sweep (P1 item 16/22).
//
// This periodic sweep gives directly Helm-installed tools the same explicit
// drift evidence as delivery-managed resources. It compares the desired state Astronomer
// recorded on each installed_charts row (status + revision) against the
// live helm release and stamps installed_charts.drift_detected / drift_detail
// so the catalog UI can surface a drift badge.
//
// Drift is flagged when either:
//   - the release is gone from the cluster (helm "release not found"), or
//   - the live helm status is not "deployed" (failed/pending-rollback/etc.), or
//   - the live revision is ahead of the revision Astronomer last recorded
//     (someone ran `helm upgrade` out-of-band).
//
// The sweep does NOT auto-correct — like the cluster_template drift check it
// only surfaces the divergence; the operator decides whether to reapply.
//
// This is a tunnel-queue task: helm Status RPCs go through the per-cluster
// agent WebSocket, which only terminates on the server pod. So it is
// registered on the server-embedded tunnel worker, not the standalone
// worker pod (mirrors cluster_template:drift_check).
package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ToolDriftSweepType is the asynq task type. Re-exported via
// worker.TypeToolDriftSweep for the mux wiring. Routes through the tunnel
// queue (ClusterTemplateApplyQueueName) because it needs the agent WS.
const ToolDriftSweepType = "tool:drift_sweep"

// toolDriftSweepBatch caps how many installed_charts rows the sweep probes
// per tick. Each row is one helm Status RPC over the tunnel, so we keep the
// batch small to bound tunnel load; the claim query rotates oldest coverage
// across ticks and lets worker replicas process disjoint rows.
const toolDriftSweepBatch = 100

const toolDriftLeaseTTL = 10 * time.Minute

// A Helm status request may otherwise inherit the tunnel's deliberately long
// ten-minute command timeout. Keep every claimed unit of work comfortably
// inside the durable lease instead: a timed-out cluster cannot hold the rest
// of the sweep hostage or let a stale worker write after ownership expires.
const toolDriftProbeTimeout = 45 * time.Second

// ToolDriftSweepQuerier is the slice of *sqlc.Queries the sweep uses.
// Local interface so unit tests can stand up a fake.
type ToolDriftSweepQuerier interface {
	ClaimInstalledChartsForDriftSweep(ctx context.Context, arg sqlc.ClaimInstalledChartsForDriftSweepParams) ([]sqlc.InstalledChart, error)
	MarkInstalledChartDrift(ctx context.Context, arg sqlc.MarkInstalledChartDriftParams) (int64, error)
	ReleaseInstalledChartDriftClaim(ctx context.Context, arg sqlc.ReleaseInstalledChartDriftClaimParams) (int64, error)
}

// HelmStatusProber probes the live helm release. *handler.TunnelHelmRequester
// implements this via its Status method. Narrowed here so the worker package
// doesn't import the handler package (which would import-cycle).
type HelmStatusProber interface {
	Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error)
}

// ToolDriftSweepDeps wires the sweep.
type ToolDriftSweepDeps struct {
	Queries ToolDriftSweepQuerier
	Helm    HelmStatusProber
}

// HandleToolDriftSweep is the tunnel-worker handler. Missing composition is a
// task error and a production startup failure.
func (runtime ToolDriftRuntime) HandleToolDriftSweep(ctx context.Context, _ *asynq.Task) error {
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("tool drift sweep runtime is not configured")
	}
	return runPeriodicTaskWithRowLeases(ctx, ToolDriftSweepType, func() error {
		return runToolDriftSweep(ctx, runtime.Deps)
	})
}

// runToolDriftSweep is the testable core, split from the asynq handler so
// tests don't need an asynq.Task or the leader lease.
func runToolDriftSweep(ctx context.Context, deps ToolDriftSweepDeps) error {
	claimToken := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	charts, err := deps.Queries.ClaimInstalledChartsForDriftSweep(ctx, sqlc.ClaimInstalledChartsForDriftSweepParams{
		LockedUntil: pgtype.Timestamptz{Time: time.Now().UTC().Add(toolDriftLeaseTTL), Valid: true},
		ClaimToken:  claimToken,
		QueryLimit:  toolDriftSweepBatch,
	})
	if err != nil {
		return fmt.Errorf("list installed charts for drift sweep: %w", err)
	}
	var drift atomic.Int64
	var failuresMu sync.Mutex
	var failures []error
	recordFailure := func(err error) {
		failuresMu.Lock()
		failures = append(failures, err)
		failuresMu.Unlock()
	}
	fanOutClusters(ctx, charts, toolDriftProbeTimeout, func(probeCtx context.Context, c sqlc.InstalledChart) {
		detected, detail, probeOK := chartDrift(probeCtx, deps.Helm, c)
		if !probeOK {
			// Transient probe failure: preserve the prior drift state and
			// advance the attempt timestamp while releasing ownership. That
			// prevents an unreachable oldest prefix from starving healthy rows.
			rows, err := deps.Queries.ReleaseInstalledChartDriftClaim(ctx, sqlc.ReleaseInstalledChartDriftClaimParams{
				ID: c.ID, ClaimToken: claimToken,
			})
			if err != nil || rows != 1 {
				if err == nil {
					err = fmt.Errorf("claim ownership lost")
				}
				recordFailure(fmt.Errorf("release tool drift claim %s: %w", c.ID, err))
				runtimeLogger(ctx).WarnContext(ctx, "tool drift claim release failed", "installed_chart_id", c.ID, "error", err)
			}
			return
		}
		rows, err := deps.Queries.MarkInstalledChartDrift(ctx, sqlc.MarkInstalledChartDriftParams{
			ID:            c.ID,
			DriftDetected: detected,
			DriftDetail:   detail,
			ClaimToken:    claimToken,
		})
		if err != nil || rows != 1 {
			if err == nil {
				err = fmt.Errorf("claim ownership lost")
			}
			recordFailure(fmt.Errorf("mark tool drift %s: %w", c.ID, err))
			runtimeLogger(ctx).WarnContext(ctx, "tool drift mark failed",
				"installed_chart_id", c.ID, "error", err)
			return
		}
		if detected {
			drift.Add(1)
		}
	})
	runtimeLogger(ctx).InfoContext(ctx, "tool drift sweep", "evaluated", len(charts), "drift", drift.Load())
	return errors.Join(failures...)
}

// chartDrift compares one installed_charts row against its live helm
// release and returns (drifted, human-readable detail, probeOK). detail is
// "" when no drift. probeOK is false on a transient probe error (agent not
// connected, timeout): the caller skips the write so the prior drift state
// and drift_checked_at are preserved and the row is re-probed promptly,
// rather than erasing a genuine drift signal on a one-off blip.
func chartDrift(ctx context.Context, helm HelmStatusProber, c sqlc.InstalledChart) (bool, string, bool) {
	status, err := helm.Status(ctx, c.ClusterID.String(), c.ReleaseName, c.Namespace)
	if err != nil {
		if isReleaseNotFound(err) {
			return true, "helm release missing from cluster", true
		}
		// Transient (agent not connected, timeout). Signal probeOK=false so
		// the caller preserves the prior drift state instead of overwriting
		// it. We log so the probe failure isn't invisible.
		runtimeLogger(ctx).WarnContext(ctx, "tool drift probe failed",
			"release", c.ReleaseName, "namespace", c.Namespace, "error", err)
		return false, "", false
	}
	if status == nil {
		return false, "", true
	}
	if status.Status != "deployed" {
		return true, fmt.Sprintf("helm release status %q (expected \"deployed\")", status.Status), true
	}
	if int32(status.Revision) > c.Revision {
		return true, fmt.Sprintf("helm revision %d ahead of recorded revision %d (out-of-band upgrade)", status.Revision, c.Revision), true
	}
	return false, "", true
}

// isReleaseNotFound mirrors handler.isHelmReleaseNotFound without taking a
// handler-package dependency.
func isReleaseNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "release: not found") || strings.Contains(msg, "release not found")
}
