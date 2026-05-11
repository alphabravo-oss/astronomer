package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Set up structured logger.
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	observability.WithEvent(logger, "server_starting").Info("starting astronomer server",
		"version", version.Version,
		"commit", version.GitCommit,
		"env", cfg.Env,
	)

	srv, err := server.NewApp(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("failed to initialize server", "error", err)
		os.Exit(1)
	}
	if srv.DB() != nil {
		queries := sqlc.New(srv.DB().Pool())
		if _, err := observability.EnsureInstanceID(context.Background(), queries); err != nil {
			logger.Error("failed to ensure observability instance id", "error", err)
			os.Exit(1)
		}
		logger = observability.Logger(logger)
		slog.SetDefault(logger)
		// Rancher-style: if no users exist, create the admin with either
		// $ASTRONOMER_BOOTSTRAP_PASSWORD or a random password (logged once)
		// and flag must_change_password so the dashboard forces a rotation
		// on first sign-in.
		if err := auth.EnsureBootstrapAdmin(context.Background(), queries, logger); err != nil {
			logger.Error("failed to ensure bootstrap admin", "error", err)
			os.Exit(1)
		}
		// Seed platform_configuration.server_url from the Helm value so the
		// local Argo self-management loop knows what hostname to put on the
		// self-manage Application without requiring a manual settings step.
		if err := auth.EnsurePlatformConfig(context.Background(), queries, cfg.ServerURL, "", logger); err != nil {
			logger.Error("failed to ensure platform config", "error", err)
			os.Exit(1)
		}
	}

	// Graceful shutdown on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if srv.DB() != nil {
		db.StartMetricsReporter(ctx, srv.DB().Pool(), logger)
	}

	go func() {
		if err := srv.Start(":8000"); err != nil {
			observability.WithEvent(logger, "server_runtime_error").Error("server error", "error", err)
			os.Exit(1)
		}
	}()
	go func() {
		if err := server.StartMetricsServer(ctx, cfg.ServerMetricsAddr, logger); err != nil {
			observability.WithEvent(logger, "server_metrics_listener_error").Error("server metrics listener error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	observability.WithEvent(logger, "server_stopping").Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		observability.WithEvent(logger, "server_shutdown_error").Error("shutdown error", "error", err)
		os.Exit(1)
	}

	observability.WithEvent(logger, "server_stopped").Info("server stopped")
}
