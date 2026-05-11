package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
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

	// Create worker and scheduler.
	w := worker.NewWorker(cfg.RedisURL, log)
	w.RegisterHandlers()

	s := worker.NewScheduler(cfg.RedisURL, log)
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
		redisOpt = asynq.RedisClientOpt{Addr: "localhost:6379"}
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
