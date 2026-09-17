package server

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/principal"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func (c *productionComposition) composeCoreRouterDependencies(cfg *config.Config, logger *slog.Logger) (RouterDependencies, error) {
	database := c.database
	queries := c.queries
	requester := c.requester
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	bus := c.bus
	hub := c.hub
	queue := c.queue

	rbacHandler := handler.NewRBACHandler(queries)
	rbacHandler.SetRunTx(sqlcMutationTxRunner[handler.RBACMutationTx](database))
	rbacHandler.SetAuthorization(rbacEngine, rbacQuerier)
	roleCatalog, err := rbac.LoadCatalog()
	if err != nil {
		logger.Error("failed to load RBAC template catalog", "error", err)
	} else {
		rbacHandler.SetTemplateCatalog(roleCatalog)
	}

	principals := handler.NewPrincipalHandler(queries, principal.NewDirectory(principal.NewLDAPAdapter()))
	principals.SetEncryptor(c.encryptor)
	principals.SetRunTx(sqlcMutationTxRunner[handler.PrincipalMutationTx](database))

	gatekeeper := handler.NewGatekeeperConstraintsHandler(queries, requester)
	gatekeeper.SetAuthorization(rbacEngine, rbacQuerier)
	gatekeeper.SetRunTx(sqlcMutationTxRunner[handler.GatekeeperConstraintMutationTx](database))

	resourceSearch := handler.NewResourcesSearchHandler(queries, requester)
	resourceSearch.SetAuthorization(rbacEngine, rbacQuerier)
	resourceSearch.SetAuditWriter(queries)

	clusterAgent := handler.NewClusterAgentHandler(queries)
	clusterAgent.SetRunTx(sqlcMutationTxRunner[handler.ClusterAgentMutationTx](database))
	clusterAgent.SetAgentUpgradeTarget(cfg.AgentImageRepository, cfg.AgentImageTag)
	clusterAgent.SetK8sRequester(requester)
	clusterAgent.SetEventBus(bus)

	imageVulns := handler.NewImageVulnHandler(queries)
	imageVulns.SetK8sRequester(requester)
	imageVulns.SetRunTx(sqlcMutationTxRunner[handler.ImageVulnMutationTx](database))

	serviceProxy := handler.NewServiceProxyHandler(requester)
	serviceProxy.SetToolQuerier(queries)
	serviceProxy.SetAuditWriter(queries)

	chartRatings := handler.NewChartRatingsHandler(queries)
	chartRatings.SetRecommendationPolicy(catalog.NewRecommendationPolicy(cfg.ChartRatingBayesianAverage, cfg.ChartRatingBayesianWeight))
	chartRatings.SetLogger(logger)
	chartRatings.SetAuthorization(rbacEngine, rbacQuerier)
	chartRatings.SetRunTx(sqlcMutationTxRunner[handler.ChartRatingMutationTx](database))

	auditHandler := handler.NewAuditHandler(queries)
	auditHandler.SetRunTx(sqlcMutationTxRunner[handler.AuditExportMutationTx](database))
	streamSecurity := tunnel.StreamConsumerDependencies{
		JWT:         c.jwtManager,
		Queries:     queries,
		Tickets:     c.streamTickets,
		AuditWriter: queries,
		RBACEngine:  rbacEngine,
		RBACQuerier: rbacQuerier,
	}
	execConsumer, err := tunnel.NewExecConsumer(hub, logger, streamSecurity)
	if err != nil {
		return RouterDependencies{}, err
	}
	logsConsumer, err := tunnel.NewLogsConsumer(hub, logger, streamSecurity)
	if err != nil {
		return RouterDependencies{}, err
	}
	eventStream, err := handler.NewEventStreamHandler(
		bus,
		c.jwtManager,
		queries,
		c.streamTickets,
		rbacEngine,
		rbacQuerier,
		c.charlieFindingEvents,
	)
	if err != nil {
		return RouterDependencies{}, err
	}

	return RouterDependencies{
		CoreAuth: CoreAuthDependencies{
			JWT:         c.jwtManager,
			Encryptor:   c.encryptor,
			AuthQueries: queries,
			AuditWriter: queries,
			Auth:        c.authHandler,
			TOTP:        c.totpHandler,
			SSO:         c.ssoHandler,
			Docs:        handler.NewDocsHandler(),
			SSOPresets:  handler.NewSSOPresetsHandler(),
			RBAC:        rbacHandler,
			Principals:  principals,
			RBACQueries: rbacQuerier,
			RBACEngine:  rbacEngine,
			Queries:     queries,
			Readyz: newReadinessHandler(database, queue, hub).
				withLocatorError(c.locatorReadinessErr).
				withSecurityCacheCoordinator(c.securityCacheCoordinator, cfg.ServerReplicas > 1).
				withCriticalRuntime(c.runtime),
		},
		ClusterResources: ClusterResourceDependencies{
			Clusters:              c.clusterHandler,
			ClusterTemplates:      c.clusterTemplateHandler,
			ClusterRegistration:   c.clusterRegistrationHandler,
			ClusterRegistries:     c.clusterRegistriesHandler,
			ClusterSnapshots:      c.clusterSnapshotsHandler,
			ControlPlaneSnapshots: c.controlPlaneSnapshotHandler,
			NamespaceScopedRBAC:   cfg.NamespaceScopedRBACEnabled,
			NetworkPolicies:       c.networkPoliciesHandler,
			Gatekeeper:            gatekeeper,
			Projects:              c.projectHandler,
			Tools:                 c.toolHandler,
			Backups:               c.backupHandler,
			Logging:               c.loggingHandler,
			Monitoring:            c.monitoringHandler,
			ControlPlane:          c.controlPlaneHandler,
			Resources:             c.resourceHandler,
			ServiceProxy:          serviceProxy,
			Workloads:             c.workloadHandler,
			ResourcesSearch:       resourceSearch,
			ClusterAgent:          clusterAgent,
			ApiserverAudit:        c.apiserverAuditHandler,
			ApiserverAllowlist:    c.apiserverAllowlistHandler,
			ImageVulns:            imageVulns,
		},
		Delivery: DeliveryDependencies{
			Sources:     c.deliverySourceHandler,
			Bundles:     c.deliveryBundleHandler,
			Targets:     c.deliveryTargetHandler,
			Rollouts:    c.deliveryRolloutHandler,
			Deployments: deliveryhandler.NewDeploymentHandler(queries, c.deliveryDeploymentController, bus),
			Inventory:   deliveryhandler.NewInventoryHandler(queries),
			System:      c.deliverySystemRolloutHandler,
		},
		AdminPlatform: AdminPlatformDependencies{
			Audit:          auditHandler,
			Alerting:       c.alertingHandler,
			Anomaly:        c.anomalyHandler,
			Catalog:        c.catalogHandler,
			ChartRatings:   chartRatings,
			PlatformCharts: c.platformCharts,
			Security:       c.securityHandler,
			DexConfig:      c.dexHandler,
		},
		StreamingInternal: StreamingInternalDependencies{
			Hub:               hub,
			Proxy:             tunnel.NewProxyHandler(hub, logger),
			InternalK8s:       newInternalK8sHandler(c, cfg, logger),
			InternalHelm:      newInternalHelmHandler(c, cfg, logger),
			Exec:              execConsumer,
			Logs:              logsConsumer,
			EventStream:       eventStream,
			StreamTickets:     c.streamTicketHandler,
			StreamTicketStore: c.streamTickets,
		},
	}, nil
}

func newInternalK8sHandler(c *productionComposition, cfg *config.Config, logger *slog.Logger) *tunnel.InternalK8sHandler {
	h := tunnel.NewInternalK8sHandler(c.hub, tunnel.InternalRequestKeyring{
		Current: cfg.InternalPSK, Previous: cfg.InternalPSKPrevious,
	}, logger)
	h.SetAuditWriter(c.queries)
	return h
}

func newInternalHelmHandler(c *productionComposition, cfg *config.Config, logger *slog.Logger) *tunnel.InternalHelmHandler {
	h := tunnel.NewInternalHelmHandler(c.hub, tunnel.InternalRequestKeyring{
		Current: cfg.InternalPSK, Previous: cfg.InternalPSKPrevious,
	}, logger)
	h.SetAuditWriter(c.queries)
	return h
}
