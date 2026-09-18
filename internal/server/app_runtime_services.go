package server

import (
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	livemetrics "github.com/alphabravocompany/astronomer-go/internal/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func (c *productionComposition) startRuntimeServices(cfg *config.Config, logger *slog.Logger, foundation *runtimeFoundation, runtimeTasks *runtimeTaskComposition) error {
	projectCtx := runtimeTasks.core.Context(foundation.ctx)
	c.projectHandler.StartReconciler(projectCtx)
	go func() {
		time.Sleep(2 * time.Second)
		_ = runtimeTasks.project.HandleProjectReconcileAll(projectCtx, nil)
	}()

	livemetrics.New(c.bus, c.queries, c.clusterHandler.MetricsProvider(), logger).Start(foundation.ctx)
	tunnel.StartConnectionMetricsReporter(foundation.ctx, c.queries, logger)
	if localCluster, err := bootstrapLocalCluster(foundation.ctx, logger, c.queries); err != nil {
		logger.Warn("local cluster bootstrap failed", "error", err)
	} else if localCluster != nil {
		c.workloadHandler.SetLocalClusterID(localCluster.ID.String())
		if err := StartLocalAgentLeaderElection(foundation.ctx, logger, c.queries, localCluster.ID, cfg.DeliveryEnabled, cfg.DeliveryLocalFluxBootstrap); err != nil {
			logger.Warn("local agent start failed", "error", err)
		}
	}
	startClusterProbeReconciler(foundation.ctx, logger, c.queries, c.requester)
	if err := startCRDController(foundation.ctx, logger, cfg, c.queries); err != nil {
		return err
	}
	kickFirstBootCatalogSync(foundation.ctx, logger, c.queries, c.queue)
	catalogClients, catalogClientErr := catalog.NewSourceClients(catalog.SourceClientOptions{
		Timeout:             15 * time.Second,
		ProxyURL:            cfg.CatalogProxyURL,
		CAFile:              cfg.CatalogCAFile,
		AllowPrivateMirrors: cfg.CatalogAllowPrivateMirrors,
	})
	if catalogClientErr != nil {
		logger.Warn("catalog transport configuration failed; keeping existing rows", "error", catalogClientErr)
		return nil
	}
	var catalogTrust *model.TrustPolicy
	if cfg.CatalogSignatureRequired {
		policy := model.TrustPolicy{
			Provider: model.SignatureProvider(cfg.CatalogSignatureProvider),
			Identity: cfg.CatalogSignatureIdentity,
			Issuer:   cfg.CatalogSignatureIssuer,
			KeyRef:   cfg.CatalogSignatureKeyRef,
		}
		catalogTrust = &policy
	}
	if count, err := catalog.LoadSource(foundation.ctx, c.queries, catalog.SourceOptions{
		URL:            cfg.CatalogURL,
		ExpectedDigest: cfg.CatalogDigest,
		MirrorsJSON:    cfg.CatalogMirrors,
		Clients:        catalogClients,
		TrustPolicy:    catalogTrust,
		TrustDirectory: cfg.CatalogTrustDirectory,
	}); err != nil {
		logger.Warn("blessed catalog reconcile failed; keeping existing rows", "url", cfg.CatalogURL, "error", err)
	} else if count > 0 {
		logger.Info("blessed catalog reconciled", "entries", count, "url", cfg.CatalogURL)
	}
	return nil
}
