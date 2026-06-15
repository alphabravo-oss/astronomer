package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/hibiken/asynq"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	observability.WithEvent(log, "worker_starting").Info("starting astronomer worker binary",
		"version", version.Version,
		"commit", version.GitCommit,
		"built", version.BuildDate,
	)

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Distributed tracing — same InitTracing/Shutdown contract as
	// the server. The worker's asynq handlers extract traceparent from
	// incoming task payloads (planned follow-up in this same sprint),
	// so when both processes point at the same OTLP endpoint a single
	// trace can span HTTP → asynq → worker DB queries → tunnel calls.
	tracingCfg := observability.TracingFromEnv()
	tracingCfg.ServiceName = "astronomer-worker"
	tracingCfg.ServiceVersion = version.Version
	otelShutdown, err := observability.InitTracing(context.Background(), log, tracingCfg)
	if err != nil {
		log.Error("failed to init otel tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(ctx); err != nil {
			log.Warn("otel shutdown error", "error", err)
		}
	}()

	database, err := db.ConnectWithConfig(context.Background(), cfg.DatabaseURL, db.PoolConfig{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   time.Duration(cfg.DBMaxConnLifetimeMin) * time.Minute,
		MaxConnIdleTime:   time.Duration(cfg.DBMaxConnIdleMin) * time.Minute,
		HealthCheckPeriod: time.Duration(cfg.DBHealthCheckPeriodSec) * time.Second,
	})
	if err != nil {
		log.Error("failed to connect database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	if _, err := observability.EnsureInstanceID(context.Background(), sqlc.New(database.Pool())); err != nil {
		log.Error("failed to ensure observability instance id", "error", err)
		os.Exit(1)
	}
	log = observability.Logger(log)
	slog.SetDefault(log)

	// Async batched audit writer — shared design with cmd/server. The
	// worker only records audit rows from the cluster-decommission
	// reconciler today, but the cost reduction is the same: no DB
	// round-trip on the task path. Bounded by the same Shutdown
	// deadline as the worker process below.
	auditQueries := sqlc.New(database.Pool())
	auditWriter := audit.NewWriter(auditQueries, log)
	auditWriter.Start(context.Background())
	audit.SetWriter(auditWriter)
	defer audit.SetWriter(nil)

	tasks.ConfigureRuntime(tasks.RuntimeDependencies{
		Queries:                 sqlc.New(database.Pool()),
		Log:                     log,
		AgentImageRepo:          cfg.AgentImageRepository,
		AgentImageTag:           cfg.AgentImageTag,
		PlatformName:            "Astronomer",
		AuditLogRetentionMonths: cfg.AuditLogRetentionMonths,
		Leader:                  leader.New(database.Pool(), log),
	})
	var controlPlaneK8s kubernetes.Interface
	var controlPlaneDyn dynamic.Interface
	if restCfg, kErr := rest.InClusterConfig(); kErr == nil {
		if cs, kErr := kubernetes.NewForConfig(restCfg); kErr == nil {
			controlPlaneK8s = cs
		}
		if dyn, dErr := dynamic.NewForConfig(restCfg); dErr == nil {
			controlPlaneDyn = dyn
		}
	}
	tasks.ConfigureCRDOwnershipDrift(tasks.CRDOwnershipDriftDeps{
		Queries: sqlc.New(database.Pool()),
		Dynamic: controlPlaneDyn,
	})
	// Cluster decommission reconciler: the standalone worker process
	// doesn't have the tunnel hub (the hub lives in the server pod), so
	// Tunnel is nil. The reconciler treats a nil Tunnel as "agent
	// unreachable" — the notify_agent + revoke_agent_token phases skip
	// with a logged warning. Every other phase (archive_audit,
	// delete_dependents, tombstone_cluster) still runs from here, and
	// the periodic sweep picks up rows whose worker crashed mid-run.
	// When the server pod processes a decommission task it has the
	// hub, so the full flow including tunnel ops works there.
	tasks.ConfigureClusterDecommission(tasks.ClusterDecommissionDeps{
		Queries: sqlc.New(database.Pool()),
		K8s:     controlPlaneK8s,
		// TODO(rbac-invalidation): the standalone worker process has no
		// in-process RBAC cache to flush, but when it runs the decommission
		// phase from here, the server pod's cache still holds stale per-user
		// cluster bindings until the 15s TTL elapses. Cross-process
		// invalidation (pub/sub or a notify channel) would close this gap.
		RBACCache: nil,
	})
	// ArgoCD managed-cluster label refresh. The standalone worker pod runs
	// in the same control-plane cluster as the server (both are Deployments
	// in the astronomer namespace), so an in-cluster k8s client targets the
	// same argocd namespace where the Argo cluster Secrets live. When
	// in-cluster config isn't available (laptop dev) the task degrades to a
	// logged warning and the operator re-registers manually.
	{
		refreshK8s := controlPlaneK8s
		tasks.ConfigureArgoCDRefresh(tasks.ArgoCDRefreshDeps{
			Queries: sqlc.New(database.Pool()),
			K8s:     refreshK8s,
		})
		var enc *auth.Encryptor
		if cfg.EncryptionKey != "" {
			if e, encErr := auth.NewEncryptor(cfg.EncryptionKey); encErr == nil {
				enc = e
			} else {
				log.Warn("argocd auto-register encryptor init failed; task will skip remote proxy tokens", "error", encErr)
			}
		}
		tasks.ConfigureArgoCDAutoRegister(tasks.ArgoCDAutoRegisterDeps{
			Queries:             sqlc.New(database.Pool()),
			Encryptor:           enc,
			K8s:                 refreshK8s,
			ClusterProxyBaseURL: cfg.ArgoCDClusterProxyBaseURL,
		})
		tasks.ConfigurePlaintextCredentialMigration(tasks.PlaintextCredentialMigrationDeps{
			Queries:   sqlc.New(database.Pool()),
			Encryptor: enc,
		})
	}

	// Email dispatch (migration 047). Wired only when the encryptor
	// is available — the SMTP password is Fernet-encrypted and the
	// dispatcher can't decrypt without a key. The runtime no-ops on
	// a nil sender, so a misconfigured deployment degrades to a logged
	// "email dispatcher not configured" line and queued rows pile up
	// (the dispatcher's 1-hour ageRowsToSkipped guard catches them).
	if cfg.EncryptionKey != "" {
		if enc, encErr := auth.NewEncryptor(cfg.EncryptionKey); encErr == nil {
			q := sqlc.New(database.Pool())
			provider := email.NewSQLSettingsProvider(q, enc, 5*time.Second)
			sender := email.NewSender(provider, enc, log)
			sender.SetBrandingProvider(email.NewPlatformConfigBrandingProvider(q, ""))
			tasks.ConfigureEmail(tasks.EmailDeps{
				Queries:  q,
				Sender:   sender,
				Provider: provider,
			})
		} else {
			log.Warn("email dispatch disabled: encryptor init failed", "error", encErr)
		}
	} else {
		log.Warn("email dispatch disabled: ASTRONOMER_ENCRYPTION_KEY is not set")
	}

	// Create worker and scheduler. Both fail-fast on invalid REDIS_URL —
	// the old silent-fallback behavior was a production footgun in
	// air-gapped / split-network deployments.
	w, werr := worker.NewWorker(cfg.RedisURL, log)
	if werr != nil {
		log.Error("failed to start worker", "error", werr)
		os.Exit(1)
	}
	redisOpt, redisErr := asynq.ParseRedisURI(cfg.RedisURL)
	if redisErr != nil {
		// Already validated by NewWorker above; keep this fail-fast in case
		// a future edit changes worker initialization order.
		log.Error("failed to parse REDIS_URL for task outbox dispatcher", "error", redisErr)
		os.Exit(1)
	}
	taskOutboxClient := asynq.NewClient(redisOpt)
	defer func() {
		_ = taskOutboxClient.Close()
	}()
	tasks.ConfigureTaskOutboxDispatch(tasks.TaskOutboxDispatchDeps{
		Queries:  sqlc.New(database.Pool()),
		Enqueuer: taskOutboxClient,
	})
	w.RegisterHandlers()

	s, serr := worker.NewScheduler(cfg.RedisURL, log)
	if serr != nil {
		log.Error("failed to start scheduler", "error", serr)
		os.Exit(1)
	}
	if err := s.RegisterPeriodicTasks(); err != nil {
		log.Error("failed to register periodic tasks", "error", err)
		os.Exit(1)
	}

	// Set up signal-driven shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db.StartMetricsReporter(ctx, database.Pool(), log)
	inspector := asynq.NewInspector(redisOpt)
	defer func() {
		_ = inspector.Close()
	}()
	worker.StartQueueMetricsReporter(ctx, inspector, log)

	// Start worker and scheduler in background goroutines.
	errCh := make(chan error, 3)
	go func() {
		if err := w.Start(); err != nil {
			errCh <- fmt.Errorf("worker: %w", err)
		}
	}()
	go func() {
		if err := s.Start(); err != nil {
			errCh <- fmt.Errorf("scheduler: %w", err)
		}
	}()
	go func() {
		if err := worker.StartMetricsServer(ctx, cfg.WorkerMetricsAddr, log); err != nil {
			errCh <- fmt.Errorf("metrics server: %w", err)
		}
	}()

	observability.WithEvent(log, "worker_started").Info("astronomer-worker started", "redis_url", cfg.RedisURL)

	// Wait for shutdown signal or fatal error.
	select {
	case <-ctx.Done():
		observability.WithEvent(log, "worker_stopping").Info("received shutdown signal")
	case err := <-errCh:
		observability.WithEvent(log, "worker_runtime_error").Error("fatal error", "error", err)
	}

	w.Shutdown()
	s.Shutdown()

	// Drain pending audit events. Use a fresh timeout because the
	// signal-context (ctx) was already cancelled at this point.
	auditShutdownCtx, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := auditWriter.Shutdown(auditShutdownCtx); err != nil {
		observability.WithEvent(log, "worker_audit_shutdown_error").Warn("audit writer shutdown error",
			"dropped_total", auditWriter.DropCount(),
			"error", err,
		)
	}
	auditCancel()

	observability.WithEvent(log, "worker_stopped").Info("astronomer-worker stopped")
}
