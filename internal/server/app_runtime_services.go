package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func (c *productionComposition) startRuntimeServices(cfg *config.Config, logger *slog.Logger, foundation *runtimeFoundation, runtimeTasks *runtimeTaskComposition) error {
	projectCtx := runtimeTasks.core.Context(foundation.ctx)
	if err := c.runtime.GoLoop("project-reconciler", true, func(context.Context) {
		c.projectHandler.RunReconciler(projectCtx)
	}); err != nil {
		return err
	}
	if err := c.runtime.Task("project-initial-reconcile", func(ctx context.Context) error {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return runtimeTasks.project.HandleProjectReconcileAll(projectCtx, nil)
		}
	}); err != nil {
		return err
	}

	if err := c.runtime.GoLoop("connection-metrics-reporter", true, func(ctx context.Context) {
		tunnel.RunConnectionMetricsReporter(ctx, c.queries, logger)
	}); err != nil {
		return err
	}
	if localCluster, err := bootstrapLocalCluster(foundation.ctx, logger, c.queries); err != nil {
		logger.Warn("local cluster bootstrap failed", "error", err)
	} else if localCluster != nil {
		c.workloadHandler.SetLocalClusterID(localCluster.ID.String())
		localAgent, err := buildLocalAgentLeaderRuntime(logger, c.queries, localCluster.ID, helmruntime.Config{
			Driver: cfg.HelmDriver, RegistryConfig: cfg.HelmRegistryConfig,
			RepositoryConfig: cfg.HelmRepositoryConfig, RepositoryCache: cfg.HelmRepositoryCache,
			PluginsDirectory: cfg.HelmPluginsDirectory, BurstLimit: 100,
		}, cfg.PodNamespace, cfg.ProcessHostname)
		if err != nil {
			logger.Warn("local agent start failed", "error", err)
		} else if localAgent != nil {
			if err := c.runtime.Go("embedded-local-agent", true, localAgent); err != nil {
				return err
			}
		}
	}
	crdRunner, err := buildCRDControllerRunner(
		logger,
		cfg,
		c.queries,
		sqlcMutationTxRunner[crdClusterDecommissionMutationTx](c.database),
	)
	if err != nil {
		return err
	}
	if crdRunner != nil {
		if err := c.runtime.Go("crd-controller", true, crdRunner); err != nil {
			return err
		}
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
