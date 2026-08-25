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
	deps := c.composeCoreRouterDependencies(cfg, logger)
	c.composeAdministrativeRouterDependencies(cfg, &deps)
	c.composeManagementRouterDependencies(cfg, logger, &deps)
	c.composePlatformRouterDependencies(cfg, logger, &deps)
	c.configureRouterPolicies(ctx, logger, &deps)
	c.configureRouterStreams(cfg, logger, &deps)

	if c.nativeRBACAuthz != nil {
		deps.NativeAuthz = c.nativeRBACAuthz
		deps.NativeRBAC = c.nativeRBACHandler
	}
	if err := validateProductionSecurityWiring(cfg, deps); err != nil {
		c.database.Close()
		return nil, err
	}
	if err := validateManagementBackupStartup(cfg.ManagementBackupEnabled, deps.AdminDrill); err != nil {
		c.database.Close()
		return nil, err
	}

	router := NewRouter(cfg, deps)
	deferredReplayers, err := newDeferredHTTPReplayers(router, c.jwtManager, c.encryptor, c.queries)
	if err != nil {
		c.database.Close()
		return nil, err
	}
	deferredRuntime := tasks.DeferredRuntime{Deps: tasks.DeferredDispatchDeps{Queries: c.queries, Replayers: deferredReplayers}}
	return &routerComposition{deps: deps, router: router, deferredRuntime: deferredRuntime}, nil
}

func (c *productionComposition) composeAdministrativeRouterDependencies(cfg *config.Config, deps *RouterDependencies) {
	database := c.database
	queries := c.queries
	redisOpt := c.redisOpt
	bus := c.bus
	encryptor := c.encryptor
	localK8s := c.localK8s
	localNamespace := c.localNamespace

	admin := RouterDependencies{
		PlatformHealth: func() *handler.PlatformHealthHandler {
			h := handler.NewPlatformHealthHandler(database.Pool())
			h.SetAsynqInspector(asynq.NewInspector(redisOpt))
			return h
		}(),
		AdminQueues: func() *handler.AdminQueuesHandler {
			h := handler.NewAdminQueuesHandler(asynq.NewInspector(redisOpt), queries)
			h.SetEventBus(bus)
			h.SetRunTx(sqlcMutationTxRunner[handler.AdminQueueMutationTx](database))
			return h
		}(),
		AdminTaskOutbox: func() *handler.AdminTaskOutboxHandler {
			h := handler.NewAdminTaskOutboxHandler(queries)
			h.SetRunTx(sqlcMutationTxRunner[handler.AdminTaskOutboxMutationTx](database))
			return h
		}(),
		AdminDrill: newManagementBackupHandler(cfg.ManagementBackupEnabled, queries, database, encryptor, localK8s, localNamespace),
	}
	deps.PlatformHealth = admin.PlatformHealth
	deps.AdminQueues = admin.AdminQueues
	deps.AdminTaskOutbox = admin.AdminTaskOutbox
	deps.AdminDrill = admin.AdminDrill
}
