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
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// busPublisherAdapter bridges the *events.Bus into the tunnel.LifecyclePublisher
// interface (the tunnel package can't import events directly without a cycle).
type busPublisherAdapter struct{ bus *events.Bus }

func (a busPublisherAdapter) Publish(eventType string, data any) {
	a.bus.Publish(events.Type(eventType), data)
}

type securityCacheTarget struct {
	jwt  *auth.JWTManager
	rbac *appmiddleware.RBACCache
}

type charlieLiveBindings struct {
	queries  *sqlc.Queries
	bindings rbac.BindingQuerier
}

type charlieLiveFeatures struct{ queries *sqlc.Queries }

func (r charlieLiveFeatures) BoolValue(ctx context.Context, key string, _ bool) bool {
	if r.queries == nil || key != "feature.charlie" {
		return false
	}
	setting, err := r.queries.GetPlatformSetting(ctx, key)
	if err != nil {
		return false
	}
	var enabled bool
	if json.Unmarshal(setting.Value, &enabled) != nil {
		return false
	}
	return enabled
}

func (r charlieLiveBindings) CurrentBindings(ctx context.Context, principal uuid.UUID) ([]rbac.RoleBinding, bool, error) {
	if r.queries == nil || r.bindings == nil || principal == uuid.Nil {
		return nil, false, fmt.Errorf("Charlie live RBAC is unavailable")
	}
	user, err := r.queries.GetUserByID(ctx, principal)
	if err != nil || !user.IsActive {
		return nil, false, err
	}
	bindings, err := r.bindings.GetUserBindings(ctx, principal.String())
	return bindings, err == nil, err
}

func (r charlieLiveBindings) CanUseCharlie(ctx context.Context, principal uuid.UUID) (bool, error) {
	bindings, active, err := r.CurrentBindings(ctx, principal)
	if err != nil || !active {
		return false, err
	}
	engine := rbac.NewEngine()
	return engine.CheckPermission(bindings, rbac.ResourceCharlie, rbac.VerbRead, uuid.Nil, uuid.Nil) ||
		engine.CheckPermission(bindings, rbac.ResourceCharlie, rbac.VerbCreate, uuid.Nil, uuid.Nil), nil
}

func (r charlieLiveBindings) CanReadIncidentResources(ctx context.Context, principal uuid.UUID, resources []sqlc.CharlieSessionResource) (bool, error) {
	bindings, active, err := r.CurrentBindings(ctx, principal)
	if err != nil || !active {
		return false, err
	}
	engine := rbac.NewEngine()
	for _, item := range resources {
		if item.RequiredVerb != "read" {
			return false, nil
		}
		resource, verb, clusterID, ok := charlieResourcePermission(item)
		if !ok || !engine.CheckPermission(bindings, resource, verb, clusterID, uuid.Nil) {
			return false, nil
		}
	}
	return true, nil
}

func charlieResourcePermission(item sqlc.CharlieSessionResource) (rbac.Resource, rbac.Verb, uuid.UUID, bool) {
	switch item.ResourceType {
	case "installation":
		return rbac.ResourceSettings, rbac.VerbRead, uuid.Nil, true
	case "management_component":
		return rbac.ResourceMonitoring, rbac.VerbRead, uuid.Nil, true
	case "alert":
		return rbac.ResourceAlerts, rbac.VerbRead, uuid.Nil, true
	case "backup":
		return rbac.ResourceBackups, rbac.VerbRead, uuid.Nil, true
	case "self_management_application":
		return rbac.ResourceDeliveryPlatform, rbac.VerbRead, uuid.Nil, true
	case "cluster_agents", "tunnel":
		return rbac.ResourceClusters, rbac.VerbList, uuid.Nil, true
	case "agent_connection_record":
		clusterID, err := uuid.Parse(item.ResourceID)
		return rbac.ResourceClusters, rbac.VerbRead, clusterID, err == nil
	default:
		return "", "", uuid.Nil, false
	}
}

func (t securityCacheTarget) InvalidateJWTJTILocal(jti string) {
	if t.jwt != nil {
		t.jwt.InvalidateJWTJTILocal(jti)
	}
}
func (t securityCacheTarget) InvalidateJWTUserLocal(userID string) {
	if t.jwt != nil {
		t.jwt.InvalidateJWTUserLocal(userID)
	}
}
func (t securityCacheTarget) InvalidateJWTAllLocal() {
	if t.jwt != nil {
		t.jwt.InvalidateJWTAllLocal()
	}
}
func (t securityCacheTarget) InvalidateRBACUserLocal(userID string) {
	if t.rbac != nil {
		t.rbac.InvalidateRBACUserLocal(userID)
	}
}
func (t securityCacheTarget) InvalidateRBACAllLocal() {
	if t.rbac != nil {
		t.rbac.InvalidateRBACAllLocal()
	}
}

// runServerReconcilerLeader holds a Postgres advisory lock and starts server
// reconcilers only while this pod is leader (CORR-R06). On leadership loss it
// cancels the child context (stopping ticker loops that respect ctx) and
// retries acquisition.
func runServerReconcilerLeader(ctx context.Context, elector *leader.Elector, log *slog.Logger, run func(context.Context) error) error {
	if log == nil {
		log = slog.Default()
	}
	const job = "server.reconcilers"
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		rel, held, err := elector.TryLeader(ctx, job)
		if err != nil {
			log.Warn("server reconciler leader election failed", "error", err)
		} else if held {
			log.Info("server reconciler leadership acquired", "job", job)
			runErr := run(ctx)
			rel()
			if ctx.Err() != nil {
				return nil
			}
			if runErr == nil {
				runErr = errors.New("reconciler group exited without cancellation")
			}
			return runErr
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
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

// reportInsecureDevKeys logs + exports the dev-sentinel state on every boot,
// in every environment. validateProductionSecurityConfig above only speaks up
// under config.env=production, which is where the published chart keys used to
// hide: a "development" install signs the same JWTs and wraps the same stored
// cluster credentials (dev-keys-default-and-silent).
func reportInsecureDevKeys(cfg *config.Config, logger *slog.Logger) {
	observability.ReportInsecureDevKeys(logger, config.DevSentinelsInUse(cfg))
}

func validateProductionSecurityWiring(cfg *config.Config, deps RouterDependencies) error {
	_ = cfg // Security wiring is mandatory in every environment.
	var errs []string
	if deps.CoreAuth.JWT == nil {
		errs = append(errs, "JWT manager is not wired")
	} else if !deps.CoreAuth.JWT.HasRevocationChecker() {
		errs = append(errs, "JWT revocation checker is not wired")
	}
	if dependencyMissing(deps.CoreAuth.AuthQueries) {
		errs = append(errs, "auth queries are not wired")
	}
	if deps.CoreAuth.RBACEngine == nil {
		errs = append(errs, "RBAC engine is not wired")
	}
	if dependencyMissing(deps.CoreAuth.RBACQueries) {
		errs = append(errs, "RBAC queries are not wired")
	}
	if deps.CoreAuth.Encryptor == nil {
		errs = append(errs, "encryptor is not wired")
	}
	if deps.CoreAuth.SettingsCache == nil {
		errs = append(errs, "platform settings cache is not wired")
	}
	if deps.CoreAuth.Queries == nil {
		errs = append(errs, "shared queries are not wired")
	}
	if dependencyMissing(deps.CoreAuth.AuditWriter) {
		errs = append(errs, "security audit writer is not wired")
	}
	if deps.StreamingInternal.Hub != nil && !deps.StreamingInternal.Hub.AgentTokenValidatorWired() {
		errs = append(errs, "hub agent-token validator is not wired")
	}
	if deps.StreamingInternal.Exec != nil && !deps.StreamingInternal.Exec.SecurityWiringValid() {
		errs = append(errs, "exec stream security is not fully wired")
	}
	if deps.StreamingInternal.Logs != nil && !deps.StreamingInternal.Logs.SecurityWiringValid() {
		errs = append(errs, "logs stream security is not fully wired")
	}
	if (deps.StreamingInternal.Exec != nil || deps.StreamingInternal.Logs != nil || deps.StreamingInternal.EventStream != nil) && deps.StreamingInternal.StreamTicketStore == nil {
		errs = append(errs, "stream ticket store is not wired")
	}
	// project_namespaces feeds the synthetic namespace-scoped bindings, so a
	// project handler with no RBAC cache invalidator turns every
	// remove-namespace into a revoke that does not take effect until the cache
	// entry expires. Fail the boot instead.
	if deps.ClusterResources.Projects != nil && !deps.ClusterResources.Projects.RBACInvalidatorWired() {
		errs = append(errs, "project handler RBAC cache invalidator is not wired")
	}
	if deps.CoreAuth.SCIM != nil && !deps.CoreAuth.SCIM.TransactionalAuditWired() {
		errs = append(errs, "SCIM transactional audit is not wired")
	}
	if deps.CoreAuth.SSO != nil && !deps.CoreAuth.SSO.TransactionalAuditWired() {
		errs = append(errs, "SSO callback transactional audit, encryption, or RBAC invalidation is not wired")
	}
	if deps.CoreAuth.TOTP != nil && !deps.CoreAuth.TOTP.TransactionalAuditWired() {
		errs = append(errs, "TOTP transactional audit is not wired")
	}
	if deps.AdminPlatform.SMTP != nil && !deps.AdminPlatform.SMTP.TransactionalAuditWired() {
		errs = append(errs, "SMTP transactional audit is not wired")
	}
	if deps.AdminPlatform.Security != nil && !deps.AdminPlatform.Security.TransactionalAuditWired() {
		errs = append(errs, "security transactional audit is not wired")
	}
	if deps.AdminPlatform.Extensions != nil && !deps.AdminPlatform.Extensions.TransactionalAuditWired() {
		errs = append(errs, "extension transactional audit is not wired")
	}
	if deps.AdminPlatform.PlatformDefaultTemplate != nil && !deps.AdminPlatform.PlatformDefaultTemplate.TransactionalAuditWired() {
		errs = append(errs, "platform default template transactional audit is not wired")
	}
	if deps.ClusterResources.ControlPlaneSnapshots != nil && !deps.ClusterResources.ControlPlaneSnapshots.TransactionalAuditWired() {
		errs = append(errs, "control-plane snapshot transactional audit is not wired")
	}
	if deps.ClusterResources.Resources != nil && !deps.ClusterResources.Resources.TransactionalSettingsSSOAuditWired() {
		errs = append(errs, "settings and SSO transactional audit is not wired")
	}
	if deps.ClusterResources.Resources != nil && !deps.ClusterResources.Resources.TransactionalUserAuditWired() {
		errs = append(errs, "user administration transactional audit is not wired")
	}
	if deps.ClusterResources.ProjectCatalogs != nil && !deps.ClusterResources.ProjectCatalogs.TransactionalAuditWired() {
		errs = append(errs, "project catalog transactional audit is not wired")
	}
	if deps.Delivery.ConfigurationTemplates != nil && !deps.Delivery.ConfigurationTemplates.TransactionalAuditWired() {
		errs = append(errs, "delivery configuration template transactional audit is not wired")
	}
	if deps.Delivery.OverrideSets != nil && !deps.Delivery.OverrideSets.TransactionalAuditWired() {
		errs = append(errs, "delivery override set transactional audit is not wired")
	}
	if deps.AdminPlatform.SupportBundle != nil && !deps.AdminPlatform.SupportBundle.TransactionalMutationWired() {
		errs = append(errs, "support bundle durable operation store is not wired")
	}
	if deps.AdminPlatform.Audit != nil && !deps.AdminPlatform.Audit.DurableExportWired() {
		errs = append(errs, "audit durable export operation store is not wired")
	}
	if len(errs) > 0 {
		return fmt.Errorf("security wiring invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func dependencyMissing(dependency any) bool {
	if dependency == nil {
		return true
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
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
	queue      *asynq.Client
	// hub is the tunnel hub; nil in lightweight test servers. Held here
	// so Shutdown can drain WS connections before tearing down HTTP.
	hub *tunnel.Hub
	// tunnelWorker is an in-process asynq.Server that drains the
	// "tunnel" queue. Tasks on that queue (cluster_template:apply +
	// drift_check) call into the ToolHandler.EnsureInstalled tunnel
	// path, which only works on the pod that owns the WS terminations.
	// Nil in lightweight test servers built via New().
	tunnelWorker tunnelWorkerLifecycle
	// Encryptor is the Fernet encryptor wired into handlers that surface
	// encrypted columns (delivery credentials, SSO client secrets, etc.).
	Encryptor *auth.Encryptor
	// SSO drives the OAuth login/callback flow. May be nil if no providers
	// are configured at boot.
	SSO *auth.SSOManager
	// charlieRuntime owns independently gated configuration-discovery and work
	// generations. The bridge remains transport-dormant outside an authorized
	// product operation.
	charlieRuntime *charlieLifecycleGroup
	charlieBridge  *charlie.ManagedBridge
	// taskLeader owns the small Postgres pool reserved for advisory-lock
	// sessions. It is closed after reconcilers stop and before the main pool.
	taskLeader *leader.Elector
	// runtime owns every process-lifetime loop and supplies the critical-loop
	// readiness state consumed by /readyz.
	runtime *runtimeSupervisor
	// shutdownHooks drain durable buffers and telemetry before the resource
	// pools they depend on are closed. Hooks are registered during startup and
	// run in registration order after ingress has stopped.
	shutdownHooks   []shutdownHook
	resourceClosers []shutdownHook
	stopping        atomic.Bool
	shutdownMu      sync.Mutex
	shutdownDone    bool
	shutdownErr     error
}

type tunnelWorkerLifecycle interface {
	Run(context.Context) error
	Shutdown()
}

type shutdownHook struct {
	name string
	fn   func(context.Context) error
}

// AddShutdownHook registers a bounded drain that must complete before Redis
// and Postgres are closed. It is intended for startup-time wiring only.
func (s *Server) AddShutdownHook(name string, fn func(context.Context) error) {
	if s == nil || fn == nil {
		return
	}
	s.shutdownHooks = append(s.shutdownHooks, shutdownHook{name: name, fn: fn})
}

func (s *Server) addResourceCloser(name string, fn func(context.Context) error) {
	if s == nil || fn == nil {
		return
	}
	s.resourceClosers = append(s.resourceClosers, shutdownHook{name: name, fn: fn})
}

// AddRuntimeLoop registers an additional process-lifetime component after
// NewApp composition (for example the dedicated metrics listener).
func (s *Server) AddRuntimeLoop(name string, critical bool, run func(context.Context) error) error {
	if s == nil || s.runtime == nil {
		return errors.New("runtime supervisor is unavailable")
	}
	return s.runtime.Go(name, critical, run)
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
	errCh := make(chan error, 1)
	if s.tunnelWorker != nil {
		if s.runtime == nil {
			_ = ln.Close()
			return errors.New("tunnel-queue worker requires runtime supervisor")
		}
		if err := s.runtime.Go("tunnel-queue-worker", true, func(ctx context.Context) error {
			return s.tunnelWorker.Run(ctx)
		}); err != nil {
			_ = ln.Close()
			return err
		}
	}
	s.logger.Info("server listening", "addr", addr)
	go func() { errCh <- s.httpServer.Serve(ln) }()
	var runtimeFailures <-chan error
	if s.runtime != nil {
		runtimeFailures = s.runtime.Failures()
	}
	select {
	case err = <-errCh:
	case err = <-runtimeFailures:
	}
	if errors.Is(err, http.ErrServerClosed) || (err == nil && s.stopping.Load()) {
		return nil
	}
	return err
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
//  3. Stop task intake, cancel every owned loop, and join it before draining
//     hooks or closing any dependency used by those loops.
//  4. Drain audit/telemetry, then close leader sessions, Redis, and Postgres.
//     Project mutations are delivered through
//     the durable task outbox; there are no request-spawned project goroutines.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.shutdownDone {
		return s.shutdownErr
	}
	s.stopping.Store(true)
	var shutdownErrs []error
	runtimeJoined := true
	if s.hub != nil {
		drained := s.hub.Drain()
		s.logger.Info("tunnel hub drained", "agents_disconnected", drained)
	}
	if err := s.httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("http server: %w", err))
	}
	if s.runtime != nil {
		s.runtime.MarkStopping()
	}
	if s.tunnelWorker != nil {
		s.tunnelWorker.Shutdown()
	}
	if s.charlieRuntime != nil {
		if err := s.charlieRuntime.Shutdown(ctx); err != nil {
			charlie.LogOperationalFailure(ctx, s.logger, "runtime.shutdown_failed", "")
			shutdownErrs = append(shutdownErrs, fmt.Errorf("Charlie runtime: %w", err))
		}
	}
	if s.charlieBridge != nil {
		s.charlieBridge.Close()
	}
	if s.runtime != nil {
		s.runtime.BeginStop()
		if err := s.runtime.Wait(ctx); err != nil {
			shutdownErrs = append(shutdownErrs, err)
			runtimeJoined = false
		}
	}
	for _, hook := range s.shutdownHooks {
		if err := hook.fn(ctx); err != nil {
			s.logger.Warn("shutdown drain failed", "component", hook.name, "error", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("%s: %w", hook.name, err))
		}
	}
	if !runtimeJoined {
		s.logger.Error("runtime loops did not join; leaving dependency pools open for process exit")
		s.shutdownErr = errors.Join(shutdownErrs...)
		s.shutdownDone = true
		return s.shutdownErr
	}
	if s.taskLeader != nil {
		s.taskLeader.Close()
	}
	for _, closer := range s.resourceClosers {
		if err := closer.fn(ctx); err != nil {
			s.logger.Warn("runtime resource close failed", "component", closer.name, "error", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("%s: %w", closer.name, err))
		}
	}
	if s.queue != nil {
		_ = s.queue.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
	s.shutdownErr = errors.Join(shutdownErrs...)
	s.shutdownDone = true
	return s.shutdownErr
}

// ServeHTTP implements http.Handler, useful for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// kubectlShellComponents builds the in-browser kubectl shell handler and its
// immutable reaper runtime. A disabled feature returns a nil handler and an
// empty runtime; the optional task remains bound and records skipped ticks.
//
// The handler bundles its own kubectl.Deps so the reaper task can be
// configured from the same struct (server starts both; the worker
// process gets the same deps via the shared queue wiring).
func kubectlShellComponents(
	queries *sqlc.Queries,
	rbacQuerier rbac.BindingQuerier,
	rbacEngine *rbac.Engine,
	requester handler.K8sRequester,
	cfg *config.Config,
	logger *slog.Logger,
	taskLeader tasks.LeaderElector,
) (*handler.KubectlShellHandler, tasks.KubectlSessionReapRuntime) {
	if cfg == nil || !cfg.KubectlShellEnabled {
		return nil, tasks.KubectlSessionReapRuntime{}
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
	runtime := tasks.KubectlSessionReapRuntime{Deps: deps, Leader: taskLeader, Log: logger}
	return handler.NewKubectlShellHandler(queries, rbacQuerier, rbacEngine, deps), runtime
}

// detectReleaseNamespace returns the namespace this server pod is running
// in. Tries the POD_NAMESPACE env var first (set by the chart via the
// Downward API when configured) then falls back to the standard
// serviceaccount mount. Returns "astronomer" if both fail, since that's
// the chart's default namespace.
func detectReleaseNamespace(configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
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
