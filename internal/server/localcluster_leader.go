package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
)

const localAgentLeaderLease = "astronomer-local-agent"

// buildLocalAgentLeaderRuntime ensures exactly one HA server replica owns the
// singleton management-cluster tunnel. The lease context cancels every local
// agent loop before another replica can acquire leadership.
func buildLocalAgentLeaderRuntime(
	logger *slog.Logger,
	queries *sqlc.Queries,
	clusterID uuid.UUID,
	helmRuntime helmruntime.Config,
	namespace string,
	identity string,
	deliveryConfig localAgentDeliveryConfig,
) (func(context.Context) error, error) {
	if logger == nil {
		logger = slog.Default()
	}
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Warn("local agent disabled: not running in-cluster", "error", err)
		return nil, nil
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create local agent leader-election client: %w", err)
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "astronomer"
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		identity = uuid.NewString()
	}
	lock, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		namespace,
		localAgentLeaderLease,
		client.CoreV1(),
		client.CoordinationV1(),
		resourcelock.ResourceLockConfig{Identity: identity},
	)
	if err != nil {
		return nil, fmt.Errorf("create local agent leader lease: %w", err)
	}

	return func(ctx context.Context) error {
		electionCtx, stopElection := context.WithCancel(ctx)
		defer stopElection()
		runtimeErr := make(chan error, 1)
		reportRuntimeError := func(err error) {
			select {
			case runtimeErr <- err:
			default:
			}
			stopElection()
		}
		config := leaderelection.LeaderElectionConfig{
			Lock:          lock,
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
			// A released lease can be acquired before the guarded goroutine has
			// observed cancellation. Let the short lease expire instead.
			ReleaseOnCancel: false,
			Name:            localAgentLeaderLease,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(leaderCtx context.Context) {
					logger.Info("local agent leadership acquired", "identity", identity)
					localAgent, buildErr := buildLocalAgentRuntime(
						leaderCtx,
						logger,
						queries,
						clusterID,
						helmRuntime,
						deliveryConfig,
					)
					if buildErr != nil {
						reportRuntimeError(fmt.Errorf("build local agent runtime: %w", buildErr))
						return
					}
					if localAgent == nil {
						reportRuntimeError(fmt.Errorf("build local agent runtime: no runtime returned"))
						return
					}
					if runErr := localAgent(leaderCtx); runErr != nil && leaderCtx.Err() == nil {
						reportRuntimeError(fmt.Errorf("run local agent: %w", runErr))
						return
					}
					if leaderCtx.Err() == nil {
						reportRuntimeError(fmt.Errorf("local agent runtime exited without cancellation"))
					}
				},
				OnStoppedLeading: func() {
					if ctx.Err() == nil {
						logger.Warn("local agent leadership lost", "identity", identity)
					} else {
						logger.Info("local agent leadership stopped", "identity", identity)
					}
				},
				OnNewLeader: func(newIdentity string) {
					if newIdentity != identity {
						logger.Info("local agent leader elected", "identity", newIdentity)
					}
				},
			},
		}
		elector, err := leaderelection.NewLeaderElector(config)
		if err != nil {
			return fmt.Errorf("configure local agent leader election: %w", err)
		}
		elector.Run(electionCtx)
		select {
		case err := <-runtimeErr:
			return err
		default:
		}
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("local agent leader election stopped unexpectedly")
	}, nil
}
