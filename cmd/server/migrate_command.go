package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
)

// handleMigrateCommand applies the embedded migration set and exits. The
// server does this at boot anyway; the subcommand exists for the dev loop
// (scripts/dev.sh migrate, make migrate) and for operators who want to
// migrate ahead of a rollout. Exit codes: 0 applied/up to date, 1 failure.
func handleMigrateCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	databaseURL := flags.String("database", os.Getenv("DATABASE_URL"), "PostgreSQL URL (default $DATABASE_URL)")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall timeout including waiting on the migration lock")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *databaseURL == "" {
		cfg, err := config.Load()
		if err != nil || cfg.DatabaseURL == "" {
			_, _ = fmt.Fprintln(stderr, "migrate: -database or DATABASE_URL is required")
			return 2
		}
		*databaseURL = cfg.DatabaseURL
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	database, err := db.ConnectWithConfig(ctx, *databaseURL, db.PoolConfig{MaxConns: 2, MinConns: 1})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate: connect: %v\n", err)
		return 1
	}
	defer database.Close()
	result, err := database.Migrate(ctx, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "schema at version %d (%d applied)\n", result.Version, result.Applied)
	return 0
}
