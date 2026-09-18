package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

type routerComposition struct {
	deps            RouterDependencies
	router          http.Handler
	deferredRuntime tasks.DeferredRuntime
}

func (c *productionComposition) composeRouter(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*routerComposition, error) {
	deps, err := c.composeCoreRouterDependencies(cfg, logger)
	if err != nil {
		return nil, err
	}
	c.composeAdministrativeRouterDependencies(cfg, &deps)
	c.composeManagementRouterDependencies(cfg, logger, &deps)
	c.composePlatformRouterDependencies(cfg, logger, &deps)
	c.configureRouterPolicies(ctx, logger, &deps)
	c.configureRouterStreamAdapters(logger, &deps)

	if c.nativeRBACAuthz != nil {
		deps.ClusterResources.NativeAuthz = c.nativeRBACAuthz
		deps.ClusterResources.NativeRBAC = c.nativeRBACHandler
	}
	if err := validateManagementBackupStartup(cfg.ManagementBackupEnabled, deps.AdminPlatform.AdminDrill); err != nil {
		c.database.Close()
		return nil, err
	}

	router, err := NewProductionRouter(cfg, deps)
	if err != nil {
		c.database.Close()
		return nil, err
	}
	deferredReplayers, err := newDeferredHTTPReplayers(router, c.jwtManager, c.encryptor, c.queries)
	if err != nil {
		c.database.Close()
		return nil, err
	}
	deferredRuntime := tasks.DeferredRuntime{Deps: tasks.DeferredDispatchDeps{Queries: c.queries, Replayers: deferredReplayers}}
	return &routerComposition{deps: deps, router: router, deferredRuntime: deferredRuntime}, nil
}

func (c *productionComposition) composeAdministrativeRouterDependencies(cfg *config.Config, deps *RouterDependencies) {
	platformHealth := handler.NewPlatformHealthHandler(c.database.Pool())
	platformHealth.SetAsynqInspector(asynq.NewInspector(c.redisOpt))

	adminQueues := handler.NewAdminQueuesHandler(asynq.NewInspector(c.redisOpt), c.queries)
	adminQueues.SetEventBus(c.bus)
	adminQueues.SetRunTx(sqlcMutationTxRunner[handler.AdminQueueMutationTx](c.database))

	adminTaskOutbox := handler.NewAdminTaskOutboxHandler(c.queries)
	adminTaskOutbox.SetRunTx(sqlcMutationTxRunner[handler.AdminTaskOutboxMutationTx](c.database))

	deps.AdminPlatform.PlatformHealth = platformHealth
	deps.AdminPlatform.AdminQueues = adminQueues
	deps.AdminPlatform.AdminTaskOutbox = adminTaskOutbox
	deps.AdminPlatform.AdminDrill = newManagementBackupHandler(
		cfg,
		c.queries,
		c.database,
		c.encryptor,
		c.localK8s,
		c.localNamespace,
	)
}
