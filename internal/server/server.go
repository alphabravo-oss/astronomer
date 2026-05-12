package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	livemetrics "github.com/alphabravocompany/astronomer-go/internal/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
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

// dsnEnforcesTLS reports whether a Postgres DSN includes an sslmode setting
// that requires TLS. Acceptable values: `require`, `verify-ca`, `verify-full`.
// Anything else (sslmode=disable, sslmode=allow, sslmode=prefer, or no
// sslmode at all — Postgres treats omission as `prefer` which silently
// downgrades to plaintext if the server allows it) returns false.
func dsnEnforcesTLS(dsn string) bool {
	d := strings.ToLower(dsn)
	return strings.Contains(d, "sslmode=require") ||
		strings.Contains(d, "sslmode=verify-ca") ||
		strings.Contains(d, "sslmode=verify-full")
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
	logger     *slog.Logger
	db         *db.DB
	cancel     context.CancelFunc
	queue      *asynq.Client
	// hub is the tunnel hub; nil in lightweight test servers. Held here
	// so Shutdown can drain WS connections before tearing down HTTP.
	hub *tunnel.Hub
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
	// Production posture guard: warn loudly when the DB DSN doesn't enforce
	// TLS but config.env=production. The chart-level preflight already
	// catches this for the helm-managed install path; this covers operators
	// who bypass the chart and run the binary directly against a hand-rolled
	// DATABASE_URL. Not fail-closed at the binary level — same reason the
	// encryptor isn't — to keep dev/local stacks working unchanged.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ASTRONOMER_ENV")), "production") &&
		!dsnEnforcesTLS(cfg.DatabaseURL) {
		logger.Warn(
			"DATABASE_URL does not enforce TLS but ASTRONOMER_ENV=production "+
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

	bus := events.NewBus()
	hub := tunnel.NewHubWithValidator(logger, queries)
	hub.SetPublisher(busPublisherAdapter{bus: bus})
	remoteServer := tunnel2.NewRemoteServer(logger, queries)
	requester := handler.NewTunnelK8sRequester(hub)
	helmRequester := handler.NewTunnelHelmRequester(hub)
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
	rbacEngine := rbac.NewEngine()
	rbacQuerier := appmiddleware.NewSQLCRBACQuerier(queries)
	monitoringHandler.SetAuthorization(rbacEngine, rbacQuerier)
	argocdHandler.SetAuthorization(rbacEngine, rbacQuerier)
	toolHandler.SetAuthorization(rbacEngine, rbacQuerier)
	catalogHandler.SetAuthorization(rbacEngine, rbacQuerier)
	loggingHandler.SetAuthorization(rbacEngine, rbacQuerier)
	workloadHandler.SetAuthorization(rbacEngine, rbacQuerier)
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
	projectHandler.SetTaskQueue(queue)
	projectHandler.SetK8sRequester(requester)
	projectHandler.SetLogger(logger)
	controlPlaneHandler := handler.NewControlPlaneHandler(queries, monitoringHandler, argocdHandler, toolHandler, catalogHandler, backupHandler, loggingHandler, securityHandler, queue)

	authHandler := handler.NewAuthHandlerWithTokens(queries, queries, jwtManager)
	authHandler.SetPasswordRehasher(queries)
	authHandler.SetRoleBindings(queries)
	authHandler.SetAuditWriter(queries)
	authHandler.SetLogger(logger)

	var ssoHandler *handler.SSOHandler
	if ssoManager != nil {
		ssoHandler = handler.NewSSOHandler(ssoManager, queries, jwtManager, "/")
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
	clusterHandler.SetAgentImage(cfg.AgentImageRepository, cfg.AgentImageTag)
	// Fan cluster.* lifecycle events out to SSE subscribers on Create / Update
	// / Delete. The bus implements the EventPublisher interface naturally.
	clusterHandler.SetEventPublisher(busPublisherAdapter{bus: bus})
	// Wire the asynq client into the DELETE handler so the cluster
	// decommission reconciler fires immediately on remove-cluster click.
	// The periodic sweep is the safety net when redis is briefly down.
	clusterHandler.SetDecommissionQueue(queue)
	// Wire metrics: tunnel requester for remote clusters, in-cluster clients
	// for the local cluster. Both are nil-safe; missing deps fall back to zero.
	clusterHandler.SetMetricsRequester(requester)
	// localK8s and localNamespace are reused below to construct the support
	// bundle handler; SetMetricsLocalClient / SetKubernetesClient consume
	// localK8s too.
	var localK8s kubernetes.Interface
	if restCfg, err := rest.InClusterConfig(); err == nil {
		if cs, err := kubernetes.NewForConfig(restCfg); err == nil {
			localK8s = cs
			argocdHandler.SetKubernetesClient(cs)
			if mc, err := metricsv.NewForConfig(restCfg); err == nil {
				clusterHandler.SetMetricsLocalClient(cs, mc)
			} else {
				clusterHandler.SetMetricsLocalClient(cs, nil)
			}
		}
	}
	localNamespace := detectReleaseNamespace()

	// Share the same metrics provider with the workload handler so per-node
	// CPU/memory usage on the node-detail page comes from the same fetch (and
	// the same cache) as the dashboard cluster card.
	workloadHandler.SetMetricsProvider(clusterHandler.MetricsProvider())

	resourceHandler := handler.NewResourceHandlerWithQueries(queries, requester)
	resourceHandler.SetEncryptor(encryptor)
	resourceHandler.SetSSOManager(ssoManager)
	platformCharts, chartRepoErr := handler.NewPlatformChartRepoHandler()
	if chartRepoErr != nil {
		return nil, chartRepoErr
	}

	deps := RouterDependencies{
		JWT:          jwtManager,
		Encryptor:    encryptor,
		AuthQueries:  queries,
		Auth:         authHandler,
		SSO:          ssoHandler,
		Clusters:     clusterHandler,
		Projects:     projectHandler,
		Tools:        toolHandler,
		Audit:        handler.NewAuditHandler(queries),
		Alerting:     handler.NewAlertingHandlerWithDeps(queries, requester),
		ArgoCD:       argocdHandler,
		Backups:      backupHandler,
		Catalog:      catalogHandler,
		Logging:      loggingHandler,
		Monitoring:   monitoringHandler,
		ControlPlane: controlPlaneHandler,
		Resources:    resourceHandler,
		PlatformCharts: platformCharts,
		ResourcesSearch: func() *handler.ResourcesSearchHandler {
			h := handler.NewResourcesSearchHandler(queries, requester)
			h.SetAuthorization(rbacEngine, rbacQuerier)
			return h
		}(),
		Readyz:        newReadinessHandler(database, queue, hub),
		DexConfig:     dexHandler,
		RBAC:          handler.NewRBACHandler(queries),
		RBACQueries:   rbacQuerier,
		RBACEngine:    rbacEngine,
		Security:      securityHandler,
		ServiceProxy:  handler.NewServiceProxyHandler(requester),
		Workloads:     workloadHandler,
		Hub:           hub,
		Proxy:         tunnel.NewProxyHandler(hub, logger),
		Exec:          tunnel.NewExecConsumer(hub, logger),
		Logs:          tunnel.NewLogsConsumer(hub, logger),
		RemoteServer:  remoteServer,
		RemoteQueries: queries,
		EventStream: handler.NewEventStreamHandler(bus),
		// Top-of-dashboard health rollup.
		PlatformHealth: func() *handler.PlatformHealthHandler {
			h := handler.NewPlatformHealthHandler(database.Pool())
			h.SetAsynqInspector(asynq.NewInspector(redisOpt))
			return h
		}(),
		// Admin queue inspector.
		AdminQueues: handler.NewAdminQueuesHandler(asynq.NewInspector(redisOpt), queries),
		SupportBundle: func() *handler.SupportBundleHandler {
			h := handler.NewSupportBundleHandler(queries, localK8s, localNamespace)
			// Enable the asynq-queues + schema-
			// migrations sections by wiring the inspector and the DB pool.
			h.SetAsynqInspector(asynq.NewInspector(redisOpt))
			h.SetDBPool(database.Pool())
			return h
		}(),
	}
	// EventSource cannot send Authorization headers, so the stream handler
	// also accepts ?token=<jwt|api_token>. Wire it through the same JWT
	// manager + token querier the rest of the API uses.
	deps.EventStream.SetAuth(jwtManager, queries)
	// Browser WebSocket clients can't set Authorization either; wire the
	// same query-param auth fallback into the pod exec consumer.
	deps.Exec.SetAuth(jwtManager, queries)

	// Browser `new WebSocket(...)` cannot set Authorization either — the pod
	// logs WS handler accepts the same `?token=` fallback. Without this
	// hook the route would accept unauthenticated connections, which would
	// leak pod log contents to anyone who can reach the API.
	if deps.Logs != nil {
		deps.Logs.SetAuth(jwtManager, queries)
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
		deps.ArgoCDUIProxy = argoUIProxy
	}

	router := NewRouter(cfg, deps)

	s := &Server{
		handler:   router,
		logger:    logger,
		db:        database,
		queue:     queue,
		hub:       hub,
		Encryptor: encryptor,
		SSO:       ssoManager,
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
	})
	// Cluster decommission reconciler: needs DB + the tunnel hub for both
	// MsgDecommission RPC and forced Disconnect after token revoke. When the
	// hub is unavailable (worker-only process), pass nil — the reconciler
	// falls back to "agent unreachable, skip with warning" semantics.
	tasks.ConfigureClusterDecommission(tasks.ClusterDecommissionDeps{
		Queries: queries,
		Tunnel:  hub,
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

	return s, nil
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
	s.logger.Info("server listening", "addr", addr)
	return s.httpServer.Serve(ln)
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
	err := s.httpServer.Shutdown(ctx)
	if s.cancel != nil {
		s.cancel()
	}
	if s.db != nil {
		s.db.Close()
	}
	if s.queue != nil {
		s.queue.Close()
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
