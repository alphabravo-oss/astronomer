package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

// Condition types written by the server-side probe reconciler. These mirror
// the constants in internal/worker/tasks/health_check.go but live here too
// so the server can write them without depending on the worker package
// (which would create an import cycle through tunnel-aware code).
const (
	probeConditionAgentReachable    = "AgentReachable"
	probeConditionGatewayAPISupport = "GatewayAPISupported"
)

const (
	probeStatusTrue    = "True"
	probeStatusFalse   = "False"
	probeStatusUnknown = "Unknown"
)

// startClusterProbeReconciler runs probe-based cluster conditions
// (AgentReachable, GatewayAPISupported) from the server process. These
// require the tunnel-backed K8sRequester which only the server has — the
// worker process has no access to the tunnel registry, so its
// cluster:health_check task only maintains heartbeat-derived conditions.
//
// Ticks every 60s, iterates active clusters, runs cheap probes through the
// tunnel, and upserts one cluster_conditions row per (cluster_id, type).
// Probes are time-boxed and run sequentially per cluster to avoid hammering
// the tunnel on large fleets; this can be parallelized once we have proof
// of the right concurrency cap.
func startClusterProbeReconciler(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries, requester handler.K8sRequester) {
	if logger == nil || queries == nil || requester == nil {
		return
	}
	go func() {
		// Short initial delay so the first sweep doesn't race tunnel
		// registration on cold start.
		time.Sleep(5 * time.Second)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			if err := sweepClusterProbes(runCtx, logger, queries, requester); err != nil {
				logger.Warn("cluster probe sweep failed", "error", err)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sweepClusterProbes(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries, requester handler.K8sRequester) error {
	clusters, err := queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 500, Offset: 0})
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}
	for _, c := range clusters {
		probeOne(ctx, logger, queries, requester, c.ID)
	}
	return nil
}

func probeOne(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries, requester handler.K8sRequester, clusterID uuid.UUID) {
	upsert := func(condType, status, reason, message string) {
		if _, err := queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
			ClusterID: clusterID,
			Type:      condType,
			Status:    status,
			Reason:    reason,
			Message:   message,
		}); err != nil {
			logger.Warn("upsert cluster condition failed",
				"cluster_id", clusterID.String(), "type", condType, "error", err)
		}
	}

	// AgentReachable: round-trip GET /version through the tunnel. Cheap,
	// unauthenticated at the apiserver, and tells us both "agent is up" and
	// "agent can reach the cluster apiserver". Fail-closed on errors.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	resp, err := requester.Do(probeCtx, clusterID.String(), http.MethodGet, "/version", nil, nil)
	cancel()
	switch {
	case err != nil:
		upsert(probeConditionAgentReachable, probeStatusFalse, "ProbeError", err.Error())
	case resp == nil:
		upsert(probeConditionAgentReachable, probeStatusFalse, "EmptyResponse",
			"Agent returned no response.")
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		upsert(probeConditionAgentReachable, probeStatusTrue, "ProbeSucceeded",
			fmt.Sprintf("GET /version → %d.", resp.StatusCode))
	default:
		upsert(probeConditionAgentReachable, probeStatusFalse, "ProbeBadStatus",
			fmt.Sprintf("GET /version → %d.", resp.StatusCode))
	}

	// GatewayAPISupported: discovery probe for gateway.networking.k8s.io/v1.
	// 200 → True, 404 → False (CRDs not installed), anything else → Unknown.
	probeCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	resp, err = requester.Do(probeCtx, clusterID.String(), http.MethodGet,
		"/apis/gateway.networking.k8s.io/v1", nil, nil)
	cancel()
	switch {
	case err != nil:
		upsert(probeConditionGatewayAPISupport, probeStatusUnknown, "ProbeError", err.Error())
	case resp == nil:
		upsert(probeConditionGatewayAPISupport, probeStatusUnknown, "EmptyResponse",
			"Agent returned no response.")
	case resp.StatusCode == http.StatusNotFound:
		upsert(probeConditionGatewayAPISupport, probeStatusFalse, "CRDsMissing",
			"gateway.networking.k8s.io/v1 not registered on this cluster.")
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		upsert(probeConditionGatewayAPISupport, probeStatusTrue, "APIGroupPresent",
			"gateway.networking.k8s.io/v1 is registered.")
	default:
		upsert(probeConditionGatewayAPISupport, probeStatusUnknown, "ProbeBadStatus",
			fmt.Sprintf("Discovery probe returned %d.", resp.StatusCode))
	}
}
