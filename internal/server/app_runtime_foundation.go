package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	livemetrics "github.com/alphabravocompany/astronomer-go/internal/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
)

type runtimeFoundation struct {
	server *Server
	ctx    context.Context
}

func (c *productionComposition) startRuntimeFoundation(cfg *config.Config, logger *slog.Logger, routed *routerComposition, lifecycles *charlieLifecycleGroup) (*runtimeFoundation, error) {
	s := &Server{
		handler: routed.router, logger: logger, db: c.database, queue: c.queue, hub: c.hub,
		Encryptor: c.encryptor, SSO: c.ssoManager, charlieRuntime: lifecycles, charlieBridge: c.managedCharlieBridge,
		taskLeader: c.taskLeader, runtime: c.runtime,
	}
	cleanupOnError := true
	defer func() {
		if !cleanupOnError {
			return
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = s.Shutdown(shutdownCtx)
	}()
	s.httpServer = &http.Server{
		Handler: wrapWithTracing(routed.router), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
	}
	if c.runtime == nil {
		return nil, fmt.Errorf("runtime supervisor is unavailable")
	}
	if err := c.runtime.Start(context.Background()); err != nil {
		return nil, err
	}
	reconcileCtx := c.runtime.ctx
	if c.securityCacheCoordinator != nil && c.runtimeRedisClient != nil {
		if err := c.runtime.GoLoop("security-cache-invalidation", true, c.securityCacheCoordinator.Run); err != nil {
			return nil, err
		}
		s.addResourceCloser("security cache Redis", func(context.Context) error {
			return c.runtimeRedisClient.Close()
		})
	}
	if c.eventRelayClient != nil {
		if err := c.runtime.GoLoop("event-redis-relay", true, c.bus.StartRedisRelay); err != nil {
			return nil, err
		}
		s.addResourceCloser("event relay Redis", func(context.Context) error {
			return c.eventRelayClient.Close()
		})
	}
	if c.queries != nil {
		if err := c.runtime.GoLoop("quota-usage-reporter", true, func(ctx context.Context) {
			quota.RunReporter(ctx, c.queries, logger)
		}); err != nil {
			return nil, err
		}
	}
	if lifecycles != nil {
		if err := lifecycles.Start(reconcileCtx); err != nil {
			return nil, err
		}
		if err := c.runtime.Go("charlie-lifecycle", true, lifecycles.Run); err != nil {
			return nil, err
		}
	}
	if c.charlieFindingProjection != nil {
		if err := c.runtime.GoLoop("charlie-finding-projection", true, c.charlieFindingProjection.Run); err != nil {
			return nil, err
		}
	}
	if reconciler := charlie.NewArtifactReconciler(c.queries, c.localK8s, c.charlieHelm); reconciler != nil {
		if err := c.runtime.GoLoop("charlie-artifact-reconciler", true, reconciler.Run); err != nil {
			return nil, err
		}
	}
	metricsPublisher := livemetrics.New(c.bus, c.queries, c.clusterHandler.MetricsProvider(), logger)
	runReconcilers := func(ctx context.Context) error {
		return runRuntimeLoopGroup(ctx,
			namedRuntimeLoop{name: "monitoring", run: c.monitoringHandler.RunReconciler},
			namedRuntimeLoop{name: "backup", run: c.backupHandler.RunReconciler},
			namedRuntimeLoop{name: "tool", run: c.toolHandler.RunReconciler},
			namedRuntimeLoop{name: "catalog", run: c.catalogHandler.RunReconciler},
			namedRuntimeLoop{name: "logging", run: c.loggingHandler.RunReconciler},
			namedRuntimeLoop{name: "control-plane", run: c.controlPlaneHandler.RunEvaluator},
			namedRuntimeLoop{name: "workload", run: c.workloadHandler.RunReconciler},
			namedRuntimeLoop{name: "live-metrics", run: metricsPublisher.Run},
			namedRuntimeLoop{name: "cluster-probes", run: func(ctx context.Context) {
				runClusterProbeReconciler(ctx, logger, c.queries, c.requester)
			}},
		)
	}
	if cfg.ServerReplicas > 1 && c.database != nil {
		if err := c.runtime.Go("leader-reconcilers", true, func(ctx context.Context) error {
			return runServerReconcilerLeader(ctx, c.taskLeader, logger, runReconcilers)
		}); err != nil {
			return nil, err
		}
	} else {
		if err := c.runtime.Go("reconcilers", true, runReconcilers); err != nil {
			return nil, err
		}
	}
	if err := c.runtime.GoLoop("connection-failure-janitor", true, func(ctx context.Context) {
		c.connLimiter.RunJanitor(ctx, 0)
	}); err != nil {
		return nil, err
	}
	if c.webhookTap != nil {
		if err := c.runtime.GoLoop("webhook-event-tap", true, c.webhookTap.Run); err != nil {
			return nil, err
		}
	}
	if c.siemTap != nil {
		if err := c.runtime.GoLoop("siem-event-tap", true, c.siemTap.Run); err != nil {
			return nil, err
		}
	}
	cleanupOnError = false
	return &runtimeFoundation{server: s, ctx: reconcileCtx}, nil
}
