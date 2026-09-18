package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
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

	clusterProbePageSize    = int32(500)
	clusterProbeConcurrency = 32
)

type clusterProbeQuerier interface {
	ListClusterProbeTargets(ctx context.Context, arg sqlc.ListClusterProbeTargetsParams) ([]uuid.UUID, error)
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
}

// runClusterProbeReconciler maintains probe-based cluster conditions
// (AgentReachable, GatewayAPISupported) from the server process. These require
// the tunnel-backed K8sRequester which only the server has. It is owned and
// joined by the server runtime instead of starting an untracked goroutine.
func runClusterProbeReconciler(ctx context.Context, logger *slog.Logger, queries clusterProbeQuerier, requester handler.K8sRequester) {
	if logger == nil || queries == nil || requester == nil {
		return
	}
	// Short initial delay so the first sweep doesn't race tunnel registration
	// on cold start.
	initial := time.NewTimer(5 * time.Second)
	select {
	case <-ctx.Done():
		initial.Stop()
		return
	case <-initial.C:
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	var cursor uuid.UUID
	for {
		runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err := sweepClusterProbes(runCtx, logger, queries, requester, &cursor); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("cluster probe sweep failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sweepClusterProbes(ctx context.Context, logger *slog.Logger, queries clusterProbeQuerier, requester handler.K8sRequester, cursor *uuid.UUID) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		clusters, err := queries.ListClusterProbeTargets(ctx, sqlc.ListClusterProbeTargetsParams{
			PageSize: clusterProbePageSize, AfterID: *cursor,
		})
		if err != nil {
			return fmt.Errorf("list cluster probe targets: %w", err)
		}
		if err := fanOutClusterProbes(ctx, logger, queries, requester, clusters, cursor); err != nil {
			return err
		}
		if len(clusters) < int(clusterProbePageSize) {
			*cursor = uuid.Nil
			return nil
		}
	}
}

func fanOutClusterProbes(ctx context.Context, logger *slog.Logger, queries clusterProbeQuerier, requester handler.K8sRequester, clusters []uuid.UUID, cursor *uuid.UUID) error {
	sem := make(chan struct{}, clusterProbeConcurrency)
	var wg sync.WaitGroup
	for _, clusterID := range clusters {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		// Cancellation can win concurrently with an available semaphore slot.
		if err := ctx.Err(); err != nil {
			<-sem
			wg.Wait()
			return err
		}
		// Advance on dispatch, including timed-out probes. Retrying the first
		// slow page every tick would starve the rest of the estate forever.
		*cursor = clusterID
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			probeOne(ctx, logger, queries, requester, clusterID)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

func probeOne(ctx context.Context, logger *slog.Logger, queries clusterProbeQuerier, requester handler.K8sRequester, clusterID uuid.UUID) {
	// Positive machine-origin marker: this is a background health probe with no
	// human anywhere near it (design doc §10.5, agent lifecycle / health). It is
	// stamped rather than left to fall through as "no user", because §7
	// invariant 4 forbids inferring machine from the absence of a user.
	ctx = callerid.WithMachine(ctx, callerid.SourceAgentLifecycle)
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
	if ctx.Err() != nil {
		return
	}
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
	if ctx.Err() != nil {
		return
	}
	probeCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	resp, err = requester.Do(probeCtx, clusterID.String(), http.MethodGet,
		"/apis/gateway.networking.k8s.io/v1", nil, nil)
	cancel()
	if ctx.Err() != nil {
		return
	}
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
