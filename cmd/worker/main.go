package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/hibiken/asynq"
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
	tasks.ConfigureRuntime(tasks.RuntimeDependencies{
		Queries:                 sqlc.New(database.Pool()),
		Log:                     log,
		AgentImageRepo:          cfg.AgentImageRepository,
		AgentImageTag:           cfg.AgentImageTag,
		PlatformName:            "Astronomer",
		AuditLogRetentionMonths: cfg.AuditLogRetentionMonths,
		Leader:                  leader.New(database.Pool(), log),
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
	})

	// Create worker and scheduler. Both fail-fast on invalid REDIS_URL —
	// the old silent-fallback behavior was a production footgun in
	// air-gapped / split-network deployments.
	w, werr := worker.NewWorker(cfg.RedisURL, log)
	if werr != nil {
		log.Error("failed to start worker", "error", werr)
		os.Exit(1)
	}
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
	redisOpt, redisErr := asynq.ParseRedisURI(cfg.RedisURL)
	if redisErr != nil {
		// Already validated by NewWorker/NewScheduler above; if we get
		// here REDIS_URL was somehow mutated between then and now.
		log.Error("failed to parse REDIS_URL for inspector", "error", redisErr)
		os.Exit(1)
	}
	inspector := asynq.NewInspector(redisOpt)
	defer inspector.Close()
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
	observability.WithEvent(log, "worker_stopped").Info("astronomer-worker stopped")
}
