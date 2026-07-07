package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	livemetrics "github.com/alphabravocompany/astronomer-go/internal/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/notify"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// busPublisherAdapter bridges the *events.Bus into the tunnel.LifecyclePublisher
// interface (the tunnel package can't import events directly without a cycle).
type busPublisherAdapter struct{ bus *events.Bus }

func (a busPublisherAdapter) Publish(eventType string, data any) {
	a.bus.Publish(events.Type(eventType), data)
}

// emailNotifierAdapter wraps *email.Enqueuer in the handler-local
// EmailNotifier surface. The two-type indirection keeps the handler
// package free of any email package imports (so the test fakes don't
// have to drag templates along) while still letting NewApp wire the
// concrete Enqueuer.
type emailNotifierAdapter struct{ e *email.Enqueuer }

func (a *emailNotifierAdapter) EnqueueAndLog(ctx context.Context, req handler.EmailNotifierRequest) {
	if a == nil || a.e == nil {
		return
	}
	a.e.EnqueueAndLog(ctx, email.Request{
		To:       req.To,
		Template: req.Template,
		Subject:  req.Subject,
		Data:     req.Data,
		UserID:   req.UserID,
	})
}

// dsnEnforcesTLS reports whether a Postgres DSN includes an sslmode setting
// that requires TLS. Acceptable values: `require`, `verify-ca`, `verify-full`.
// Anything else (sslmode=disable, sslmode=allow, sslmode=prefer, or no
// sslmode at all — Postgres treats omission as `prefer` which silently
// downgrades to plaintext if the server allows it) returns false.
func dsnEnforcesTLS(dsn string) bool {
	return config.DSNEnforcesTLS(dsn)
}

// devSecretKey / devEncryptionKey mirror the sentinels in internal/config so the
// server package (and its tests) keep their existing names. The authoritative
// production fail-fast lives in config.ValidateProductionSecurity, which both the
// server and the worker call (C-01).
const (
	devSecretKey     = "local-dev-secret-key-change-in-production"
	devEncryptionKey = "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA="
)

func isProductionConfig(cfg *config.Config) bool {
	return config.IsProduction(cfg)
}

func validateProductionSecurityConfig(cfg *config.Config, encryptor *auth.Encryptor) error {
	return config.ValidateProductionSecurity(cfg, encryptor != nil)
}

func validateProductionSecurityWiring(cfg *config.Config, deps RouterDependencies) error {
	if !isProductionConfig(cfg) {
		return nil
	}
	var errs []string
	if deps.JWT == nil {
		errs = append(errs, "JWT manager is not wired")
	}
	if deps.AuthQueries == nil {
		errs = append(errs, "auth queries are not wired")
	}
	if deps.RBACEngine == nil {
		errs = append(errs, "RBAC engine is not wired")
	}
	if deps.RBACQueries == nil {
		errs = append(errs, "RBAC queries are not wired")
	}
	if deps.Encryptor == nil {
		errs = append(errs, "encryptor is not wired")
	}
	if len(errs) > 0 {
		return fmt.Errorf("production security wiring invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

// resolveCallbackBaseURL builds the API base URL used when registering SSO
// providers. The auth package appends `/auth/callback/{provider}` itself, so
// this function must stop at `/api/v1` rather than `/api/v1/auth`.
//
// It prefers platform_configuration.server_url so the production deployment URL
// is always honoured; falls back to a localhost-friendly default if no
// platform record exists yet (e.g. pre-bootstrap).
func resolveCallbackBaseURL(ctx context.Context, _ *config.Config, queries *sqlc.Queries) string {
	base := "http://localhost:8000"
	if queries == nil {
		return base + "/api/v1"
	}
	if cfg, err := queries.GetPlatformConfig(ctx); err == nil && strings.TrimSpace(cfg.ServerUrl) != "" {
		base = strings.TrimRight(cfg.ServerUrl, "/")
	}
	return base + "/api/v1"
}

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpServer *http.Server
	handler    http.Handler
	// internalArgoCDHandler serves the dedicated, network-isolated
	// ArgoCD->cluster proxy on a separate (non-public) port. nil in
	// lightweight test servers.
	internalArgoCDHandler http.Handler
	logger                *slog.Logger
	db                    *db.DB
	cancel                context.CancelFunc
	queue                 *asynq.Client
	// hub is the tunnel hub; nil in lightweight test servers. Held here
	// so Shutdown can drain WS connections before tearing down HTTP.
	hub *tunnel.Hub
	// tunnelWorker is an in-process asynq.Server that drains the
	// "tunnel" queue. Tasks on that queue (cluster_template:apply +
	// drift_check) call into the ToolHandler.EnsureInstalled tunnel
	// path, which only works on the pod that owns the WS terminations.
	// Nil in lightweight test servers built via New().
	tunnelWorker *worker.Worker
	// Encryptor is the Fernet encryptor wired into handlers that surface
	// encrypted columns (argocd auth tokens, sso client secrets, etc.).
	Encryptor *auth.Encryptor
	// SSO drives the OAuth login/callback flow. May be nil if no providers
	// are configured at boot.
	SSO *auth.SSOManager
}

// DB returns the primary application database wrapper when this server was
// built via NewApp. Nil for tests or lightweight routers built with New.
func (s *Server) DB() *db.DB {
	return s.db
}

// New creates a new Server with the given config and logger.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	router := NewRouter(cfg, RouterDependencies{})

	s := &Server{
		handler: router,
		logger:  logger,
	}

	s.httpServer = &http.Server{
		// Wrap with otelhttp so every request emits a server span
		// when the global TracerProvider has an exporter; no-op when it
		// doesn't.
		Handler: wrapWithTracing(router),
		// ReadHeaderTimeout caps the slowloris exposure but does not bound the
		// long-lived WebSocket tunnel connection (which lives in /api/v1/ws/...).
		// Keep ReadTimeout/WriteTimeout at zero so the WS connection is not
		// forcibly closed mid-stream. Per-handler timeouts cover REST routes.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s
}

// NewApp creates a fully wired production server with database-backed handlers.
func NewApp(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	if isProductionConfig(cfg) && !dsnEnforcesTLS(cfg.DatabaseURL) {
		logger.Warn(
			"DATABASE_URL does not enforce TLS but production mode is enabled "+
				"— production must use sslmode=require/verify-ca/verify-full",
			"event", "production_dsn_tls_warning",
		)
	}

	database, err := db.ConnectWithConfig(ctx, cfg.DatabaseURL, db.PoolConfig{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   time.Duration(cfg.DBMaxConnLifetimeMin) * time.Minute,
		MaxConnIdleTime:   time.Duration(cfg.DBMaxConnIdleMin) * time.Minute,
		HealthCheckPeriod: time.Duration(cfg.DBHealthCheckPeriodSec) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	// T8.1 — fail fast on a corrupt schema_migrations row set
	// (multi-row drift or dirty=true). The .247 incident on 2026-05-13
	// silently shipped {84 dirty=t, 86 clean} for hours; refusing to
	// start surfaces it in the first CrashLoop instead of letting the
	// pod serve traffic against an indeterminate schema.
	if shErr := database.SchemaHealth(ctx); shErr != nil {
		database.Close()
		return nil, fmt.Errorf("schema health check failed: %w", shErr)
	}

	queries := sqlc.New(database.Pool())
	jwtManager := auth.NewJWTManager(cfg.SecretKey, cfg.SessionTimeoutMinutes)

	// Best-effort Fernet encryptor + SSO manager. Both are optional: if the
	// encryption key is missing or invalid we still come up so dev/local
	// stacks without secrets don't break — a warning is logged. Handlers
	// that need decryption skip the work when the encryptor is nil.
	var (
		encryptor  *auth.Encryptor
		ssoManager *auth.SSOManager
	)
	if cfg.EncryptionKey != "" {
		enc, encErr := auth.NewEncryptor(cfg.EncryptionKey)
		if encErr != nil {
			logger.Warn("failed to initialise encryptor", "error", encErr)
		} else {
			encryptor = enc
			callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
			ssoManager = auth.NewSSOManager(encryptor, jwtManager, callbackBase)
			if loadErr := ssoManager.LoadFromDatabase(ctx, queries); loadErr != nil {
				logger.Warn("failed to load sso providers", "error", loadErr)
			}
		}
	} else {
		logger.Warn("ASTRONOMER_ENCRYPTION_KEY is not set; encrypted columns will be returned as ciphertext and SSO is disabled")
	}
	if err := validateProductionSecurityConfig(cfg, encryptor); err != nil {
		database.Close()
		return nil, err
	}

	// Migration 045 — Dex consolidation.
	//
	// When the chart deploys the in-cluster Dex (dex.enabled=true), the
	// configmap template sets DEX_BUNDLED_ENABLED + DEX_BUNDLED_* describing
	// the templated objects. The bootstrap below auto-wires the singleton
	// dex_settings row so the operator's first connector + Apply works
	// without a manual settings step. No-op when dex.enabled=false (legacy
	// operator-managed Dex flow stays in effect).
	if _, err := SeedBundledDexSettings(ctx, queries, logger); err != nil {
		logger.Warn("dex bootstrap: seed failed", "error", err)
	}
	// Surface drift between legacy sso_configurations and the new
	// dex_connectors path. Best-effort: log + continue on error.
	if err := WarnIfLegacySSORowsActive(ctx, queries, logger); err != nil {
		logger.Debug("dex bootstrap: legacy SSO check failed", "error", err)
	}

	bus := events.NewBus()
	hub := tunnel.NewHubWithValidator(logger, queries)
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
	monitoringHandler.SetLogger(logger)
	argocdHandler := handler.NewArgoCDHandler(queries)
	argocdHandler.SetLogger(logger)
	argocdHandler.SetEncryptor(encryptor)
	argocdHandler.SetClusterProxyBaseURL(cfg.ArgoCDClusterProxyBaseURL)
	toolHandler := handler.NewToolHandlerWithHelm(queries, helmRequester)
	toolHandler.SetLogger(logger)
	catalogHandler := handler.NewCatalogHandlerWithHelm(queries, helmRequester)
	catalogHandler.SetLogger(logger)
	backupHandler := handler.NewBackupHandler(queries)
	// Phase B2 — Velero backup engine wiring. Handler degrades cleanly when
	// these aren't set; we set them here so the running stack uses real Velero
	// CRs + real S3 SigV4 probes instead of the legacy stub paths.
	backupHandler.SetEncryptor(encryptor)
	backupHandler.SetK8sRequester(requester)
	backupHandler.SetLogger(logger)
	loggingHandler := handler.NewLoggingHandler(queries)
	// Logging controller — DB-backed operations table + background reconciler
	// applies rendered ConfigMaps into the managed cluster's astronomer-logging
	// namespace via the tunnel K8s requester. Comparison.md §7/§10/§11.
	loggingHandler.SetK8sRequester(requester)
	loggingHandler.SetLogger(logger)
	securityHandler := handler.NewSecurityHandler(queries)
	// Phase B5 — CIS scans wiring (handler creates ClusterScan CRs through the
	// tunnel and runs an in-process poller until the report lands).
	securityHandler.SetK8sRequester(requester)
	securityHandler.SetClusterQuerier(queries)
	securityHandler.SetLogger(logger)
	workloadHandler := handler.NewWorkloadHandlerWithDeps(queries, requester)
	workloadHandler.SetLogger(logger)
	// The tunnel requester also implements the live pod watch used by the
	// /pods/watch/ SSE endpoint.
	workloadHandler.SetPodWatcher(requester)
	rbacEngine := rbac.NewEngine()
	rbacQuerier := appmiddleware.NewSQLCRBACQuerierWithNamespaceScoping(queries, cfg.NamespaceScopedRBACEnabled)
	monitoringHandler.SetAuthorization(rbacEngine, rbacQuerier)
	argocdHandler.SetAuthorization(rbacEngine, rbacQuerier)
	toolHandler.SetAuthorization(rbacEngine, rbacQuerier)
	catalogHandler.SetAuthorization(rbacEngine, rbacQuerier)
	backupHandler.SetAuthorization(rbacEngine, rbacQuerier)
	loggingHandler.SetAuthorization(rbacEngine, rbacQuerier)
	workloadHandler.SetAuthorization(rbacEngine, rbacQuerier)
	// Anomaly-baselines read endpoints gate on cluster authz (fail closed:
	// unwired → 500 for any authenticated caller), so this MUST be set.
	anomalyHandler := handler.NewAnomalyHandler(queries)
	anomalyHandler.SetAuthorization(rbacEngine, rbacQuerier)
	// Handler-side result filtering must be enabled TOGETHER with the list gate
	// (below via deps.NamespaceScopedRBAC): the gate admits scoped users, the
	// handler filters their results. Enabling one without the other would leak.
	workloadHandler.SetNamespaceScopedRBAC(cfg.NamespaceScopedRBACEnabled)
	// Fail-fast on a bad REDIS_URL — the old silent localhost fallback was
	// a production footgun. Returning an error
	// surfaces the misconfig at process start instead of letting every
	// asynq enqueue silently fail downstream.
	redisOpt, redisErr := asynq.ParseRedisURI(cfg.RedisURL)
	if redisErr != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", cfg.RedisURL, redisErr)
	}
	queue := asynq.NewClient(redisOpt)
	// Phase B5 — give the security handler the asynq queue. The handler's
	// IngestEnqueuer interface is satisfied directly by *asynq.Client.
	securityHandler.SetIngestQueue(queue)
	// Persister lets the in-process poller write ClusterScan reports back to
	// our security_scan_results rows.
	securityHandler.SetIngestPersister(queries)
	// Phase B3 — project enforcement controller: wire the project handler with
	// the asynq queue (for AddNamespace → enqueue project:reconcile) and the
	// shared K8sRequester (for ResourceQuota / LimitRange / NetworkPolicy
	// server-side apply through the tunnel).
	projectHandler := handler.NewProjectHandler(queries)
	projectHandler.SetEncryptor(encryptor)
	projectHandler.SetTaskQueue(queue)
	projectHandler.SetTaskOutbox(queries)
	projectHandler.SetK8sRequester(requester)
	projectHandler.SetLogger(logger)
	// Atomic namespace add/remove: row-lock the project and write the JSONB
	// array + project_namespaces sidecar in one transaction, so a mid-write
	// failure can't desync them. Pool-backed; unit tests wire their own runner.
	projectHandler.SetRunTx(func(ctx context.Context, fn func(q handler.ProjectNamespaceTx) error) error {
		tx, err := database.Pool().BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	// Cluster templates (migration 049). Owns /api/v1/cluster-templates/*
	// CRUD plus the per-cluster bind/apply/reapply/detach surface. The
	// asynq client is shared with the rest of the platform so apply
	// tasks land in the same queue as decommission/argocd-refresh.
	clusterTemplateHandler := handler.NewClusterTemplateHandler(queries)
	clusterTemplateHandler.SetQueue(queue)
	clusterTemplateHandler.SetTaskOutbox(queries)
	// Cluster registries (migration 050). Multi-registry-per-cluster admin
	// UX. Apply queue uses the same asynq client; tunnel requester is
	// shared with project enforcement so the /test/ endpoint can dial the
	// member cluster's network from the management plane.
	clusterRegistriesHandler := handler.NewClusterRegistriesHandler(queries)
	clusterRegistriesHandler.SetApplyEnqueue(queue)
	clusterRegistriesHandler.SetTaskOutbox(queries)
	clusterRegistriesHandler.SetRequester(requester)
	clusterRegistriesHandler.SetEncryptor(encryptor)
	tasks.ConfigurePlaintextCredentialMigration(tasks.PlaintextCredentialMigrationDeps{
		Queries:   queries,
		Encryptor: encryptor,
	})
	// Network policy templates (migration 068). Sister of cluster
	// templates but namespace-scoped: deny-all-ingress, project-isolated,
	// namespace-only, allow-ingress-controllers. The reconciler shares
	// the tunnel K8sRequester so the same circuit-breaker / retry
	// behavior as every other tunnel-mediated K8s op applies.
	networkPoliciesHandler := handler.NewNetworkPolicyHandler(queries)
	networkPoliciesHandler.SetQueue(queue)
	networkPoliciesHandler.SetK8sRequester(requester)
	tasks.ConfigureNetworkPolicyApply(tasks.NetworkPolicyApplyDeps{
		Queries:   queries,
		Requester: requester,
	})
	// Cloud credentials (migration 053). Project-scoped CRUD over
	// AWS / GCP / Azure / Generic secrets, with a /test/ endpoint
	// that dials each provider's "validate" SDK and a materialization
	// worker that fans the cleartext out to in-cluster k8s Secrets
	// via the tunnel. Encryptor is required for any write; tester is
	// the default impl (10s budget per provider call).
	cloudCredentialsHandler := handler.NewCloudCredentialHandler(queries)
	cloudCredentialsHandler.SetAuditor(queries)
	cloudCredentialsHandler.SetEncryptor(encryptor)
	cloudCredentialsHandler.SetEnqueuer(queue)
	cloudCredentialsHandler.SetTaskOutbox(queries)
	cloudCredentialsHandler.SetTester(handler.NewDefaultCloudTester())
	// Dashboard widgets (migration 058). Admin CRUD over widget rows +
	// datasource rows; render endpoints serve a per-scope widget grid.
	dashboardsHandler := handler.NewDashboardHandler(queries)
	dashboardsHandler.SetAuditor(queries)
	dashboardsHandler.SetEncryptor(encryptor)
	// Per-project BYO catalogs (migration 061).
	projectCatalogsHandler := handler.NewProjectCatalogHandler(queries)
	projectCatalogsHandler.SetAuditor(queries)
	// Cluster groups (migration 066).
	clusterGroupsHandler := handler.NewClusterGroupHandler(queries)
	clusterGroupsHandler.SetAuditor(queries)
	handler.RegisterClusterGroupMetrics()
	tasks.ClusterGroupMetricsRefresher = func(ctx context.Context) {
		handler.RefreshClusterGroupMetrics(ctx, queries)
	}
	// Vault integration (migration 067). Resolver wired into each
	// install path so ${vault://...} markers in operator-supplied values
	// blobs are substituted in-memory at install time.
	vaultResolver := vault.NewResolver(queries, encryptor)
	vaultResolver.SetObserver(newVaultMetricsObserver(queries))
	vaultHandler := handler.NewVaultHandler(queries)
	vaultHandler.SetAuditor(queries)
	vaultHandler.SetEncryptor(encryptor)
	vaultHandler.SetProbe(handler.LiveVaultProbe{})
	vaultHandler.SetResolver(vaultResolver)
	catalogHandler.SetVaultResolver(vaultResolver)
	toolHandler.SetVaultResolver(vaultResolver)
	clusterTemplateHandler.SetVaultResolver(vaultResolver)
	// Cluster snapshots (migration 052). Velero CRDs are driven over
	// the existing tunnel K8sRequester so the same circuit-breaker /
	// retry behaviour as every other tunnel-mediated K8s op applies.
	// Metric registration is idempotent — see RegisterClusterSnapshotsMetrics.
	clusterSnapshotsHandler := handler.NewClusterSnapshotsHandler(queries)
	clusterSnapshotsHandler.SetRequester(requester)
	// Control-plane (etcd) DR snapshots — OFF unless an operator opts in via
	// config (control_plane_snapshots_enabled). Left nil, the etcd routes below
	// never register, so the privileged snapshot Job path is unreachable.
	// ponytail: manual snapshots only; scheduled sweep wired separately if asked.
	var controlPlaneSnapshotHandler *handler.ControlPlaneSnapshotHandler
	if cfg.ControlPlaneSnapshotsEnabled {
		controlPlaneSnapshotHandler = handler.NewControlPlaneSnapshotHandler(queries)
		controlPlaneSnapshotHandler.SetRequester(requester)
		// Wire the sweep worker: it reconciles in-flight snapshot Jobs to a
		// terminal DB state (and auto-schedules rolling snapshots only when
		// feature.control_plane_snapshots is additionally enabled). Left
		// unwired when the feature is off, so HandleControlPlaneSnapshotSweep
		// stays a no-op even though its scheduler entry ticks.
		tasks.ConfigureControlPlaneSnapshotSweep(tasks.ControlPlaneSnapshotSweepDeps{Queries: queries, Log: logger})
		tasks.SetControlPlaneSnapshotApplier(controlPlaneSnapshotHandler.ApplySnapshotJob)
		tasks.SetControlPlaneSnapshotStatusReader(controlPlaneSnapshotHandler.ReadSnapshotJobStatus)
	}

	// Native per-CRD RBAC — an additive allow layer on the k8s-proxy authz
	// hook, OFF unless native_rbac_enabled. Left nil, deps.NativeAuthz is nil
	// (proxy authz unchanged) and the CRUD routes never register.
	var nativeRBACAuthz *nativeRBACAuthorizer
	var nativeRBACHandler *handler.NativeRBACHandler
	if cfg.NativeRBACEnabled {
		nativeRBACAuthz = newNativeRBACAuthorizer(queries)
		nativeRBACHandler = handler.NewNativeRBACHandler(queries)
		nativeRBACHandler.SetInvalidator(nativeRBACAuthz.Invalidate)
		// Privilege-escalation guard on native-rule authoring: the caller must
		// already hold the mapped (resource, verb) at the rule's scope. Without
		// this the guard is a no-op and an rbac:create holder can self-escalate.
		nativeRBACHandler.SetAuthorization(rbacEngine, rbacQuerier)
	}
	// Fleet operations (migration 056). Coordinated multi-cluster actions
	// (drain N clusters, upgrade a tool across the fleet, apply-template
	// fanout) with label-selector targeting + bounded blast radius.
	// Handler owns CRUD + state-transition endpoints; orchestrator worker
	// drives every pending/running row toward a terminal status.
	fleetOperationsHandler := handler.NewFleetOperationHandler(queries)
	fleetDispatcher := handler.NewFleetDispatcher(toolHandler, clusterTemplateHandler, queries)
	tasks.ConfigureFleetOrchestrate(tasks.FleetOrchestrateDeps{
		Queries:    queries,
		Dispatcher: fleetDispatcher,
	})
	// Task A2: durable agent-token rotation policy sweep. DB-only deps —
	// flags clusters due for rotation per their token_rotation_days policy.
	tasks.ConfigureAgentTokenRotate(tasks.AgentTokenRotateDeps{
		Queries: queries,
	})
	handler.RegisterClusterSnapshotsMetrics()
	handler.WireSnapshotWorkerMetrics()
	tasks.ConfigureClusterSnapshotTasks(tasks.ClusterSnapshotDeps{
		Queries: queries,
		Driver:  handler.NewVeleroDriverAdapter(requester),
		Log:     logger,
	})
	// Migration 071 — service mesh detector. The handler's POST /detect/
	// path delegates to tasks.DetectAndUpsert, so the deps need to be
	// configured here in addition to the worker scheduler that fires the
	// periodic sweep.
	tasks.ConfigureMeshDetect(tasks.MeshDetectDeps{
		Queries:   queries,
		Requester: requester,
	})
	// Sprint 069: CRD-mirror v2 cluster-detail read surface + tunnel
	// ingest router. The Hub routes MIRROR_EVENT frames into
	// MirrorRouter, which upserts into the mirrored_* tables; the REST
	// handler reads them back out for the cluster-detail page. Periodic
	// prune (every 30m) is wired via worker/scheduler.go.
	clusterResourcesHandler := handler.NewClusterResourcesHandler(queries)
	mirrorRouter := crd.NewMirrorRouter(queries)
	// Sprint 062: wire the trivy VulnerabilityReport ingester into the
	// same MirrorRouter the sprint-069 GVKs use. The agent emits trivy
	// CRs via the same MIRROR_EVENT channel with Kind=VulnerabilityReport;
	// the router dispatches them into scanner.Ingester which writes the
	// image_vulnerability_reports + image_vulnerabilities tables that
	// the Image Scans dashboard reads. Without this wiring trivy reports
	// on the cluster are silently dropped (the router used to error on
	// unknown kind; now it'd no-op without the ingester) — exactly the
	// "image scan tab is empty even though trivy is running" symptom
	// operators were seeing.
	// T6.062 — wire the Ingester's audit hook to the audit writer
	// so every successful Trivy ingest (and every delete that follows
	// a stale-report prune) lands a `image_vulns.ingested` audit row.
	// Was nil since the package landed; the hook surface existed but
	// no caller plumbed it.
	trivyAuditHook := scanner.AuditHook(func(ctx context.Context, clusterID uuid.UUID, reportName, action string) {
		audit.Record(ctx, queries, audit.Event{
			Source:       "crd_mirror",
			Action:       "image_vulns." + action,
			ResourceType: "image_vulnerability_report",
			ResourceID:   reportName,
			Detail: map[string]any{
				"cluster_id":  clusterID.String(),
				"report_name": reportName,
			},
		})
	})
	trivyIngester := scanner.NewIngester(queries, database.Pool(), nil, trivyAuditHook)
	mirrorRouter.SetVulnIngester(crd.NewVulnIngesterAdapter(trivyIngester.IngestUnstructured))
	hub.SetMirrorIngester(mirrorRouter)
	apiserverAuditHandler := handler.NewApiserverAuditHandler(handler.NewApiserverAuditStoreAdapter(queries))
	hub.SetAuditPersister(apiserverAuditHandler)
	// PATH A: mint the scoped apiserver-audit ingest token in CONNECT_ACK so an
	// agent configured with AUDIT_DELIVERY=http can authenticate its direct POST
	// to /clusters/{id}/apiserver-audit/ (clusters:write scope + cluster:update).
	if issuer := auth.NewIngestIssuer(queries); issuer != nil {
		hub.SetAuditIngestIssuer(issuer)
	}
	controlPlaneHandler := handler.NewControlPlaneHandler(queries, monitoringHandler, argocdHandler, toolHandler, catalogHandler, backupHandler, loggingHandler, securityHandler, queue)

	authHandler := handler.NewAuthHandlerWithTokens(queries, queries, jwtManager)
	authHandler.SetPasswordRehasher(queries)
	authHandler.SetRoleBindings(queries)
	authHandler.SetAuditWriter(queries)
	authHandler.SetLogger(logger)
	// Auth hardening (migration 039): account lockout + JWT session revocation.
	authHandler.SetLockoutQuerier(queries)
	authHandler.SetRevocationQuerier(queries)
	authHandler.SetLockoutPolicy(cfg.LoginFailureThreshold, time.Duration(cfg.LockoutDurationMinutes)*time.Minute)
	// Single sign-out (migration 054 / NIST 800-53 AC-12). Wired when
	// the encryptor is available: the stored upstream id_token is
	// Fernet-encrypted at rest, so without the key Logout has nothing
	// to decrypt. The post_logout_redirect_uri is derived from the
	// callback base URL — keeping it adjacent to the SSO redirect URI
	// means the same dashboard hostname is registered with the IdP for
	// both legs of the OIDC flow.
	authHandler.SetSSOSessionStore(queries)
	if encryptor != nil {
		authHandler.SetEncryptor(encryptor)
		callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
		authHandler.SetPostLogoutRedirectURL(callbackBase + "/auth/logout-done/")
	}

	// 2FA / TOTP (migration 043). The handler needs the Fernet
	// encryptor to wrap secrets at rest; without it we skip wiring so
	// /auth/totp/* returns 503 not_configured if invoked. The Login
	// gate degrades to the legacy password-only flow.
	var totpHandler *handler.TOTPHandler
	if encryptor != nil {
		totpHandler = handler.NewTOTPHandler(queries, queries, encryptor, jwtManager)
		totpHandler.SetIssuer(cfg.TOTPIssuer)
		totpHandler.SetAuditWriter(queries)
		totpHandler.SetLogger(logger)
		totpHandler.SetPasswordRehasher(queries)
		totpHandler.SetRequireAll(cfg.TOTPRequire)
		authHandler.SetTOTPGate(totpHandler)
		authHandler.SetTOTPRequireAll(cfg.TOTPRequire)
		// Runtime MFA-enforcement policy: read the admin-toggleable
		// `totp.required` platform setting on every login so flipping it
		// (via the settings API or a compliance baseline) takes effect
		// without a redeploy. OR'd with the static cfg.TOTPRequire knob
		// inside the handler.
		authHandler.SetTOTPPolicy(func(ctx context.Context) bool {
			row, err := queries.GetPlatformSetting(ctx, "totp.required")
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(row.Value) == 0) {
				return false // setting never written -> documented default
			}
			if err != nil {
				logger.Error("totp.required policy read failed; failing closed (enforcing MFA)", "error", err)
				return true // transient/unknown DB error -> enforce, do not bypass
			}
			var v bool
			if json.Unmarshal(row.Value, &v) != nil {
				return true // unparseable value -> enforce rather than silently disable
			}
			return v
		})
	}
	// Wire the same revocation + cutoff backend into the JWT validator
	// so the auth middleware enforces the deny-list on every authenticated
	// request — without it, Logout would write a row no validator ever
	// consults.
	jwtManager.SetRevocationChecker(handler.NewJWTRevocationChecker(queries))

	var ssoHandler *handler.SSOHandler
	if ssoManager != nil {
		ssoHandler = handler.NewSSOHandler(ssoManager, queries, jwtManager, "/")
		// SLO persistence (migration 054). Wire both the writer + the
		// encryptor so the Callback can store Fernet-encrypted upstream
		// id_tokens onto the sso_sessions row keyed by the access JWT's
		// JTI. Without either, the Callback silently skips persistence
		// and Logout degrades to "JWT revoked locally only".
		ssoHandler.SetSSOSessionWriter(queries)
		if encryptor != nil {
			ssoHandler.SetEncryptor(encryptor)
		}
	}

	// Phase B4 — Dex shim handler. Always wire it (even pre-encryption-key)
	// so the connector wizard is browseable at /auth/dex/connector-types/;
	// secret round-trips silently no-op when the encryptor is nil and the
	// /apply endpoint short-circuits with a 503 when the K8s requester is
	// unavailable.
	dexHandler := handler.NewDexHandler(queries)
	dexHandler.SetEncryptor(encryptor)
	dexHandler.SetK8sRequester(requester)
	dexHandler.SetLogger(logger)

	clusterHandler := handler.NewClusterHandler(queries)
	clusterHandler.SetEncryptor(encryptor)
	clusterHandler.SetAgentDisconnector(hub)
	clusterHandler.SetAgentImage(cfg.AgentImageRepository, cfg.AgentImageTag)
	clusterHandler.SetRegistrationTokenTTL(time.Duration(cfg.RegistrationTokenTTLHours) * time.Hour)
	clusterHandler.SetPullReconcileEnabled(cfg.PullReconcileEnabled)
	// HMAC key for short-TTL signed manifest-download URLs. Falls back to
	// the JWT signing secret when a dedicated one isn't configured so a
	// single-secret install still gets signed URLs.
	manifestSecret := strings.TrimSpace(cfg.ManifestSigningSecret)
	if manifestSecret == "" {
		manifestSecret = cfg.SecretKey
	}
	clusterHandler.SetManifestSigningSecret(manifestSecret)
	// Fleet-style PULL reconcile: wire the desired-state responder onto the
	// tunnel hub. Read-only rendering (agent manifest + enabled baseline
	// components), so it is wired unconditionally — the PullReconcileEnabled
	// flag gates whether the AGENT runs its loop, not whether the server can
	// describe the desired state. nil-safe on the hub side.
	hub.SetDesiredStateProvider(NewDesiredStateAdapter(clusterHandler, queries, queries))
	// Fan cluster.* lifecycle events out to SSE subscribers on Create / Update
	// / Delete. The bus implements the EventPublisher interface naturally.
	clusterHandler.SetEventPublisher(busPublisherAdapter{bus: bus})
	// Wizard handler (migration 078 / sprint 22). The handler owns the
	// phase-machine service; we hand a reference to the cluster handler
	// (so Create writes the first two step rows), to the tunnel hub (so
	// the first heartbeat advances awaiting_agent → connected), and to
	// the cluster_template:apply task wiring below.
	clusterRegistrationHandler := handler.NewClusterRegistrationHandler(queries, bus)
	clusterRegistrationHandler.SetApplyQueue(queue)
	clusterRegistrationHandler.SetTaskOutbox(queries)
	clusterRegistrationHandler.SetArgoCDAutoRegisterQueue(queue)
	clusterRegistrationHandler.Service().SetMetricsHook(observability.NewRegistrationMetricsHook())
	// Wire the platform-default cluster_templates row as the wizard's
	// auto-attach target. Without this lookup the operator's "install
	// platform baseline" checkbox is silently ignored (the handler
	// short-circuits when baselineTemplateID is uuid.Nil). The platform
	// config row is the same source the legacy auto-attach reads.
	if pcfg, pcfgErr := queries.GetPlatformConfig(ctx); pcfgErr == nil && pcfg.DefaultClusterTemplateID.Valid {
		clusterRegistrationHandler.SetBaselineTemplateID(uuid.UUID(pcfg.DefaultClusterTemplateID.Bytes))
	}
	clusterHandler.SetRegistrationService(clusterRegistrationHandler.Service())
	if hub != nil {
		hub.SetRegistrationAdvancer(&argoCDAutoRegisterAdvancer{
			base:       clusterRegistrationHandler.Service(),
			queue:      queue,
			taskOutbox: queries,
			log:        logger,
		})
	}
	// Wire the asynq client into the DELETE handler so the cluster
	// decommission reconciler fires immediately on remove-cluster click.
	// The periodic sweep is the safety net when redis is briefly down.
	clusterHandler.SetDecommissionQueue(queue)
	clusterHandler.SetTaskOutbox(queries)
	// Same client wires the Update handler -> argocd refresh task so a
	// labels mutation lands on every upstream ArgoCD cluster Secret without
	// the operator re-registering.
	clusterHandler.SetArgoCDRefreshQueue(queue)
	// Sprint 074 — auto-attach platform-default cluster_template on
	// Create enqueues the apply task immediately so the operator sees
	// the baseline operators install in seconds (not on the next
	// drift_check sweep). Best-effort; nil-safe.
	clusterHandler.SetTemplateApplyQueue(queue)
	// Wire metrics: tunnel requester for remote clusters, in-cluster clients
	// for the local cluster. Both are nil-safe; missing deps fall back to zero.
	clusterHandler.SetMetricsRequester(requester)
	// localK8s and localNamespace are reused below to construct the support
	// bundle handler; SetMetricsLocalClient / SetKubernetesClient consume
	// localK8s too.
	var localK8s kubernetes.Interface
	var localDyn dynamic.Interface
	if restCfg, err := rest.InClusterConfig(); err == nil {
		if cs, err := kubernetes.NewForConfig(restCfg); err == nil {
			localK8s = cs
			argocdHandler.SetKubernetesClient(cs)
			// The argocd:refresh_managed_cluster_labels task patches Secrets
			// in the control-plane's argocd namespace, so it shares the same
			// in-cluster k8s client. When in-cluster config is unavailable
			// (e.g. laptop dev) the task degrades to a logged warning and the
			// PATCH is skipped — the operator can re-register manually.
			tasks.ConfigureArgoCDRefresh(tasks.ArgoCDRefreshDeps{
				Queries: queries,
				K8s:     cs,
			})
			tasks.ConfigureArgoCDAutoRegister(tasks.ArgoCDAutoRegisterDeps{
				Queries:             queries,
				Encryptor:           encryptor,
				K8s:                 cs,
				ClusterProxyBaseURL: cfg.ArgoCDClusterProxyBaseURL,
				Registration:        clusterRegistrationHandler.Service(),
			})
			if mc, err := metricsv.NewForConfig(restCfg); err == nil {
				clusterHandler.SetMetricsLocalClient(cs, mc)
			} else {
				clusterHandler.SetMetricsLocalClient(cs, nil)
			}
		}
		if dyn, err := dynamic.NewForConfig(restCfg); err == nil {
			localDyn = dyn
		}
	}
	tasks.ConfigureCRDOwnershipDrift(tasks.CRDOwnershipDriftDeps{
		Queries: queries,
		Dynamic: localDyn,
	})
	// Even when in-cluster config fails, configure the refresh task with the
	// DB querier so the worker can at least report the no-k8s degradation
	// path cleanly. The K8s field stays nil → refreshSingleManagedClusterSecret
	// returns a clear "kubernetes client not configured" error.
	if localK8s == nil {
		tasks.ConfigureArgoCDRefresh(tasks.ArgoCDRefreshDeps{
			Queries: queries,
			K8s:     nil,
		})
		tasks.ConfigureArgoCDAutoRegister(tasks.ArgoCDAutoRegisterDeps{
			Queries:             queries,
			Encryptor:           encryptor,
			K8s:                 nil,
			ClusterProxyBaseURL: cfg.ArgoCDClusterProxyBaseURL,
			Registration:        clusterRegistrationHandler.Service(),
		})
	}
	localNamespace := detectReleaseNamespace()

	// Share the same metrics provider with the workload handler so per-node
	// CPU/memory usage on the node-detail page comes from the same fetch (and
	// the same cache) as the dashboard cluster card.
	workloadHandler.SetMetricsProvider(clusterHandler.MetricsProvider())

	resourceHandler := handler.NewResourceHandlerWithQueries(queries, requester)
	resourceHandler.SetEncryptor(encryptor)
	resourceHandler.SetSSOManager(ssoManager)
	resourceHandler.SetJWTManager(jwtManager)
	// Admin force-logout SLO clean-up (migration 054). The handler
	// enumerates the target user's sso_sessions rows and fires
	// best-effort back-channel end-session POSTs against each IdP
	// before deleting the rows. Wired unconditionally; the encryptor
	// gate inside the handler is what actually decides whether the
	// back-channel POST can fire.
	resourceHandler.SetSSOSessionStore(queries)
	resourceHandler.SetSSOBackchannelClient(handler.NewDefaultSSOBackchannelClient())
	// User delete cascades through *_role_bindings; signal the RBAC cache to
	// drop the per-user entry instead of waiting out the TTL.
	if cache := rbacQuerier.Cache(); cache != nil {
		resourceHandler.SetRBACCacheInvalidator(cache)
		// Group-sync (migration 042) mutates *_role_bindings under the
		// hood on every SSO login + every admin re-sync; the same
		// per-user cache invalidator handles both paths so the next
		// authenticated request reflects the post-sync state.
		if ssoHandler != nil {
			ssoHandler.SetRBACCacheInvalidator(cache)
		}
	}
	platformCharts, chartRepoErr := handler.NewPlatformChartRepoHandler()
	if chartRepoErr != nil {
		return nil, chartRepoErr
	}

	// SMTP email (migration 047). Wired only when the encryptor is
	// available — the password column is Fernet-encrypted and a
	// missing key would mean we couldn't round-trip a saved password.
	// The Enqueuer is best-effort across the codebase: every hook
	// site calls EnqueueAndLog so a missing SMTP relay never breaks a
	// user-facing action.
	var (
		smtpHandler   *handler.SMTPHandler
		emailEnqueuer *email.Enqueuer
	)
	if encryptor != nil {
		settingsProvider := email.NewSQLSettingsProvider(queries, encryptor, 5*time.Second)
		brandingProvider := email.NewPlatformConfigBrandingProvider(queries, "")
		smtpHandler = handler.NewSMTPHandler(queries, encryptor, logger)
		smtpHandler.SetSettingsProvider(settingsProvider)
		smtpHandler.SetBrandingProvider(brandingProvider)
		smtpHandler.SetAuditWriter(queries)
		smtpHandler.SetBaselineOverrideChecker(handler.NewBaselineOverrideChecker(rbacEngine, rbacQuerier))
		emailEnqueuer = email.NewEnqueuer(queries, brandingProvider, logger)
		// Bridge to the notification_templates override layer
		// (migration 059). Without this the Enqueuer renders only the
		// embedded defaults; with it the operator's per-key override
		// (if present + enabled) takes effect at enqueue time.
		emailEnqueuer.SetOverrideLookup(func(ctx context.Context, key string) (email.Overrides, bool) {
			res, err := notify.Resolve(ctx, queries, key)
			if err != nil || !res.HasOverride {
				return email.Overrides{}, false
			}
			return email.Overrides{Subject: res.Subject, BodyText: res.Body}, true
		})
		notifier := &emailNotifierAdapter{e: emailEnqueuer}
		authHandler.SetEmailNotifier(notifier)
		if totpHandler != nil {
			totpHandler.SetEmailNotifier(notifier)
		}
		resourceHandler.SetEmailNotifier(notifier)
		controlPlaneHandler.SetEmailNotifier(notifier)
		authHandler.SetPasswordResetStore(queries)
		// Tell the worker-task runtime about the SMTP plumbing so
		// the email:dispatch task can drain queued rows.
		tasks.ConfigureEmail(tasks.EmailDeps{
			Queries:  queries,
			Sender:   email.NewSender(settingsProvider, encryptor, logger),
			Provider: settingsProvider,
		})
	}

	// Outbound webhook subscriptions (migration 048). Wired only when
	// the encryptor is available — the HMAC signing secret is
	// Fernet-encrypted at rest. The Tap subscribes to the same in-memory
	// bus the SSE stream consumes; the dispatcher task drains pending
	// deliveries every 15s. Tap.Start runs further below alongside the
	// other reconciler goroutines once reconcileCtx is defined.
	var (
		webhookHandler *handler.WebhookHandler
		webhookTap     *webhook.Tap
	)
	if encryptor != nil {
		webhookHandler = handler.NewWebhookHandler(queries, encryptor, logger)
		webhookHandler.SetAuditWriter(queries)
		webhookHandler.SetBaselineOverrideChecker(handler.NewBaselineOverrideChecker(rbacEngine, rbacQuerier))
		webhookSender := webhook.NewSender(nil) // default http.Client
		// Bridge to the notification_templates override layer
		// (migration 059). The webhook precedence order becomes
		// subscription template → operator override → JSON marshal;
		// see internal/webhook/sender.go buildBody for details.
		webhookSender.SetOverrideLookup(func(ctx context.Context, key string) (string, bool) {
			res, err := notify.Resolve(ctx, queries, key)
			if err != nil || !res.HasOverride {
				return "", false
			}
			return res.Body, true
		})
		tasks.ConfigureWebhook(tasks.WebhookDeps{
			Queries:   queries,
			Sender:    webhookSender,
			Encryptor: encryptor,
		})
		webhookTap = webhook.NewTap(queries, bus, logger)
		webhookHandler.SetTap(webhookTap)
		// Bridge audit.Record → bus so audit.* events fan out into
		// webhook deliveries without every audit call site having to
		// know about webhooks.
		audit.SetBusPublisher(busPublisherAdapter{bus: bus})
	}

	// External SIEM forwarders (migration 055). Same gate as webhooks
	// (the auth blob is Fernet-encrypted) and parallel wiring: the bus
	// tap subscribes to the same events.Bus the SSE stream consumes,
	// and the dispatcher task drains the per-forwarder queue every 2s.
	// We start the tap further below once reconcileCtx is defined.
	var (
		siemHandler *handler.SIEMHandler
		siemTap     *siem.BusTap
	)
	if encryptor != nil {
		siemHandler = handler.NewSIEMHandler(queries, encryptor, logger)
		siemHandler.SetAuditWriter(queries)
		tasks.ConfigureSIEM(tasks.SIEMDeps{
			Queries:   queries,
			Encryptor: encryptor,
		})
		siemTap = siem.NewBusTap(queries, bus, webhook.MatchFilters, logger)
		siemHandler.SetTap(siemTap)
	}

	// Migration 057: shared maintenance-window evaluator. One
	// evaluator backs both the admin handler (which invalidates on
	// writes) and every destructive mutation handler's gate. 30s TTL
	// keeps the per-mutation check cheap; default operator stance is
	// zero windows, so the steady-state cost is one cached read per
	// gated request.
	maintenanceEvaluator := maintenance.NewEvaluator(queries)
	// Warn-level startup audit for permitted-mode + empty-op-types
	// windows: those refuse ALL destructive ops outside the window,
	// which is the most dangerous configuration. The handler validation
	// won't reject this combination because it's a valid operator
	// choice, but it MUST be intentional.
	maintenanceStartupWarn(ctx, queries, logger)

	// Stream tickets must be validatable on ANY replica (the pod that mints the
	// ?ticket= and the pod nginx pins the WebSocket to are independently
	// load-balanced). Back them with Redis — already hard-required infra (asynq
	// + the tunnel locator use the same URL) — so a ticket is single-use
	// cluster-wide. Fall back to per-pod in-memory only if the URL won't parse
	// (single-replica dev), which correctly limits browser exec/logs/shell to
	// one replica there.
	streamTickets := auth.NewStreamTicketStore(time.Minute)
	if backend, terr := auth.NewRedisStreamTicketBackendFromURL(cfg.RedisURL); terr != nil {
		logger.Warn("stream tickets: redis backend unavailable, using per-pod in-memory store (multi-replica browser exec/logs/shell auth will fail ~50%)", "error", terr)
	} else {
		streamTickets = auth.NewStreamTicketStoreWithBackend(time.Minute, backend)
	}
	streamTicketHandler := handler.NewStreamTicketHandler(streamTickets)
	streamTicketHandler.SetAuthorization(rbacEngine, rbacQuerier)

	deps := RouterDependencies{
		JWT:                 jwtManager,
		Encryptor:           encryptor,
		AuthQueries:         queries,
		AuditWriter:         queries,
		ArgoCDProxyTokens:   queries,
		StreamTickets:       streamTicketHandler,
		StreamTicketStore:   streamTickets,
		Auth:                authHandler,
		TOTP:                totpHandler,
		SSO:                 ssoHandler,
		Clusters:            clusterHandler,
		ClusterTemplates:    clusterTemplateHandler,
		ClusterRegistration: clusterRegistrationHandler,
		ClusterRegistries:   clusterRegistriesHandler,
		NetworkPolicies:     networkPoliciesHandler,
		Gatekeeper: func() *handler.GatekeeperConstraintsHandler {
			h := handler.NewGatekeeperConstraintsHandler(queries, requester)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetAuditWriter(queries)
			return h
		}(),
		ClusterSnapshots:      clusterSnapshotsHandler,
		ControlPlaneSnapshots: controlPlaneSnapshotHandler,
		FleetOperations:       fleetOperationsHandler,
		Projects:              projectHandler,
		Tools:                 toolHandler,
		Audit:                 handler.NewAuditHandler(queries),
		Alerting:              handler.NewAlertingHandlerWithDeps(queries, requester),
		Anomaly:               anomalyHandler,
		ArgoCD:                argocdHandler,
		Backups:               backupHandler,
		Catalog:               catalogHandler,
		// Migration 055: chart-rating + recommendation surface. Bound
		// to the same *sqlc.Queries used for the rest of the catalog
		// so audit / superuser checks see the same row.
		ChartRatings: func() *handler.ChartRatingsHandler {
			h := handler.NewChartRatingsHandler(queries)
			h.SetLogger(logger)
			return h
		}(),
		Logging:        loggingHandler,
		Monitoring:     monitoringHandler,
		ControlPlane:   controlPlaneHandler,
		Resources:      resourceHandler,
		PlatformCharts: platformCharts,
		Docs:           handler.NewDocsHandler(),
		SSOPresets:     handler.NewSSOPresetsHandler(),
		ResourcesSearch: func() *handler.ResourcesSearchHandler {
			h := handler.NewResourcesSearchHandler(queries, requester)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetAuditWriter(queries)
			return h
		}(),
		AgentFleet: func() *handler.AgentFleetHandler {
			h := handler.NewAgentFleetHandler(queries)
			h.SetAgentUpgradeTarget(cfg.AgentImageRepository, cfg.AgentImageTag)
			h.SetK8sRequester(requester)
			return h
		}(),
		Readyz:    newReadinessHandler(database, queue, hub).withLocatorError(locatorReadinessErr),
		DexConfig: dexHandler,
		RBAC: func() *handler.RBACHandler {
			h := handler.NewRBACHandler(queries)
			// Wire authorization so the privilege-escalation guard on
			// role-binding creation (and MyRoles/CheckMyRole) evaluates real
			// caller bindings. Without this, guard*Binding no-ops (fails open).
			h.SetAuthorization(rbacEngine, rbacQuerier)
			// T1.1 — load the embedded role-templates catalog. We fail
			// hard here because a busted template file means a buyer-
			// facing endpoint silently returns no data; the loader's
			// schema checks catch most authoring errors so the boot
			// failure surfaces them immediately in CI.
			cat, lerr := rbac.LoadCatalog()
			if lerr != nil {
				logger.Error("failed to load RBAC template catalog", "error", lerr)
			} else {
				h.SetTemplateCatalog(cat)
			}
			return h
		}(),
		RBACQueries:         rbacQuerier,
		RBACEngine:          rbacEngine,
		NamespaceScopedRBAC: cfg.NamespaceScopedRBACEnabled,
		Security:            securityHandler,
		ApiserverAudit:      apiserverAuditHandler,
		ImageVulns: func() *handler.ImageVulnHandler {
			h := handler.NewImageVulnHandler(queries)
			h.SetK8sRequester(requester)
			h.SetAuditQuerier(queries)
			h.SetLogger(logger)
			return h
		}(),
		ServiceProxy: func() *handler.ServiceProxyHandler {
			h := handler.NewServiceProxyHandler(requester)
			h.SetToolQuerier(queries)
			h.SetAuditWriter(queries)
			return h
		}(),
		Workloads: workloadHandler,
		Hub:       hub,
		Proxy:     tunnel.NewProxyHandler(hub, logger),
		InternalK8s: func() *tunnel.InternalK8sHandler {
			h := tunnel.NewInternalK8sHandler(hub, tunnel.DerivePSK(cfg.EncryptionKey), logger)
			// Audit every mutation crossing the internal door, attributed
			// to the originating user — same writer the user-facing proxy
			// records through (M6 / C2t).
			h.SetAuditWriter(queries)
			return h
		}(),
		InternalHelm: func() *tunnel.InternalHelmHandler {
			h := tunnel.NewInternalHelmHandler(hub, tunnel.DerivePSK(cfg.EncryptionKey), logger)
			h.SetAuditWriter(queries)
			return h
		}(),
		Exec:          tunnel.NewExecConsumer(hub, logger),
		Logs:          tunnel.NewLogsConsumer(hub, logger),
		RemoteServer:  remoteServer,
		RemoteQueries: queries,
		EventStream:   handler.NewEventStreamHandler(bus),
		// Top-of-dashboard health rollup.
		PlatformHealth: func() *handler.PlatformHealthHandler {
			h := handler.NewPlatformHealthHandler(database.Pool())
			h.SetAsynqInspector(asynq.NewInspector(redisOpt))
			return h
		}(),
		// Admin queue inspector.
		AdminQueues:     handler.NewAdminQueuesHandler(asynq.NewInspector(redisOpt), queries),
		AdminTaskOutbox: handler.NewAdminTaskOutboxHandler(queries),
		// Admin backup-restore drill viewer — reads rows that the
		// management-plane-restore-drill CronJob writes to
		// backup_drill_results. Superuser-gated inside the handler.
		AdminDrill: handler.NewAdminDrillHandler(queries),
		// Management-plane log tail (T03 FEATURES-051226). Only wired
		// when the in-cluster k8s client is available — laptop dev /
		// test fakes get a nil-safe omission instead of a panicking
		// list call. Caps are read from MANAGEMENT_LOGS_* env vars set
		// by the chart's ConfigMap; defaults (1000 lines / 1 MiB)
		// apply when those are empty.
		ManagementLogs: func() *handler.ManagementLogsHandler {
			if localK8s == nil || localNamespace == "" {
				return nil
			}
			h := handler.NewManagementLogsHandler(queries, localK8s, localNamespace, os.Getenv("RELEASE_NAME"))
			maxLines, _ := strconv.Atoi(os.Getenv("MANAGEMENT_LOGS_MAX_LINES"))
			maxBytes, _ := strconv.Atoi(os.Getenv("MANAGEMENT_LOGS_MAX_BYTES"))
			h.SetCaps(maxLines, maxBytes)
			return h
		}(),
		// SMTP admin + email enqueuer (migration 047). Both are nil
		// when the encryptor isn't configured; the router wiring is
		// nil-safe.
		SMTP:           smtpHandler,
		EmailEnqueuer:  emailEnqueuer,
		Webhooks:       webhookHandler,
		SIEMForwarders: siemHandler,
		// Notification-template overrides (migration 059). Always
		// non-nil — the table is unconditional. The wiring below
		// also attaches OverrideLookup closures to the email
		// Enqueuer and the webhook Sender so dispatchers consult
		// overrides at delivery time.
		NotificationTemplates: func() *handler.NotificationTemplateHandler {
			h := handler.NewNotificationTemplateHandler(queries, logger)
			h.SetAuditWriter(queries)
			return h
		}(),
		// Identity-group sync admin endpoints (migration 042). CRUD
		// over identity_group_mappings + per-user re-sync. The RBAC
		// cache invalidator is wired below once rbacQuerier is known
		// to support it (it's a *SQLCRBACQuerier in prod).
		GroupMappings: handler.NewGroupMappingsHandler(queries),
		// SCIM 2.0 provisioning (migration 114). Bearer-token-authed
		// /scim/v2/* User CRUD + read-only Group list, mapped onto the
		// existing users + identity_group_mappings tables.
		SCIM: handler.NewSCIMHandler(queries),
		// Operator-facing admin surface to mint/list/revoke the static
		// bearer tokens the /scim/v2/* chain authenticates against.
		SCIMTokenAdmin: handler.NewSCIMTokenAdminHandler(queries),
		SupportBundle: func() *handler.SupportBundleHandler {
			h := handler.NewSupportBundleHandler(queries, localK8s, localNamespace)
			// Enable the asynq-queues + schema-
			// migrations sections by wiring the inspector and the DB pool.
			h.SetAsynqInspector(asynq.NewInspector(redisOpt))
			h.SetDBPool(database.Pool())
			return h
		}(),
		// SOC 2 / ISO 27001 audit-prep bundle. Streams inline; the
		// former async `compliance:export` path is disabled until it has
		// durable status/output state and a registered worker.
		Compliance:        handler.NewComplianceHandler(queries, queue),
		CompliancePosture: handler.NewCompliancePostureHandler(queries, cfg.AuditLogRetentionMonths),
		License:           handler.NewLicenseHandler(),
		// Rancher-style global settings hub (migration 046). The
		// SettingsCache is shared between the settings handler (PUT /
		// DELETE invalidate) and the FeatureGate middleware (reads).
		// 30s TTL — settings change rarely; the cache makes the
		// per-request feature check effectively free.
		PlatformSettings: handler.NewPlatformSettingsHandler(queries),
		SettingsCache:    handler.NewSettingsCache(queries, 30*time.Second),
		Extensions: func() *handler.ExtensionHandler {
			h := handler.NewExtensionHandler(queries)
			h.SetAuditWriter(queries)
			// §DataProxy — wire the RBAC engine + the SAME bindings
			// querier the route middleware uses, so the per-call
			// permission re-check in ProxyData runs against the
			// requesting user's own bindings (never the extension's).
			h.SetRBAC(rbacEngine, rbacQuerier)
			// §BridgeProtocol — the Tier-2 bridge ticket store. Opaque,
			// sha256-hashed at rest, single-use, ≤60s, scoped to
			// {user,extension,dataSource,cluster}. Backs both the
			// ext/token.request issuer and the X-Extension-Ticket
			// validator the data proxy consumes.
			extTickets := auth.NewExtensionTicketStore(time.Minute)
			h.SetExtensionTickets(extTickets)
			h.SetExtensionTicketIssuer(extTickets)
			// Ed25519 public key (base64) that signed extension
			// bundles must verify against. Absent => bundle
			// verification fails closed.
			if err := h.SetTrustedBundleKey(os.Getenv("EXTENSION_BUNDLE_TRUSTED_KEY")); err != nil {
				logger.Warn("invalid EXTENSION_BUNDLE_TRUSTED_KEY; bundle verification will fail closed", "error", err)
			}
			return h
		}(),
		// Sprint 074 — platform-default cluster template.
		PlatformDefaultTemplate: func() *handler.PlatformDefaultTemplateHandler {
			h := handler.NewPlatformDefaultTemplateHandler(queries)
			h.SetApplyQueue(queue)
			h.SetTaskOutbox(queries)
			return h
		}(),
		// Sprint 075: slug-coverage endpoint for the platform-baseline.
		PlatformBaselineCoverage: handler.NewPlatformBaselineCoverageHandler(queries),
		// Per-tenant resource quotas (migration 051). The handler is
		// constructed first so the enforcer below can borrow the same
		// queries surface. The enforcer wires into clusters / auth /
		// rbac handlers via SetQuotaEnforcer (see below).
		Quotas: handler.NewQuotaHandler(queries),
		// Cloud credentials (migration 053). Provider-typed secrets at
		// the project level + a materialization worker that fans them
		// out to in-cluster k8s Secrets.
		CloudCredentials: cloudCredentialsHandler,
		// Maintenance windows (migration 057). Operator-defined time
		// windows that gate destructive ops; same handler also owns the
		// /admin/deferred-operations/* admin surface for the queued-op
		// drain. The evaluator wired here is shared with the
		// MaintenanceGate that each destructive handler reads from on
		// every gated mutation, so PUT/DELETE invalidations are reflected
		// in the per-request check instantly.
		Maintenance: handler.NewMaintenanceHandler(queries, maintenanceEvaluator),
		Dashboards:  dashboardsHandler,
		// GitOps cluster registration (migration 060). Operators commit
		// ClusterRegistration YAML to a tracked Git repo and Astronomer
		// reconciles via the gitops:sync worker. The handler exposes
		// CRUD over the gitops_registration_sources rows plus manual
		// /sync/ + dry-run /preview/ subroutes; the runner adapter
		// shells into the worker-tasks package so the same code path
		// drives both the periodic tick and the manual button.
		GitOps: func() *handler.GitOpsHandler {
			h := handler.NewGitOpsHandler(queries, handler.DefaultGitOpsSyncRunner(), logger)
			h.SetAuditWriter(queries)
			// T6 item 060 — wrap auth blobs with the Fernet encryptor
			// when one is available. nil-safe: development / tests
			// without an encryption key keep working but leave the
			// blob plaintext.
			h.SetEncryptor(encryptor)
			// Shared secret for inbound git-provider push webhooks. Empty =
			// the /gitops/sources/{id}/webhook/ endpoint stays disabled (503).
			h.SetWebhookSecret(cfg.GitopsWebhookSecret)
			return h
		}(),
		ProjectCatalogs: projectCatalogsHandler,
		// Compliance baselines (migration 064 — sprint 17). The
		// handler needs the *pgxpool.Pool to begin transactions
		// around Apply / Revert; passing the pool plus a
		// sqlc.New(tx) factory keeps the engine pool-agnostic for
		// unit tests.
		ComplianceBaselines: handler.NewComplianceBaselinesHandlerFromPool(database.Pool(), logger),
		// In-browser kubectl shell (migration 065 / sprint 17). The
		// chart value kubectlShell.enabled=false default keeps this
		// off in fresh installs; operators flip it on once their
		// audit-log retention is sized for the per-command rows.
		KubectlShell: kubectlShellHandler(queries, rbacQuerier, rbacEngine, requester, cfg, logger),
		// Cluster groups (migration 066). Operator-defined folder hierarchy
		// over clusters with a tree depth cap of 3.
		ClusterGroups: clusterGroupsHandler,
		// Vault integration (migration 067). Admin CRUD over
		// vault_connections + project default pointer.
		Vault: vaultHandler,
		// Sprint 069: CRD-mirror v2 read endpoints (ingress-classes,
		// gateway-classes, network-policies, resource-quotas, limit-ranges).
		ClusterResources: clusterResourcesHandler,
		// Migration 071 — service mesh tile on the cluster detail page.
		// GET serves the cached row; the optional detector wires the
		// POST /detect/ path. Detector is only wired when worker mesh-detect
		// deps are configured upstream; the handler returns a 503 from
		// POST otherwise. GET works regardless.
		ServiceMesh: func() *handler.ServiceMeshHandler {
			h := handler.NewServiceMeshHandler(queries)
			h.SetRequester(requester)
			h.SetAuditor(queries)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			h.SetDetector(handler.MeshDetectorFunc(tasks.DetectAndUpsert))
			return h
		}(),
	}
	// Wire the asynq client into the alerting handler so the "Test Channel"
	// endpoint can dispatch a real notification:send task.
	deps.Alerting.SetEnqueuer(queue)
	// Migration 063 — read-side audit. The PolicyEvaluator is shared
	// between the middleware and the admin handler so policy writes
	// invalidate the 30s cache. Both are nil-safe; when queries are
	// unwired (test fakes) the middleware is omitted from the router.
	if queries != nil {
		readAuditEval := appmiddleware.NewPolicyEvaluator(queries)
		readAuditHandler := handler.NewReadAuditPolicyHandler(queries, logger)
		readAuditHandler.SetAuditWriter(queries)
		readAuditHandler.SetCacheInvalidator(readAuditEval)
		deps.ReadAuditEvaluator = readAuditEval
		deps.ReadAuditPolicies = readAuditHandler
	}
	if deps.PlatformSettings != nil && deps.SettingsCache != nil {
		deps.PlatformSettings.SetCache(deps.SettingsCache)
	}
	// Wire the shared settings cache into the dashboard handler so the
	// iframe-host allow-list reads from the same in-process cache the
	// FeatureGate middleware uses.
	if deps.Dashboards != nil && deps.SettingsCache != nil {
		deps.Dashboards.SetSettingsCache(deps.SettingsCache)
	}

	// Wire the quota enforcer into the create-side handlers and start
	// the periodic usage reporter. The enforcer is optional on each
	// handler (SetQuotaEnforcer is nil-safe), so we only attach it
	// when both queries and the deps wiring are present.
	quotaEnforcer := quota.New(queries, logger)
	quota.MustRegister()
	quota.StartReporter(ctx, queries, logger)
	// Sprint 062 — image vuln scanner ingest metrics. Registration is
	// idempotent so re-init in tests is safe.
	scanner.MustRegisterMetrics()
	if deps.Clusters != nil {
		deps.Clusters.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.Auth != nil {
		deps.Auth.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.RBAC != nil {
		deps.RBAC.SetQuotaEnforcer(quotaEnforcer)
	}

	// Wire the migration-057 maintenance window gate into every
	// destructive mutation handler. The gate shares a single Evaluator
	// (30s in-memory cache) so the per-mutation check is cheap; the
	// MaintenanceHandler invalidates the cache on POST/PUT/DELETE.
	maintenanceGate := handler.NewMaintenanceGate(maintenanceEvaluator, queries)
	if deps.Clusters != nil {
		deps.Clusters.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Projects != nil {
		deps.Projects.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Tools != nil {
		deps.Tools.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Catalog != nil {
		deps.Catalog.SetMaintenanceGate(maintenanceGate)
	}
	if deps.ClusterTemplates != nil {
		deps.ClusterTemplates.SetMaintenanceGate(maintenanceGate)
	}
	// The deferred-op dispatcher worker also needs the queries surface;
	// op-type replayers are registered by each handler / task package's
	// init in a follow-up commit. For now register a no-op replayer set
	// so the dispatcher's failure mode is "mark failed" rather than a
	// panic on unknown types.
	tasks.ConfigureDeferredDispatch(tasks.DeferredDispatchDeps{
		Queries:   queries,
		Replayers: map[string]tasks.DeferredReplayer{},
	})
	// EventSource cannot send Authorization headers. Wire both the legacy
	// token fallback and the preferred one-use stream-ticket validator.
	deps.EventStream.SetAuth(jwtManager, queries)
	deps.EventStream.SetStreamTickets(deps.StreamTicketStore)
	// Group-sync admin re-sync mutates the user's bindings; share the
	// same RBAC cache invalidator the SSO + user handlers use so the
	// effect is immediate on the next authenticated request.
	if cache := rbacQuerier.Cache(); cache != nil && deps.GroupMappings != nil {
		deps.GroupMappings.SetRBACCacheInvalidator(cache)
	}
	// Browser WebSocket clients can't set Authorization either; wire the
	// same stream-ticket and header-auth validation into pod exec.
	deps.Exec.SetAuth(jwtManager, queries)
	deps.Exec.SetStreamTickets(deps.StreamTicketStore)
	deps.Exec.SetAuditWriter(queries)
	// Per-cluster RBAC on the Authorization-header exec path (the ?ticket= path
	// is already gated at ticket issuance). Without this an authenticated user
	// could exec into any pod on any cluster via a raw XHR/curl bearer token.
	deps.Exec.SetAuthorization(rbacEngine, rbacQuerier)

	// Browser `new WebSocket(...)` cannot set Authorization either — the pod
	// logs WS handler accepts one-use stream tickets and the legacy fallback.
	// Without this hook the route would accept unauthenticated connections,
	// which would leak pod log contents to anyone who can reach the API.
	if deps.Logs != nil {
		deps.Logs.SetAuth(jwtManager, queries)
		deps.Logs.SetStreamTickets(deps.StreamTicketStore)
		deps.Logs.SetAuditWriter(queries)
		deps.Logs.SetAuthorization(rbacEngine, rbacQuerier)
	}

	// Sprint 17+/082+: same fix for the kubectl-shell session-aware WS
	// route. Without this the route 401s every browser handshake.
	if deps.KubectlShell != nil {
		deps.KubectlShell.SetStreamAuth(jwtManager, queries)
		deps.KubectlShell.SetStreamTickets(deps.StreamTicketStore)
		// Opt-in caller-RBAC scoping. Reads feature.shell_scope_to_caller
		// (default false via BoolValue's false fallback), so this only
		// changes behaviour once an operator flips the flag on.
		if deps.SettingsCache != nil {
			deps.KubectlShell.SetFeatureFlags(deps.SettingsCache)
		}
		// v2 (Firefox-portable): bridge the inbound WS onto the cluster
		// agent via the existing exec relay instead of 307-redirecting
		// to /api/v1/ws/exec/. The shell handler validates the session
		// row + ownership; the exec consumer owns the agent fan-out.
		if deps.Exec != nil {
			deps.KubectlShell.SetExecProxy(deps.Exec)
		}
		// Multi-replica WS hand-off. When the cluster's tunnel is held
		// by a sibling pod, the shell handler forwards the HTTP-Upgrade
		// to that sibling before websocket.Accept so K8S stream frames
		// always flow on the pod that owns the agent. Mirrors the
		// HTTP-side forwardToOwnerPod fallback. hub is the same
		// *tunnel.Hub the HTTP proxy uses; in single-pod deploys the
		// locator is nil and the closure returns false.
		deps.KubectlShell.SetCrossPodWSForwarder(func(w http.ResponseWriter, r *http.Request, clusterID string) bool {
			return tunnel.ForwardWSToOwnerPod(hub, logger, w, r, clusterID)
		})
	}

	// Native RBAC — set the interface fields only when the feature is on, so
	// deps.NativeAuthz stays a genuine nil interface (not a typed-nil) and the
	// proxy authz hook's `native == nil` fast path holds.
	if nativeRBACAuthz != nil {
		deps.NativeAuthz = nativeRBACAuthz
		deps.NativeRBAC = nativeRBACHandler
	}

	// ArgoCD UI reverse proxy. Defaults to the in-cluster service URL but
	// is overridable via the `ARGOCD_UI_UPSTREAM` env var. If construction
	// fails (e.g. the URL is malformed), the proxy stays nil and the route
	// registration in NewRouter no-ops — the rest of the app still boots.
	upstream := cfg.ArgoCDUIUpstream
	if upstream == "" {
		upstream = "http://argocd-server.argocd.svc.cluster.local:80"
	}
	if argoUIProxy, err := handler.NewArgoCDUIProxy(upstream, logger); err != nil {
		logger.Warn("argocd UI proxy disabled", "error", err)
	} else {
		// Single sign-on: wire a token source that decrypts the local-cluster
		// ArgoCD instance's stored auth_token on demand. The proxy injects
		// that as the upstream `argocd.token` cookie so users skip ArgoCD's
		// own login page. Only effective when the encryptor is configured
		// AND a local-cluster instance row has been created.
		if encryptor != nil {
			argoUIProxy.SetSessionTokenSource(&localClusterArgoCDTokenSource{
				queries:   queries,
				encryptor: encryptor,
				log:       logger,
			})
		}
		argoUIProxy.SetAuditWriter(queries)
		deps.ArgoCDUIProxy = argoUIProxy
	}

	if err := validateProductionSecurityWiring(cfg, deps); err != nil {
		database.Close()
		return nil, err
	}

	router := NewRouter(cfg, deps)

	s := &Server{
		handler:               router,
		internalArgoCDHandler: NewInternalArgoCDProxyRouter(deps),
		logger:                logger,
		db:                    database,
		queue:                 queue,
		hub:                   hub,
		Encryptor:             encryptor,
		SSO:                   ssoManager,
	}
	s.httpServer = &http.Server{
		// Wrap with otelhttp so every request emits a server span
		// when the global TracerProvider has an exporter; no-op when it
		// doesn't.
		Handler: wrapWithTracing(router),
		// ReadHeaderTimeout caps the slowloris exposure but does not bound the
		// long-lived WebSocket tunnel connection (which lives in /api/v1/ws/...).
		// Keep ReadTimeout/WriteTimeout at zero so the WS connection is not
		// forcibly closed mid-stream. Per-handler timeouts cover REST routes.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	reconcileCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	monitoringHandler.StartReconciler(reconcileCtx)
	argocdHandler.StartReconciler(reconcileCtx)
	backupHandler.StartReconciler(reconcileCtx)
	toolHandler.StartReconciler(reconcileCtx)
	catalogHandler.StartReconciler(reconcileCtx)
	loggingHandler.StartReconciler(reconcileCtx)
	controlPlaneHandler.StartEvaluator(reconcileCtx)
	workloadHandler.StartReconciler(reconcileCtx)
	// A4: reap expired connect-failure buckets so the limiter map stays bounded.
	connLimiter.StartJanitor(reconcileCtx, 0)
	// Migration 048: kick off the webhook bus tap if wired. Subscribes
	// to the in-memory events.Bus and turns matching events into
	// queued webhook_deliveries rows.
	if webhookTap != nil {
		webhookTap.Start(reconcileCtx)
	}
	// Migration 055: kick off the SIEM bus tap. Same bus, different
	// destination — events fan out into siem_forward_queue for the
	// dispatcher to drain. Operators using both webhooks AND SIEM see
	// each event in both pipelines simultaneously.
	if siemTap != nil {
		siemTap.Start(reconcileCtx)
	}
	// Phase B3 — configure the worker-task runtime in this process too, so the
	// in-process project reconciler (and other server-side cron sweeps that
	// rely on the same runtime) have the K8sRequester. The dedicated worker
	// process configures a runtime without K8s access — it runs DB-only tasks.
	// This server-side runtime is the one that actually applies manifests
	// through the tunnel.
	tasks.ConfigureRuntime(tasks.RuntimeDependencies{
		Queries:        queries,
		Log:            logger,
		AgentImageRepo: cfg.AgentImageRepository,
		AgentImageTag:  cfg.AgentImageTag,
		PlatformName:   "Astronomer",
		Leader:         leader.New(database.Pool(), logger),
		K8s:            requester,
		Enqueuer:       queue,
	})
	// Migration 092: durable task outbox dispatcher. Handlers can commit a
	// task_outbox row in the same transaction as product state, and this
	// leader-elected dispatcher retries Redis/Asynq delivery until the task
	// is durably queued.
	tasks.ConfigureTaskOutboxDispatch(tasks.TaskOutboxDispatchDeps{
		Queries:  queries,
		Enqueuer: queue,
	})
	// Cluster decommission reconciler: needs DB + the tunnel hub for both
	// MsgDecommission RPC and forced Disconnect after token revoke. When the
	// hub is unavailable (worker-only process), pass nil — the reconciler
	// falls back to "agent unreachable, skip with warning" semantics.
	tasks.ConfigureClusterDecommission(tasks.ClusterDecommissionDeps{
		Queries: queries,
		Tunnel:  hub,
		K8s:     localK8s,
		// Bulk-deleting cluster_role_bindings during decommission strands
		// stale per-user entries in the middleware RBAC cache; flush them.
		RBACCache: rbacQuerier.Cache(),
	})
	// Cluster-template apply worker (migration 049). The Installer bridge
	// reuses the existing ToolHandler.EnsureInstalled — see the comment on
	// tasks.ToolInstaller for why we narrow the surface.
	tasks.ConfigureClusterTemplateApply(tasks.ClusterTemplateApplyDeps{
		Queries:      queries,
		Installer:    toolHandler,
		Registration: clusterRegistrationHandler.Service(),
	})
	// Recovery sweep: drift_check re-enqueues `failed` apply rows
	// through the same tunnel queue used by the operator-initiated
	// reapply path. Optional — drift_check still does its main job
	// when this is unwired (single-binary tests without an asynq
	// client).
	tasks.ConfigureFailedApplyEnqueuer(queue)
	// P1 item 16/22: tool drift reconciliation sweep. Reuses the tunnel
	// HelmRequester (helm Status RPCs go through the agent WS, which only
	// terminates on the server pod) — runs on the server-embedded tunnel
	// worker, same as cluster_template:drift_check.
	tasks.ConfigureToolDriftSweep(tasks.ToolDriftSweepDeps{
		Queries: queries,
		Helm:    helmRequester,
	})
	// Spin up the tunnel-queue asynq Server inside the server pod. The
	// apply path needs the WS-terminated tunnel hub (which lives only
	// here, not in the standalone worker pod) to execute helm install /
	// upgrade against the agent. The handler call above only wires the
	// runtime deps; without this Server nothing actually drains tasks
	// from the "tunnel" queue and apply rows would sit at pending.
	if cfg.RedisURL != "" {
		tw, twErr := worker.NewTunnelWorker(cfg.RedisURL, cfg.TunnelWorkerConcurrency, logger)
		if twErr != nil {
			logger.Error("failed to create tunnel-queue asynq server", "error", twErr)
		} else {
			tw.RegisterTunnelHandlers()
			s.tunnelWorker = tw
		}
	}
	// Cluster-registry apply worker (migration 050). Shares the
	// ProjectK8sRequester adapter that the project reconciler already
	// installs; the bridge decoder is in internal/handler/projects.go.
	clusterRegistriesHandler.ConfigureWorkerDeps(queries)
	// Cloud-credentials materialization worker (migration 053). Reuses
	// the same handler.K8sRequester → tasks.ProjectK8sRequester adapter
	// the project reconciler and cluster-registry apply paths already
	// use. Decryptor is the same Fernet encryptor used by the handler so
	// ciphertexts round-trip through one key set across the worker /
	// API surface.
	tasks.ConfigureCloudCredentialMaterialize(tasks.CloudCredentialMaterializeDeps{
		Queries:   queries,
		Requester: handler.ProjectK8sRequesterFromHandlerRequester(requester),
		Decryptor: encryptor,
	})
	// GitOps sync worker (migration 060). Wires the periodic gitops:sync
	// task to the cluster_decommission enqueuer so on_delete=decommission
	// + the 24h tombstone reaper share the same decom code path. The
	// cache lives under /tmp/gitops/<source_id>; subsequent ticks fetch
	// instead of cloning, so worker restart is idempotent.
	tasks.ConfigureGitOps(tasks.GitOpsDeps{
		Queries:    queries,
		Enqueuer:   queue,
		TaskOutbox: queries,
		Log:        logger,
	})
	// Phase B3 — periodic project enforcement sweep (5-min cadence; cooperative
	// DB lease handles multiple worker pods racing on the same row).
	projectHandler.StartReconciler(reconcileCtx)
	// Kick an initial sweep so the first add-namespace doesn't wait a full
	// ticker tick before any enforcement lands. Best-effort — failures here
	// are logged inside HandleProjectReconcileAll and the periodic loop will
	// retry.
	go func() {
		time.Sleep(2 * time.Second)
		_ = tasks.HandleProjectReconcileAll(reconcileCtx, nil)
	}()

	// Live metrics + status publisher: emits cluster.metrics every 10s for
	// each active cluster and flips active<->disconnected when heartbeats
	// age past the threshold (with cluster.status_changed fan-out).
	livemetrics.New(bus, queries, clusterHandler.MetricsProvider(), logger).Start(reconcileCtx)
	tunnel.StartConnectionMetricsReporter(reconcileCtx, queries, logger)
	handler.StartArgoCDApplicationMetricsReporter(reconcileCtx, queries, logger)

	// Local cluster auto-registration (Rancher pattern). Both calls are
	// best-effort: when running outside a kubernetes cluster (laptop dev,
	// some test scenarios) rest.InClusterConfig fails and StartLocalAgent
	// degrades to a warning. The DB row still gets created so the UI shows
	// the management cluster even if its data plane is offline.
	if localCluster, err := bootstrapLocalCluster(reconcileCtx, logger, queries); err != nil {
		logger.Warn("local cluster bootstrap failed", "error", err)
	} else if localCluster != nil {
		// Tell the workload handler which cluster ID is local so node-detail
		// requests against it use the in-process k8s client (no tunnel hop).
		workloadHandler.SetLocalClusterID(localCluster.ID.String())
		if err := StartLocalAgent(reconcileCtx, logger, queries, localCluster.ID); err != nil {
			logger.Warn("local agent start failed", "error", err)
		}
		startLocalArgoSelfManagement(reconcileCtx, logger, cfg, queries, toolHandler, encryptor, localCluster)
	}

	// Probe-based cluster conditions (AgentReachable, GatewayAPISupported).
	// Heartbeat-derived conditions are handled by the worker's
	// cluster:health_check task; these probes need the tunnel-backed
	// K8sRequester which only the server process has.
	startClusterProbeReconciler(reconcileCtx, logger, queries, requester)

	// CRD-mirror controller (Rancher-style "kubectl get clusters.management
	// .astronomer.io"). Off by default; the chart wires CRD_ENABLED=true
	// when crds.enabled=true. In production an explicitly-enabled CRD API
	// must start successfully, otherwise the install would advertise a
	// declarative surface that is not reconciling.
	if err := startCRDController(reconcileCtx, logger, cfg, queries); err != nil {
		return nil, err
	}

	// Sprint 075: kick a first-boot catalog sync if the catalog is empty.
	// Migration 075 seeds three well-known helm_repositories rows; without
	// this kick, the cluster_template reconciler can't resolve its baseline
	// slugs (trivy-operator, kube-state-metrics, node-exporter, fluent-bit,
	// ingress-nginx, cert-manager, gatekeeper) until the periodic
	// catalog:sync ticker fires — which is
	// once every 6h (see internal/worker/scheduler.go). Enqueueing here
	// turns "wait 6 hours after fresh install" into "wait ~30s for the
	// worker to drain the queue".
	//
	// Best-effort: any failure here (Redis offline, DB hiccup, marshal
	// error) logs a warning and the periodic schedule still recovers the
	// platform within one 6h tick. NEVER fail server startup because the
	// enqueue didn't land.
	kickFirstBootCatalogSync(reconcileCtx, logger, queries, queue)

	// Reconcile the blessed catalog (astronomer-catalog/catalog.yaml) into the
	// default helm_repositories + catalog_blessed_charts overlays. Best-effort:
	// a blank URL is a no-op, and any fetch/parse error keeps the existing rows.
	if n, err := catalog.Load(reconcileCtx, queries, &http.Client{Timeout: 15 * time.Second}, cfg.CatalogURL); err != nil {
		logger.Warn("blessed catalog reconcile failed; keeping existing rows", "url", cfg.CatalogURL, "error", err)
	} else if n > 0 {
		logger.Info("blessed catalog reconciled", "entries", n, "url", cfg.CatalogURL)
	}

	return s, nil
}

// firstBootSyncCounter is the narrow DB surface kickFirstBootCatalogSync
// needs. *sqlc.Queries satisfies it; tests inject a fake.
type firstBootSyncCounter interface {
	CountHelmCharts(ctx context.Context) (int64, error)
}

// firstBootSyncEnqueuer is the narrow asynq surface kickFirstBootCatalogSync
// needs. *asynq.Client satisfies it; tests inject a recording fake.
type firstBootSyncEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// kickFirstBootCatalogSync enqueues a one-shot catalog:sync when the
// helm_charts table is empty. Idempotent: a second server start (or a
// scheduler-driven sync that already ran) leaves the catalog non-empty
// so this is a no-op. The 6h periodic schedule continues to handle
// steady-state catalog refresh.
//
// Wired separately from the scheduler so it runs on every server start
// (not just the leader's). asynq's redis-backed queue dedupes if two
// replicas race; HandleCatalogSync itself uses runPeriodicTaskWithLeader
// to make the actual sync single-flighted.
//
// Best-effort: any failure (Redis offline, DB hiccup, marshal error)
// logs a warning and returns silently. NEVER fails server startup.
func kickFirstBootCatalogSync(ctx context.Context, logger *slog.Logger, queries firstBootSyncCounter, queue firstBootSyncEnqueuer) {
	if queries == nil || queue == nil {
		return
	}
	n, err := queries.CountHelmCharts(ctx)
	if err != nil {
		logger.Warn("sprint075: first-boot catalog sync count failed", "error", err)
		return
	}
	if n > 0 {
		logger.Debug("sprint075: catalog already populated, skipping first-boot sync", "chart_count", n)
		return
	}
	task, err := tasks.NewCatalogSyncTask(tasks.CatalogSyncPayload{})
	if err != nil {
		logger.Warn("sprint075: first-boot catalog sync build failed", "error", err)
		return
	}
	if _, err := queue.Enqueue(task); err != nil {
		logger.Warn("sprint075: first-boot catalog sync enqueue failed", "error", err)
		return
	}
	logger.Info("sprint075: first-boot catalog sync enqueued")
}

// bootstrapLocalCluster ensures the local cluster row exists. It builds a
// transient in-cluster k8s client purely to enrich the row with version /
// node-count metadata; if InClusterConfig is unavailable, the row is still
// created with empty discovery fields.
func bootstrapLocalCluster(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries) (*sqlc.Cluster, error) {
	restCfg, restErr := rest.InClusterConfig()
	var clientset *kubernetes.Clientset
	if restErr != nil {
		logger.Warn("local cluster discovery skipped: not running in-cluster", "error", restErr)
		restCfg = nil
	} else if cs, err := kubernetes.NewForConfig(restCfg); err != nil {
		logger.Warn("local cluster discovery skipped: clientset error", "error", err)
	} else {
		clientset = cs
	}
	return EnsureLocalCluster(ctx, queries, clientset, restCfg)
}

// Start begins listening on the given address. It blocks until the server stops.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if s.tunnelWorker != nil {
		go func() {
			if err := s.tunnelWorker.Start(); err != nil {
				s.logger.Error("tunnel-queue asynq server exited", "error", err)
			}
		}()
	}
	s.logger.Info("server listening", "addr", addr)
	return s.httpServer.Serve(ln)
}

// StartInternalArgoCDProxy serves the network-isolated ArgoCD->cluster proxy on
// its own listener. addr must be a non-public port (the deployment maps the
// public ingress only to the main :8000 listener, and a NetworkPolicy restricts
// this port to the argocd namespace). Blocks until the listener errors.
func (s *Server) StartInternalArgoCDProxy(addr string) error {
	if s.internalArgoCDHandler == nil || strings.TrimSpace(addr) == "" {
		return nil
	}
	srv := &http.Server{
		Handler:           s.internalArgoCDHandler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.logger.Info("internal argocd proxy listening", "addr", addr)
	return srv.Serve(ln)
}

// Shutdown gracefully shuts down the server with a deadline.
//
// Order matters:
//  1. Drain the tunnel hub. Agents see a clean WS close and reconnect
//     to a sibling replica in ~1s instead of waiting ~20s for the next
//     ping to fail. The preStop hook in the
//     chart's server-deployment runs `sleep 10` BEFORE SIGTERM lands
//     so the Service load balancer has already removed this pod from
//     endpoints — the drained agents reconnect through the LB to a
//     healthy sibling.
//  2. httpServer.Shutdown — blocks until in-flight HTTP handlers exit.
//     New connections are rejected immediately; long-running requests
//     get the deadline.
//  3. cancel the reconcile context (in-process workers, publishers).
//  4. close DB pool + asynq client.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.hub != nil {
		drained := s.hub.Drain()
		s.logger.Info("tunnel hub drained", "agents_disconnected", drained)
	}
	if s.tunnelWorker != nil {
		s.tunnelWorker.Shutdown()
	}
	err := s.httpServer.Shutdown(ctx)
	if s.cancel != nil {
		s.cancel()
	}
	if s.db != nil {
		s.db.Close()
	}
	if s.queue != nil {
		_ = s.queue.Close()
	}
	return err
}

// ServeHTTP implements http.Handler, useful for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// localClusterArgoCDTokenSource implements handler.SessionTokenSource by
// looking up the management cluster's ArgoCD instance row and decrypting its
// stored auth_token. The result is the value of the upstream `argocd.token`
// cookie that ArgoCD's UI honours; the proxy stamps it onto outgoing requests
// so users land in an authenticated session without a second login prompt.
//
// Returns ("", nil) when there is no local cluster row yet, no ArgoCD
// instance registered for it, or no encrypted token stored. Errors during
// decryption are propagated so the proxy can log them and fall through.
type localClusterArgoCDTokenSource struct {
	queries   *sqlc.Queries
	encryptor *auth.Encryptor
	log       *slog.Logger
}

func (s *localClusterArgoCDTokenSource) UpstreamSessionToken(ctx context.Context) (string, error) {
	if s == nil || s.queries == nil || s.encryptor == nil {
		return "", nil
	}
	// Find the local cluster row by listing and filtering — no
	// dedicated GetLocalCluster query exists today, but is_local has at
	// most one row by partial-unique index, so this is O(N) only over
	// the (small) cluster set.
	clusters, err := s.queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 200, Offset: 0})
	if err != nil {
		return "", err
	}
	var localID uuid.UUID
	for _, c := range clusters {
		if c.IsLocal {
			localID = c.ID
			break
		}
	}
	if localID == uuid.Nil {
		return "", nil
	}
	instances, err := s.queries.ListInstancesByCluster(ctx, sqlc.ListInstancesByClusterParams{
		ClusterID: localID,
		Limit:     1,
		Offset:    0,
	})
	if err != nil {
		return "", err
	}
	if len(instances) == 0 || instances[0].AuthTokenEncrypted == "" {
		return "", nil
	}
	plain, err := s.encryptor.Decrypt(instances[0].AuthTokenEncrypted)
	if err != nil {
		return "", err
	}
	return plain, nil
}

// kubectlShellHandler builds the in-browser kubectl shell handler when
// the feature is enabled in chart values. Returns nil when disabled so
// the router skips the route block and the worker reaper exits early.
//
// The handler bundles its own kubectl.Deps so the reaper task can be
// configured from the same struct (server starts both; the worker
// process gets the same deps via the shared queue wiring).
func kubectlShellHandler(
	queries *sqlc.Queries,
	rbacQuerier appmiddleware.RBACQuerier,
	rbacEngine *rbac.Engine,
	requester handler.K8sRequester,
	cfg *config.Config,
	logger *slog.Logger,
) *handler.KubectlShellHandler {
	if cfg == nil || !cfg.KubectlShellEnabled {
		return nil
	}
	idle := time.Duration(cfg.KubectlShellIdleTimeoutMinutes) * time.Minute
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	hard := time.Duration(cfg.KubectlShellSessionHardCapHours) * time.Hour
	if hard <= 0 {
		hard = 4 * time.Hour
	}
	deps := kubectl.Deps{
		Queries:     queries,
		Requester:   handler.KubectlK8sRequesterFromHandlerRequester(requester),
		Image:       cfg.KubectlShellImage,
		IdleTimeout: idle,
		HardCap:     hard,
		Log:         logger,
	}
	// Wire the reaper task once at the same time the handler is built;
	// the asynq scheduler entry is registered in worker/runtime.go.
	tasks.ConfigureKubectlSessionReap(tasks.KubectlSessionReapDeps{Deps: deps})
	return handler.NewKubectlShellHandler(queries, rbacQuerier, rbacEngine, deps)
}

type registrationAdvancer interface {
	OnAgentConnected(ctx context.Context, clusterID uuid.UUID, agentVersion string) error
}

type argoCDAutoRegisterAdvancer struct {
	base       registrationAdvancer
	queue      *asynq.Client
	taskOutbox tasks.TaskOutboxWriter
	log        *slog.Logger
}

func (a *argoCDAutoRegisterAdvancer) OnAgentConnected(ctx context.Context, clusterID uuid.UUID, agentVersion string) error {
	if a == nil {
		return nil
	}
	if a.base != nil {
		if err := a.base.OnAgentConnected(ctx, clusterID, agentVersion); err != nil {
			return err
		}
	}
	if a.queue == nil && a.taskOutbox == nil {
		return nil
	}
	task, err := tasks.NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		return err
	}
	if a.taskOutbox != nil {
		if _, err := tasks.EnqueueTaskOutbox(ctx, a.taskOutbox, task, tasks.TaskOutboxOptions{
			QueueName:           "default",
			MaxRetry:            5,
			Unique:              10 * time.Minute,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return nil
		} else if a.log != nil {
			a.log.Warn("failed to write argocd auto-registration task to outbox, falling back to direct enqueue",
				"cluster_id", clusterID.String(),
				"error", err,
			)
		}
	}
	if a.queue == nil {
		return nil
	}
	if _, err := a.queue.EnqueueContext(ctx, task); err != nil && a.log != nil {
		a.log.Warn("failed to enqueue argocd auto-registration after agent connect",
			"cluster_id", clusterID.String(),
			"error", err,
		)
	}
	return nil
}

// detectReleaseNamespace returns the namespace this server pod is running
// in. Tries the POD_NAMESPACE env var first (set by the chart via the
// Downward API when configured) then falls back to the standard
// serviceaccount mount. Returns "astronomer" if both fail, since that's
// the chart's default namespace.
func detectReleaseNamespace() string {
	if v := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); v != "" {
		return v
	}
	if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return "astronomer"
}

// maintenanceStartupWarn (migration 057) logs a warn-level line on
// boot for every enabled "permitted" window with an empty
// operation_types list. That combination blocks ALL destructive ops
// outside the window, which is the most dangerous configuration the
// gate supports — operators MUST opt into it intentionally. The check
// runs once at startup; the handler doesn't reject the configuration
// at PUT time because it's a valid choice.
func maintenanceStartupWarn(ctx context.Context, q maintenanceStartupQuerier, logger *slog.Logger) {
	if q == nil {
		return
	}
	rows, err := q.ListEnabledMaintenanceWindows(ctx)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Mode != maintenance.ModePermitted {
			continue
		}
		// Empty operation_types JSONB encodes as the literal "[]";
		// a missing or malformed value (very unusual; the schema
		// defaults it) is treated the same way.
		if len(row.OperationTypes) == 0 || string(row.OperationTypes) == "[]" {
			logger.Warn("maintenance window in permitted mode with empty operation_types blocks ALL destructive ops outside its window",
				"window_id", row.ID.String(),
				"name", row.Name,
				"cron_open", row.CronOpen,
				"timezone", row.Timezone,
			)
		}
	}
}

// maintenanceStartupQuerier is a narrow interface so the helper can be
// tested independently of the full *sqlc.Queries surface.
type maintenanceStartupQuerier interface {
	ListEnabledMaintenanceWindows(ctx context.Context) ([]sqlc.MaintenanceWindow, error)
}
