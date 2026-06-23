// Tool drift reconciliation sweep (P1 item 16/22).
//
// ArgoCD applications ship with built-in drift detection; helm-installed
// tools did not. This periodic sweep compares the desired state Astronomer
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
	"fmt"
	"strings"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ToolDriftSweepType is the asynq task type. Re-exported via
// worker.TypeToolDriftSweep for the mux wiring. Routes through the tunnel
// queue (ClusterTemplateApplyQueueName) because it needs the agent WS.
const ToolDriftSweepType = "tool:drift_sweep"

// toolDriftSweepBatch caps how many installed_charts rows the sweep probes
// per tick. Each row is one helm Status RPC over the tunnel, so we keep the
// batch small to bound the leader-lease hold; the ORDER BY drift_checked_at
// in ListInstalledChartsForDriftSweep rotates coverage across ticks.
const toolDriftSweepBatch = 100

// ToolDriftSweepQuerier is the slice of *sqlc.Queries the sweep uses.
// Local interface so unit tests can stand up a fake.
type ToolDriftSweepQuerier interface {
	ListInstalledChartsForDriftSweep(ctx context.Context, limit int32) ([]sqlc.InstalledChart, error)
	MarkInstalledChartDrift(ctx context.Context, arg sqlc.MarkInstalledChartDriftParams) error
}

// HelmStatusProber probes the live helm release. *handler.TunnelHelmRequester
// implements this via its Status method. Narrowed here so the worker package
// doesn't import the handler package (which would import-cycle).
type HelmStatusProber interface {
	Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error)
}

// ToolDriftSweepDeps wires the sweep. Set once at startup via
// ConfigureToolDriftSweep; tests swap fakes.
type ToolDriftSweepDeps struct {
	Queries ToolDriftSweepQuerier
	Helm    HelmStatusProber
}

var toolDriftSweepDeps ToolDriftSweepDeps

// ConfigureToolDriftSweep wires runtime dependencies. Called from server
// bootstrap (the sweep runs on the server pod's tunnel worker).
func ConfigureToolDriftSweep(deps ToolDriftSweepDeps) {
	toolDriftSweepDeps = deps
}

// ResetToolDriftSweep clears the runtime deps. Used by tests.
func ResetToolDriftSweep() {
	toolDriftSweepDeps = ToolDriftSweepDeps{}
}

// HandleToolDriftSweep is the asynq handler. Skips silently when unwired
// (e.g. the standalone worker pod, which has no tunnel).
func HandleToolDriftSweep(ctx context.Context, _ *asynq.Task) error {
	if toolDriftSweepDeps.Queries == nil || toolDriftSweepDeps.Helm == nil {
		runtimeLogger().InfoContext(ctx, "tool drift sweep runtime not configured, skipping")
		return nil
	}
	return runPeriodicTaskWithLeader(ctx, ToolDriftSweepType, func() error {
		return runToolDriftSweep(ctx, toolDriftSweepDeps)
	})
}

// runToolDriftSweep is the testable core, split from the asynq handler so
// tests don't need an asynq.Task or the leader lease.
func runToolDriftSweep(ctx context.Context, deps ToolDriftSweepDeps) error {
	charts, err := deps.Queries.ListInstalledChartsForDriftSweep(ctx, toolDriftSweepBatch)
	if err != nil {
		return fmt.Errorf("list installed charts for drift sweep: %w", err)
	}
	drift := 0
	for _, c := range charts {
		detected, detail, probeOK := chartDrift(ctx, deps.Helm, c)
		if !probeOK {
			// Transient probe failure: preserve the prior drift state and
			// old drift_checked_at so the row is re-probed promptly.
			continue
		}
		if err := deps.Queries.MarkInstalledChartDrift(ctx, sqlc.MarkInstalledChartDriftParams{
			ID:            c.ID,
			DriftDetected: detected,
			DriftDetail:   detail,
		}); err != nil {
			runtimeLogger().WarnContext(ctx, "tool drift mark failed",
				"installed_chart_id", c.ID, "error", err)
			continue
		}
		if detected {
			drift++
		}
	}
	runtimeLogger().InfoContext(ctx, "tool drift sweep", "evaluated", len(charts), "drift", drift)
	return nil
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
		runtimeLogger().WarnContext(ctx, "tool drift probe failed",
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
