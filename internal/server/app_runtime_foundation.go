package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
)

type runtimeFoundation struct {
	server *Server
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *productionComposition) startRuntimeFoundation(cfg *config.Config, logger *slog.Logger, routed *routerComposition, lifecycles *charlieLifecycleGroup) (*runtimeFoundation, error) {
	s := &Server{
		handler: routed.router, logger: logger, db: c.database, queue: c.queue, hub: c.hub,
		Encryptor: c.encryptor, SSO: c.ssoManager, charlieRuntime: lifecycles, charlieBridge: c.managedCharlieBridge,
	}
	s.httpServer = &http.Server{
		Handler: wrapWithTracing(routed.router), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
	}
	reconcileCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	if lifecycles != nil {
		if err := lifecycles.Start(reconcileCtx); err != nil {
			c.database.Close()
			return nil, err
		}
	}
	if c.charlieFindingProjection != nil {
		go c.charlieFindingProjection.Run(reconcileCtx)
	}
	if reconciler := charlie.NewArtifactReconciler(c.queries, c.localK8s, c.charlieHelm); reconciler != nil {
		go reconciler.Run(reconcileCtx)
	}
	startReconcilers := func(ctx context.Context) {
		c.monitoringHandler.StartReconciler(ctx)
		c.backupHandler.StartReconciler(ctx)
		c.toolHandler.StartReconciler(ctx)
		c.catalogHandler.StartReconciler(ctx)
		c.loggingHandler.StartReconciler(ctx)
		c.controlPlaneHandler.StartEvaluator(ctx)
		c.workloadHandler.StartReconciler(ctx)
	}
	if cfg.ServerReplicas > 1 && c.database != nil {
		elector := leader.New(c.database.Pool(), logger)
		go runServerReconcilerLeader(reconcileCtx, elector, logger, startReconcilers)
	} else {
		startReconcilers(reconcileCtx)
	}
	c.connLimiter.StartJanitor(reconcileCtx, 0)
	if c.webhookTap != nil {
		c.webhookTap.Start(reconcileCtx)
	}
	if c.siemTap != nil {
		c.siemTap.Start(reconcileCtx)
	}
	return &runtimeFoundation{server: s, ctx: reconcileCtx, cancel: cancel}, nil
}
