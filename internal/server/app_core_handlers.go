package server

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	deliveryprovider "github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	deliverystatus "github.com/alphabravocompany/astronomer-go/internal/delivery/status"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
)

func (c *productionComposition) initializeCoreHandlers(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	encryptor := c.encryptor
	bus := events.NewBus()
	hub := tunnel.NewHubWithValidator(logger, queries)
	hub.SetDeliveryStateProvider(deliveryprovider.New(queries, encryptor))
	deliveryStatusIngester := deliverystatus.NewIngester(deliverystatus.NewSQLRunner(database.Pool()))
	hub.SetDeliveryStatusSink(deliveryStatusIngester)
	hub.SetPublisher(busPublisherAdapter{bus: bus})
	// Cross-pod tunnel proxy fallback. Each pod publishes "I own this
	// cluster's WS" into redis on agent connect; sibling pods read that
	// to reverse-proxy /k8s/* and kubectl-shell requests to the owner.
	// Required for multi-replica server deployments — nginx upstream
	// keep-alive pins user-facing requests to one upstream pod, so
	// without the locator every cluster-shell/image-scan/k8s-proxy
	// request that lands on the non-owning pod 503s.
	var locatorReadinessErr string
	podIP := strings.TrimSpace(os.Getenv("ASTRONOMER_POD_IP"))
	if podIP != "" && cfg.RedisURL != "" {
		addr := podIP + ":8000"
		if loc, lerr := tunnel.NewLocatorFromAsynqRedisURL(cfg.RedisURL, addr, logger); lerr != nil {
			logger.Warn("tunnel locator init failed; cross-pod proxy disabled", "error", lerr)
		} else {
			hub.SetLocator(loc)
			logger.Info("tunnel locator wired", "address", addr)
		}
	} else if cfg.ServerReplicas > 1 && cfg.RedisURL != "" && podIP == "" {
		// L19: a multi-replica deployment with redis but no POD_IP leaves the
		// cross-pod tunnel locator disabled, so every cluster-shell / k8s-proxy /
		// exec request landing on a non-owning replica 503s. Fail readiness loudly
		// (the rollout stalls) rather than silently degrade.
		locatorReadinessErr = "ASTRONOMER_POD_IP is unset on a multi-replica deployment (server_replicas>1) with redis configured; the cross-pod tunnel locator is disabled and non-owning replicas will 503 — set ASTRONOMER_POD_IP (Helm injects it from status.podIP; add it to raw k8s manifests)"
		logger.Error("tunnel locator MISCONFIGURED (L19): /readyz will fail until ASTRONOMER_POD_IP is set",
			"server_replicas", cfg.ServerReplicas)
	}
	// A4 / M5+L13: one shared per-IP connect FAILURE limiter feeds both the hub
	// WS path and the tunnel2 /connect path (cross-path IP view). The limiter
	// counts failed CONNECT validations and resets to zero on every success, so
	// a healthy fleet behind one egress IP is never throttled. The janitor is
	// started later on reconcileCtx (alongside the other background loops).
	connLimiter := tunnel.NewConnectFailureLimiter(
		cfg.TunnelConnectAuthFailureLimit,
		time.Duration(cfg.TunnelConnectAuthFailureWindowMinutes)*time.Minute,
		nil,
	)
	hub.SetConnectLimiter(connLimiter, time.Duration(cfg.TunnelConnectClockSkewMinutes)*time.Minute)
	remoteServer := tunnel2.NewRemoteServer(logger, queries)
	remoteServer.SetConnectLimiter(connLimiter)
	requester := handler.NewTunnelK8sRequester(hub)
	// Cross-pod fallback for server-internal tunnel calls (shell open,
	// project reconciler, etc.). Same PSK both sides — derived from the
	// shared encryption key so all replicas agree without extra config.
	requester.SetInternalPSK(tunnel.DerivePSK(cfg.EncryptionKey))
	helmRequester := handler.NewTunnelHelmRequester(hub)
	// Same cross-pod PSK plumbing as the k8s requester: enables the
	// helm op to reverse-proxy to whichever sibling owns the WS when
	// the local hub doesn't (required for multi-replica catalog ops).
	helmRequester.SetInternalPSK(tunnel.DerivePSK(cfg.EncryptionKey))
	monitoringHandler := handler.NewMonitoringHandlerWithDeps(queries, requester, helmRequester)
	monitoringHandler.SetRunTx(sqlcMutationTxRunner[handler.MonitoringMutationTx](database))
	monitoringHandler.SetLogger(logger)
	grafanaTickets := auth.NewGrafanaTicketStore(auth.GrafanaTicketTTL)
	if gbackend, terr := auth.NewRedisGrafanaTicketBackendFromURL(cfg.RedisURL); terr != nil {
		logger.Warn("grafana tickets: redis backend unavailable, using per-pod in-memory store", "error", terr)
	} else {
		grafanaTickets = auth.NewGrafanaTicketStoreWithBackend(auth.GrafanaTicketTTL, gbackend)
	}
	monitoringHandler.SetGrafanaTickets(grafanaTickets)
	monitoringHandler.SetUserLookup(queries)
	monitoringHandler.SetServerURL(cfg.ServerURL)
	monitoringHandler.SetGrafanaProxyImage(os.Getenv("ASTRONOMER_SERVER_IMAGE"))
	monitoringHandler.SetGrafanaExpose(handler.GrafanaExpose{
		GatewayClass:      os.Getenv("ASTRONOMER_GATEWAY_CLASS"),
		IngressClass:      os.Getenv("ASTRONOMER_INGRESS_CLASS"),
		GatewayName:       os.Getenv("ASTRONOMER_GATEWAY_NAME"),
		PlatformNamespace: os.Getenv("POD_NAMESPACE"),
		TLSIssuerName:     os.Getenv("ASTRONOMER_TLS_ISSUER"),
		TLSIssuerKind:     os.Getenv("ASTRONOMER_TLS_ISSUER_KIND"),
	})
	grafanaSessionTTL := newSessionTimeoutResolver(queries, logger)
	monitoringHandler.SetSessionTTL(func(ctx context.Context) time.Duration {
		return time.Duration(grafanaSessionTTL(ctx)) * time.Minute
	})
	// Migration 146: the Thanos/Prometheus/Alertmanager credential is
	// Fernet-sealed at rest. This handler both reads it (to build a monitoring
	// client) and read-modify-writes the column it lives in.
	monitoringHandler.SetEncryptor(encryptor)
	alertingHandler := handler.NewAlertingHandlerWithDeps(queries, requester)
	alertingHandler.SetRunTx(sqlcMutationTxRunner[handler.AlertingMutationTx](database))
	// Migration 146: persistSharedAlertingAssetHashes is a read-modify-write on
	// monitoring_backends.auth_config, so this handler needs the same key the
	// monitoring handler got. Without it, syncing alerting assets would re-seal
	// a document it could not read.
	alertingHandler.SetEncryptor(encryptor)
	toolHandler := handler.NewToolHandlerWithHelm(queries, helmRequester)
	toolHandler.SetRunTx(sqlcMutationTxRunner[handler.ToolMutationTx](database))
	toolHandler.SetLogger(logger)
	toolHandler.SetEventBus(bus)
	catalogHandler := handler.NewCatalogHandlerWithHelm(queries, helmRequester)
	catalogHandler.SetRunTx(sqlcMutationTxRunner[handler.CatalogMutationTx](database))
	catalogHandler.SetLogger(logger)
	// Migration 145: chart-repository credentials are Fernet-sealed at rest.
	catalogHandler.SetEncryptor(encryptor)
	// P4.9 — catalog_release.changed liveness events on installed-chart writes.
	catalogHandler.SetEventBus(bus)
	backupHandler := handler.NewBackupHandler(queries)
	backupHandler.SetRunTx(sqlcMutationTxRunner[handler.BackupMutationTx](database))
	// Phase B2 — Velero backup engine wiring. Handler degrades cleanly when
	// these aren't set; we set them here so the running stack uses real Velero
	// CRs + real S3 SigV4 probes instead of the legacy stub paths.
	backupHandler.SetEncryptor(encryptor)
	backupHandler.SetK8sRequester(requester)
	backupHandler.SetLogger(logger)
	backupHandler.SetEventBus(bus)
	loggingHandler := handler.NewLoggingHandler(queries)
	loggingHandler.SetRunTx(sqlcMutationTxRunner[handler.LoggingMutationTx](database))
	// Logging controller — DB-backed operations table + background reconciler
	// applies ConfigMaps and ingest-token Secrets into astronomer-logging, and
	// patches the baseline fluent-bit Helm extraVolumeMounts. Comparison.md §7/§10/§11.
	loggingHandler.SetK8sRequester(requester)
	loggingHandler.SetHelmRequester(helmRequester)
	loggingHandler.SetLogger(logger)
	loggingHandler.SetEventBus(bus)
	loggingHandler.SetEncryptor(encryptor)
	securityHandler := handler.NewSecurityHandler(queries)
	// Phase B5 — CIS scans wiring (handler creates ClusterScan CRs through the
	// tunnel and runs an in-process poller until the report lands).
	securityHandler.SetK8sRequester(requester)
	securityHandler.SetClusterQuerier(queries)
	securityHandler.SetLogger(logger)
	securityHandler.SetEventBus(bus)
	securityHandler.SetRunTx(sqlcMutationTxRunner[handler.SecurityMutationTx](database))
	// P4.9 — apiserver allow-list surface (migration 070). Previously the
	// handler was constructed only in test routers, leaving the documented
	// /clusters/{id}/apiserver-allowlist/* routes unrouted in production;
	// wired here so the network-access page works against a real server and
	// its network_access.changed publisher has a bus.
	apiserverAllowlistHandler := handler.NewApiserverAllowlistHandler(queries)
	apiserverAllowlistHandler.SetAuditor(queries)
	apiserverAllowlistHandler.SetEventBus(bus)
	apiserverAllowlistHandler.SetRunTx(sqlcMutationTxRunner[handler.ApiserverAllowlistMutationTx](database))
	workloadHandler := handler.NewWorkloadHandlerWithDeps(queries, requester)
	workloadHandler.SetRunTx(sqlcMutationTxRunner[handler.WorkloadMutationTx](database))
	workloadHandler.SetLogger(logger)
	// The tunnel requester also implements the live pod watch used by the
	// /pods/watch/ SSE endpoint.
	workloadHandler.SetPodWatcher(requester)
	rbacEngine := rbac.NewEngine()
	// Project bindings always expand to their (cluster, namespace) pairs — that
	// expansion is what makes a project-owner/project-member grant mean anything
	// on cluster resources, and it is not gated on a flag.
	rbacQuerier := appmiddleware.NewSQLCRBACQuerier(queries)
	securityHandler.SetAuthorization(rbacEngine, rbacQuerier)
	monitoringHandler.SetAuthorization(rbacEngine, rbacQuerier)
	toolHandler.SetAuthorization(rbacEngine, rbacQuerier)
	catalogHandler.SetAuthorization(rbacEngine, rbacQuerier)
	backupHandler.SetAuthorization(rbacEngine, rbacQuerier)
	loggingHandler.SetAuthorization(rbacEngine, rbacQuerier)
	loggingHandler.SetLokiIngestReconciler(monitoringHandler)
	loggingHandler.SetLokiAttachGate(monitoringHandler)
	monitoringHandler.SetSystemLoggingOutputDisabler(loggingHandler)
	workloadHandler.SetAuthorization(rbacEngine, rbacQuerier)
	// Anomaly-baselines read endpoints gate on cluster authz (fail closed:
	// unwired → 500 for any authenticated caller), so this MUST be set.
	anomalyHandler := handler.NewAnomalyHandler(queries)
	anomalyHandler.SetAuthorization(rbacEngine, rbacQuerier)
	// Handler-side result filtering must be enabled TOGETHER with the list gate
	// (below via deps.NamespaceScopedRBAC): the gate admits scoped users, the
	// handler filters their results. Enabling one without the other would leak.
	workloadHandler.SetNamespaceScopedRBAC(cfg.NamespaceScopedRBACEnabled)
	warnInertProjectBindings(ctx, queries, cfg.NamespaceScopedRBACEnabled, logger)
	c.bus = bus
	c.hub = hub
	c.deliveryStatusIngester = deliveryStatusIngester
	c.locatorReadinessErr = locatorReadinessErr
	c.connLimiter = connLimiter
	c.remoteServer = remoteServer
	c.requester = requester
	c.helmRequester = helmRequester
	c.monitoringHandler = monitoringHandler
	c.alertingHandler = alertingHandler
	c.toolHandler = toolHandler
	c.catalogHandler = catalogHandler
	c.backupHandler = backupHandler
	c.loggingHandler = loggingHandler
	c.securityHandler = securityHandler
	c.apiserverAllowlistHandler = apiserverAllowlistHandler
	c.workloadHandler = workloadHandler
	c.rbacEngine = rbacEngine
	c.rbacQuerier = rbacQuerier
	c.anomalyHandler = anomalyHandler
	return nil
}
