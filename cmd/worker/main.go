package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
	fmt.Printf("astronomer-worker %s (commit: %s, built: %s)\n",
		version.Version, version.GitCommit, version.BuildDate)

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

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

	// Start worker and scheduler in background goroutines.
	errCh := make(chan error, 2)
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

	log.Info("astronomer-worker started", "redis_url", cfg.RedisURL)

	// Wait for shutdown signal or fatal error.
	select {
	case <-ctx.Done():
		log.Info("received shutdown signal")
	case err := <-errCh:
		log.Error("fatal error", "error", err)
	}

	w.Shutdown()
	s.Shutdown()
	log.Info("astronomer-worker stopped")
}
