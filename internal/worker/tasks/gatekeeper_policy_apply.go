package tasks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

// GatekeeperPolicyApplyType is the periodic task that delivers the starter
// Gatekeeper policy bundle to clusters that have Gatekeeper installed. Runs on
// the tunnel queue (server process) because it needs the agent K8sRequester.
const GatekeeperPolicyApplyType = "gatekeeper:policy_apply"

const gatekeeperPolicyFieldManager = "astronomer-gatekeeper-policy"

// gatekeeperPolicySweepDeadline caps the whole fan-out;
// gatekeeperPolicyPerClusterTimeout caps a single cluster's probe+apply. The
// aggregate deadline is well under the 5m cadence so a stuck sweep is abandoned
// before the next tick; the per-cluster timeout keeps one slow agent from
// consuming the budget.
const (
	gatekeeperPolicySweepDeadline     = 4 * time.Minute
	gatekeeperPolicyPerClusterTimeout = 20 * time.Second
)

// NewGatekeeperPolicyApplyTask builds a fresh task handle.
func NewGatekeeperPolicyApplyTask() *asynq.Task {
	return asynq.NewTask(GatekeeperPolicyApplyType, nil, asynq.MaxRetry(2))
}

// HandleGatekeeperPolicyApply server-side-applies the embedded Gatekeeper policy
// bundle to every connected cluster that has Gatekeeper installed (detected by
// the presence of the constrainttemplates API). It is idempotent and skips
// clusters without Gatekeeper, so it never errors on a cluster that opted out.
//
// On a cluster's first pass the ConstraintTemplates apply but their generated
// constraint CRDs may not exist yet, so the Constraint resources are rejected;
// the next sweep (the templates having reconciled) applies them. This eventual
// convergence is intentional — re-apply is cheap and idempotent.
func HandleGatekeeperPolicyApply(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, GatekeeperPolicyApplyType, func() error {
		// F6: bound the whole sweep so an interval overrun can't run for many
		// minutes while the scheduler keeps enqueuing. Shorter than the 5m
		// cadence so a stuck tail is abandoned before the next tick.
		ctx, cancel := context.WithTimeout(ctx, gatekeeperPolicySweepDeadline)
		defer cancel()

		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "gatekeeper policy runtime not configured, skipping")
			return nil
		}
		if runtimeDeps.K8s == nil {
			// Only the server process holds the tunnel requester.
			runtimeLogger().DebugContext(ctx, "gatekeeper policy: no tunnel requester, skipping")
			return nil
		}
		manifests, err := gatekeeperpolicy.Manifests()
		if err != nil {
			return fmt.Errorf("load gatekeeper bundle: %w", err)
		}
		clusters, err := runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 1000, Offset: 0})
		if err != nil {
			return fmt.Errorf("list clusters: %w", err)
		}
		// F6: fan out the per-cluster tunnel probe + apply with bounded
		// concurrency and a per-cluster timeout so one slow/disconnected agent
		// is skipped-with-log instead of stalling the whole 5m tick. A tunnel
		// error already reads as "not installed" and skips the cluster.
		fanOutClusters(ctx, clusters, gatekeeperPolicyPerClusterTimeout, func(ctx context.Context, c sqlc.Cluster) {
			if !gatekeeperInstalled(ctx, c.ID) {
				return
			}
			applyGatekeeperBundle(ctx, c.ID, manifests)
		})
		return nil
	})
}

// gatekeeperInstalled returns true when the cluster serves the Gatekeeper
// constraint-template API. A tunnel error (disconnected cluster) reads as
// "not installed" so the sweep simply skips it.
func gatekeeperInstalled(ctx context.Context, clusterID uuid.UUID) bool {
	resp, err := runtimeDeps.K8s.Do(ctx, clusterID.String(), http.MethodGet, "/apis/templates.gatekeeper.sh/v1/constrainttemplates", nil, nil)
	return err == nil && resp != nil && resp.StatusCode == http.StatusOK
}

func applyGatekeeperBundle(ctx context.Context, clusterID uuid.UUID, manifests []gatekeeperpolicy.Manifest) {
	applied := 0
	for _, m := range manifests {
		path := kubeutil.ServerSideApplyPath(m.APIPath(), kubeutil.ApplyOptions{
			FieldManager: gatekeeperPolicyFieldManager,
			Force:        true,
		})
		resp, err := runtimeDeps.K8s.Do(ctx, clusterID.String(), http.MethodPatch, path, m.JSON, kubeutil.ApplyPatchHeaders())
		if err != nil {
			runtimeLogger().WarnContext(ctx, "gatekeeper policy apply failed",
				"cluster", clusterID.String(), "resource", m.Kind+"/"+m.Name, "error", err)
			continue
		}
		if resp.StatusCode >= http.StatusBadRequest {
			// A 404 here is the expected first-pass case (constraint CRD not yet
			// created by Gatekeeper); the next sweep converges.
			runtimeLogger().DebugContext(ctx, "gatekeeper policy apply rejected (will retry next sweep)",
				"cluster", clusterID.String(), "resource", m.Kind+"/"+m.Name, "status", resp.StatusCode)
			continue
		}
		applied++
	}
	runtimeLogger().InfoContext(ctx, "gatekeeper policy bundle applied",
		"cluster", clusterID.String(), "applied", applied, "total", len(manifests))
}
