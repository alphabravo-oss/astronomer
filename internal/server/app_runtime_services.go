package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
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
		tunnel.RunConnectionMetricsCollector(ctx, func(ctx context.Context) ([]sqlc.ListClusterConnectionStatusRow, error) {
			return collectConnectionMetricsSnapshot(ctx, c.database.Pool().BeginTx)
		}, logger)
	}); err != nil {
		return err
	}
	if err := c.runtime.GoLoop("registration-metrics-reporter", true, func(ctx context.Context) {
		runRegistrationMetricsCollector(ctx, c.database.Pool().BeginTx, logger)
	}); err != nil {
		return err
	}
	if err := c.runtime.GoLoop("vault-metrics-reporter", true, func(ctx context.Context) {
		runVaultMetricsCollector(ctx, c.database.Pool().BeginTx, logger)
	}); err != nil {
		return err
	}
	if err := c.runtime.GoLoop("metric-generation-reporter", true, func(ctx context.Context) {
		observability.RunMetricGenerationCollector(ctx, c.database.Pool().BeginTx, []observability.MetricOwnerFamily{observability.MetricSSOProvider, observability.MetricSIEMForwarder, observability.MetricCluster})
	}); err != nil {
		return err
	}
	if cfg.DeliveryEnabled && cfg.DeliveryLocalFluxBootstrap {
		if err := c.runtime.Task("local-flux-bootstrap", func(ctx context.Context) error {
			return ensureLocalFluxUntilReady(ctx, logger)
		}); err != nil {
			return err
		}
	}
	if localCluster, err := bootstrapLocalCluster(foundation.ctx, logger, c.queries); err != nil {
		logger.Warn("local cluster bootstrap failed", "error", err)
	} else if localCluster != nil {
		c.workloadHandler.SetLocalClusterID(localCluster.ID.String())
		localAgent, err := buildLocalAgentLeaderRuntime(logger, c.queries, localCluster.ID, helmRuntimeFromConfig(cfg), cfg.PodNamespace, cfg.ProcessHostname, localAgentDeliveryConfig{
			Enabled:            cfg.DeliveryEnabled,
			Namespace:          cfg.PodNamespace,
			SystemOIDCIssuer:   cfg.DeliveryFluxDistributionOIDCIssuer,
			SystemOIDCIdentity: cfg.DeliveryFluxDistributionCertificateIdentity,
			SystemPublicKeys:   cfg.DeliveryFluxDistributionKeyring,
		})
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
	return c.startApplicationCatalog(foundation.ctx, cfg, logger)
}

func (c *productionComposition) startApplicationCatalog(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	networkPolicy, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil {
		return err
	}
	if networkPolicy.Restricted && cfg.CatalogURL != "" && (!strings.HasPrefix(cfg.CatalogURL, "oci://") || !strings.Contains(cfg.CatalogURL, "@sha256:") || cfg.CatalogDigest == "" || !cfg.CatalogSignatureRequired) {
		return errors.New("disconnected application catalog requires an immutable authenticated source or disabled-unavailable configuration")
	}
	if !networkPolicy.Restricted && cfg.CatalogURL != "" {
		kickFirstBootCatalogSync(ctx, logger, c.queries, c.queue)
	}
	if networkPolicy.Restricted && cfg.CatalogURL == "" {
		logger.Info("application catalog unavailable in disconnected mode", "blocked_category", "application_catalog")
		return nil
	}
	catalogClients, catalogClientErr := catalog.NewSourceClients(catalog.SourceClientOptions{
		DestinationPolicy:   &networkPolicy,
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
	if count, err := catalog.LoadSource(ctx, c.queries, catalog.SourceOptions{
		URL:            cfg.CatalogURL,
		ExpectedDigest: cfg.CatalogDigest,
		MirrorsJSON:    cfg.CatalogMirrors,
		Clients:        catalogClients,
		TrustPolicy:    catalogTrust,
		TrustDirectory: cfg.CatalogTrustDirectory,
	}); err != nil {
		logger.Warn("blessed catalog reconcile failed; keeping existing rows", "error", err)
	} else if count > 0 {
		logger.Info("blessed catalog reconciled", "entries", count)
	}
	return nil
}
