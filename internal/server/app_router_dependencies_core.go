package server

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func (c *productionComposition) composeCoreRouterDependencies(cfg *config.Config, logger *slog.Logger) RouterDependencies {
	database := c.database
	queries := c.queries
	requester := c.requester
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	bus := c.bus
	hub := c.hub
	queue := c.queue

	return RouterDependencies{
		JWT:                 c.jwtManager,
		Encryptor:           c.encryptor,
		AuthQueries:         queries,
		AuditWriter:         queries,
		StreamTickets:       c.streamTicketHandler,
		StreamTicketStore:   c.streamTickets,
		Auth:                c.authHandler,
		TOTP:                c.totpHandler,
		SSO:                 c.ssoHandler,
		Clusters:            c.clusterHandler,
		ClusterTemplates:    c.clusterTemplateHandler,
		ClusterRegistration: c.clusterRegistrationHandler,
		ClusterRegistries:   c.clusterRegistriesHandler,
		NetworkPolicies:     c.networkPoliciesHandler,
		Gatekeeper: func() *handler.GatekeeperConstraintsHandler {
			h := handler.NewGatekeeperConstraintsHandler(queries, requester)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetAuditWriter(queries)
			h.SetRunTx(sqlcMutationTxRunner[handler.GatekeeperConstraintMutationTx](database))
			return h
		}(),
		ClusterSnapshots:      c.clusterSnapshotsHandler,
		ControlPlaneSnapshots: c.controlPlaneSnapshotHandler,
		Projects:              c.projectHandler,
		DeliverySources:       c.deliverySourceHandler,
		DeliveryBundles:       c.deliveryBundleHandler,
		DeliveryTargets:       c.deliveryTargetHandler,
		DeliveryRollouts:      c.deliveryRolloutHandler,
		DeliveryDeployments:   deliveryhandler.NewDeploymentHandler(queries, c.deliveryDeploymentController, bus),
		DeliveryInventory:     deliveryhandler.NewInventoryHandler(queries),
		DeliverySystem:        c.deliverySystemRolloutHandler,
		Tools:                 c.toolHandler,
		Audit:                 handler.NewAuditHandler(queries),
		Alerting:              c.alertingHandler,
		Anomaly:               c.anomalyHandler,
		Backups:               c.backupHandler,
		Catalog:               c.catalogHandler,
		ChartRatings: func() *handler.ChartRatingsHandler {
			h := handler.NewChartRatingsHandler(queries)
			h.SetLogger(logger)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetRunTx(sqlcMutationTxRunner[handler.ChartRatingMutationTx](database))
			return h
		}(),
		Logging:        c.loggingHandler,
		Monitoring:     c.monitoringHandler,
		ControlPlane:   c.controlPlaneHandler,
		Resources:      c.resourceHandler,
		PlatformCharts: c.platformCharts,
		Docs:           handler.NewDocsHandler(),
		SSOPresets:     handler.NewSSOPresetsHandler(),
		ResourcesSearch: func() *handler.ResourcesSearchHandler {
			h := handler.NewResourcesSearchHandler(queries, requester)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetAuditWriter(queries)
			return h
		}(),
		ClusterAgent: func() *handler.ClusterAgentHandler {
			h := handler.NewClusterAgentHandler(queries)
			h.SetRunTx(sqlcMutationTxRunner[handler.ClusterAgentMutationTx](database))
			h.SetAgentUpgradeTarget(cfg.AgentImageRepository, cfg.AgentImageTag)
			h.SetK8sRequester(requester)
			h.SetEventBus(bus)
			return h
		}(),
		Readyz: newReadinessHandler(database, queue, hub).
			withLocatorError(c.locatorReadinessErr).
			withSecurityCacheCoordinator(c.securityCacheCoordinator, cfg.ServerReplicas > 1),
		DexConfig: c.dexHandler,
		RBAC: func() *handler.RBACHandler {
			h := handler.NewRBACHandler(queries)
			h.SetRunTx(sqlcMutationTxRunner[handler.RBACMutationTx](database))
			h.SetAuthorization(rbacEngine, rbacQuerier)
			catalog, err := rbac.LoadCatalog()
			if err != nil {
				logger.Error("failed to load RBAC template catalog", "error", err)
			} else {
				h.SetTemplateCatalog(catalog)
			}
			return h
		}(),
		RBACQueries:         rbacQuerier,
		RBACEngine:          rbacEngine,
		NamespaceScopedRBAC: cfg.NamespaceScopedRBACEnabled,
		Security:            c.securityHandler,
		ApiserverAudit:      c.apiserverAuditHandler,
		ApiserverAllowlist:  c.apiserverAllowlistHandler,
		ImageVulns: func() *handler.ImageVulnHandler {
			h := handler.NewImageVulnHandler(queries)
			h.SetK8sRequester(requester)
			h.SetRunTx(sqlcMutationTxRunner[handler.ImageVulnMutationTx](database))
			return h
		}(),
		ServiceProxy: func() *handler.ServiceProxyHandler {
			h := handler.NewServiceProxyHandler(requester)
			h.SetToolQuerier(queries)
			h.SetAuditWriter(queries)
			return h
		}(),
		Workloads:     c.workloadHandler,
		Hub:           hub,
		Proxy:         tunnel.NewProxyHandler(hub, logger),
		InternalK8s:   newInternalK8sHandler(c, cfg, logger),
		InternalHelm:  newInternalHelmHandler(c, cfg, logger),
		Exec:          tunnel.NewExecConsumer(hub, logger),
		Logs:          tunnel.NewLogsConsumer(hub, logger),
		RemoteServer:  c.remoteServer,
		RemoteQueries: queries,
		EventStream:   handler.NewEventStreamHandler(bus),
	}
}

func newInternalK8sHandler(c *productionComposition, cfg *config.Config, logger *slog.Logger) *tunnel.InternalK8sHandler {
	h := tunnel.NewInternalK8sHandler(c.hub, tunnel.DerivePSK(cfg.EncryptionKey), logger)
	h.SetAuditWriter(c.queries)
	return h
}

func newInternalHelmHandler(c *productionComposition, cfg *config.Config, logger *slog.Logger) *tunnel.InternalHelmHandler {
	h := tunnel.NewInternalHelmHandler(c.hub, tunnel.DerivePSK(cfg.EncryptionKey), logger)
	h.SetAuditWriter(c.queries)
	return h
}
