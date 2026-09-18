package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrelease"
	"github.com/alphabravocompany/astronomer-go/internal/grafanaproxy"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func handleCLI(args []string, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}

	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		_, _ = fmt.Fprintf(stdout, "astronomer-server %s (commit %s, built %s)\n", version.Version, version.GitCommit, version.BuildDate)
		return true, 0
	}

	if args[0] == "grafana-proxy" {
		cfg, err := grafanaproxy.ParseConfig(os.Getenv("LISTEN_ADDR"), os.Getenv("GRAFANA_UPSTREAM"), os.Getenv("ASTRONOMER_URL"), os.Getenv("GRAFANA_PUBLIC_PATH"), os.Getenv("GRAFANA_PROXY_KEY"))
		if err == nil {
			err = grafanaproxy.Run(cfg)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "grafana-proxy: %v\n", err)
			return true, 1
		}
		return true, 0
	}

	if args[0] == "loki-auth" {
		cfg, err := lokiauth.ParseConfig(os.Getenv("LISTEN_ADDR"), os.Getenv("LOKI_UPSTREAM"), os.Getenv("HASHES_PATH"), os.Getenv("ACL_PATH"), os.Getenv("QUERY_KEY_PATH"))
		if err == nil {
			err = lokiauth.Run(cfg)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "loki-auth: %v\n", err)
			return true, 1
		}
		return true, 0
	}

	_, _ = fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
	return true, 2
}

func main() {
	// Command handling must happen before configuration, database, bootstrap,
	// or embedded-agent initialization. In particular, a provenance probe must
	// never start a second server process or rotate durable agent credentials.
	if handled, exitCode := handleCLI(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(exitCode)
	}

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
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	observability.WithEvent(logger, "server_starting").Info("starting astronomer server",
		"version", version.Version,
		"commit", version.GitCommit,
		"env", cfg.Env,
	)

	// Distributed tracing foundation. No-op when
	// OTEL_EXPORTER_OTLP_ENDPOINT is unset; otherwise wires an OTLP/HTTP
	// exporter behind the global TracerProvider so the chi otelhttp
	// middleware, pgx OTel tracer, and tunnel originator spans all
	// flow into the same backend.
	tracingCfg := observability.TracingConfig{
		Endpoint: cfg.OTELExporterEndpoint, Insecure: cfg.OTELExporterInsecure,
		Headers:     observability.ParseOTLPHeaders(cfg.OTELExporterHeaders),
		ServiceName: cfg.OTELServiceName, ServiceVersion: cfg.OTELServiceVersion,
		SamplerRatio: cfg.OTELSamplerRatio,
	}
	tracingCfg.ServiceName = "astronomer-server"
	tracingCfg.ServiceVersion = version.Version
	tracingCfg.Environment = cfg.Env
	tracingCfg.ServiceNamespace = "astronomer"
	tracingCfg.ServiceInstanceID = cfg.ProcessHostname
	otelShutdown, err := observability.InitTracing(context.Background(), logger, tracingCfg)
	if err != nil {
		logger.Error("failed to init otel tracing", "error", err)
		os.Exit(1)
	}

	srv, err := server.NewApp(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("failed to initialize server", "error", err)
		os.Exit(1)
	}
	// auditWriter is started below once the DB pool is verified; the
	// declaration here keeps Shutdown reachable from the single defer in
	// case of an early exit.
	var auditWriter *audit.Writer
	if srv.DB() != nil {
		distributionDigest, digestErr := fluxdistribution.ControllerSetDigest()
		if digestErr != nil {
			logger.Error("failed to identify embedded Flux distribution", "error", digestErr)
			os.Exit(1)
		}
		if configured := cfg.DeliveryFluxVersion; configured != "" && configured != fluxdistribution.Version() {
			logger.Error("configured Flux version differs from embedded distribution", "configured", configured, "embedded", fluxdistribution.Version())
			os.Exit(1)
		}
		changed, releaseErr := systemrelease.Ensure(context.Background(), srv.DB().Pool(), systemrelease.Config{
			Enabled: cfg.DeliveryEnabled, Version: version.Version,
			ArtifactRepository: cfg.DeliveryFluxDistributionRepository,
			ArtifactDigest:     cfg.DeliveryFluxDistributionDigest,
			DistributionDigest: distributionDigest,
			AgentVersion:       version.Version, AgentImage: cfg.AgentImageRepository,
			MinimumKubernetes:   cfg.DeliveryKubernetesMinMinor,
			MaximumKubernetes:   cfg.DeliveryKubernetesMaxMinor,
			CertificateIssuer:   cfg.DeliveryFluxDistributionOIDCIssuer,
			CertificateIdentity: cfg.DeliveryFluxDistributionCertificateIdentity,
		})
		if releaseErr != nil {
			logger.Error("failed to ensure signed delivery system release", "error", releaseErr)
			os.Exit(1)
		}
		if changed {
			logger.Info("signed delivery system release promoted", "version", version.Version, "distribution_digest", distributionDigest)
		}
		queries := sqlc.New(srv.DB().Pool())
		if _, err := observability.EnsureInstanceID(context.Background(), queries); err != nil {
			logger.Error("failed to ensure observability instance id", "error", err)
			os.Exit(1)
		}
		logger = observability.Logger(logger)
		slog.SetDefault(logger)
		// Async batched audit writer. The per-request synchronous
		// INSERT INTO audit_log used to add one DB round-trip to every
		// mutating handler's critical path; the writer drains a
		// bounded channel into multi-row INSERTs in a single
		// background goroutine. Crash window: ~250 ms or 50 events
		// (see writer.go for the trade-off discussion). When the
		// writer is nil — e.g. a test main — audit.Record falls back
		// to the sync insert through the supplied Querier.
		auditWriter = audit.NewWriter(queries, logger)
		auditWriter.Start(context.Background())
		audit.SetWriter(auditWriter)
		srv.AddShutdownHook("audit writer", func(ctx context.Context) error {
			defer audit.SetWriter(nil)
			err := auditWriter.Shutdown(ctx)
			if err != nil {
				observability.WithEvent(logger, "server_audit_shutdown_error").Warn("audit writer shutdown error",
					"dropped_total", auditWriter.DropCount(),
					"error", err,
				)
			}
			return err
		})
		// Rancher-style: if no users exist, create the admin with either
		// $ASTRONOMER_BOOTSTRAP_PASSWORD or a random password (logged once)
		// and flag must_change_password so the dashboard forces a rotation
		// on first sign-in.
		if err := auth.EnsureBootstrapAdmin(context.Background(), queries, auth.BootstrapAdminConfig{
			Password:            cfg.BootstrapAdminPassword,
			Username:            cfg.BootstrapAdminUsername,
			Email:               cfg.BootstrapAdminEmail,
			ForcePasswordChange: cfg.BootstrapAdminForcePasswordChange,
		}, logger); err != nil {
			logger.Error("failed to ensure bootstrap admin", "error", err)
			os.Exit(1)
		}
		// Seed platform_configuration.server_url from the Helm value so internal
		// controllers can build stable public URLs without manual setup.
		if err := auth.EnsurePlatformConfig(context.Background(), queries, cfg.ServerURL, "", logger); err != nil {
			logger.Error("failed to ensure platform config", "error", err)
			os.Exit(1)
		}
	}

	// Graceful shutdown on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv.AddShutdownHook("otel pipeline", func(ctx context.Context) error {
		err := otelShutdown(ctx)
		if err != nil {
			observability.WithEvent(logger, "server_otel_shutdown_error").Warn("otel shutdown error", "error", err)
		}
		return err
	})

	if srv.DB() != nil {
		if err := srv.AddRuntimeLoop("database-metrics-reporter", true, func(ctx context.Context) error {
			db.RunMetricsReporter(ctx, srv.DB().Pool(), logger)
			return nil
		}); err != nil {
			logger.Error("failed to register database metrics reporter", "error", err)
			os.Exit(1)
		}
	}
	if cfg.ServerMetricsAddr != "" {
		if err := srv.AddRuntimeLoop("metrics-listener", true, func(ctx context.Context) error {
			return server.StartMetricsServer(ctx, cfg.ServerMetricsAddr, logger)
		}); err != nil {
			logger.Error("failed to register metrics listener", "error", err)
			os.Exit(1)
		}
	}

	runtimeResults := make(chan error, 1)
	go func() { runtimeResults <- srv.Start(":8000") }()
	runtimeFailed := false
	runtimeJoined := false
	select {
	case <-ctx.Done():
	case result := <-runtimeResults:
		runtimeJoined = true
		if ctx.Err() == nil {
			runtimeFailed = true
			if result == nil {
				result = fmt.Errorf("server exited unexpectedly")
			}
			observability.WithEvent(logger, "server_runtime_error").Error("runtime component exited", "error", result)
		}
		stop()
	}
	observability.WithEvent(logger, "server_stopping").Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		observability.WithEvent(logger, "server_shutdown_error").Error("shutdown error", "error", err)
		runtimeFailed = true
	}
	if !runtimeJoined {
		select {
		case err := <-runtimeResults:
			if err != nil {
				observability.WithEvent(logger, "server_runtime_error").Error("server listener stopped with error", "error", err)
				runtimeFailed = true
			}
		case <-shutdownCtx.Done():
			observability.WithEvent(logger, "server_shutdown_error").Error("server listener did not join", "error", shutdownCtx.Err())
			runtimeFailed = true
		}
	}

	observability.WithEvent(logger, "server_stopped").Info("server stopped")
	if runtimeFailed {
		os.Exit(1)
	}
}
