package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/logging"

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

	if args[0] == "migrate" {
		return true, handleMigrateCommand(args[1:], stdout, stderr)
	}

	if args[0] == "database-update-export" {
		return true, handleDatabaseExportCommand(args[1:], stdout, stderr)
	}
	if args[0] == "database-update-import" {
		return true, handleDatabaseImportCommand(args[1:], stdout, stderr)
	}

	if args[0] == "database-update-publish" {
		return true, handleDatabasePublicationCommand(args[1:], stdout, stderr)
	}

	if args[0] == "grafana-proxy" {
		env, err := config.LoadStandaloneProxyEnv()
		var cfg grafanaproxy.Config
		if err == nil {
			cfg, err = grafanaproxy.ParseConfig(env.ListenAddr, env.GrafanaUpstream, env.AstronomerURL, env.GrafanaPublicPath, env.GrafanaProxyKey)
		}
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
		env, err := config.LoadStandaloneProxyEnv()
		var cfg lokiauth.Config
		if err == nil {
			cfg, err = lokiauth.ParseConfig(env.ListenAddr, env.LokiUpstream, env.HashesPath, env.ACLPath, env.QueryKeyPath)
		}
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
		logging.Default().Error().Err(err).Msg("failed to load config")
		os.Exit(1)
	}

	logger, err := logging.New(logging.Config{Level: cfg.LogLevel, Format: cfg.LogFormat, Component: "server"})
	if err != nil {
		logging.Default().Error().Err(err).Msg("invalid logging configuration")
		os.Exit(1)
	}
	logging.SetDefault(logger)

	logger.Info().Str("event", "server_starting").Str("version", version.Version).Str("commit", version.GitCommit).Str("env", cfg.Env).Msg("starting astronomer server")

	// Distributed tracing foundation. No-op when
	// OTEL_EXPORTER_OTLP_ENDPOINT is unset; otherwise wires an OTLP gRPC
	// exporter (OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf opts into HTTP) behind the global TracerProvider so the chi otelhttp
	// middleware, pgx OTel tracer, and tunnel originator spans all
	// flow into the same backend.
	tracingCfg := observability.TracingConfig{
		Endpoint: cfg.OTELExporterEndpoint, Protocol: cfg.OTELExporterProtocol, Insecure: cfg.OTELExporterInsecure,
		Headers:     observability.ParseOTLPHeaders(cfg.OTELExporterHeaders),
		ServiceName: cfg.OTELServiceName, ServiceVersion: cfg.OTELServiceVersion,
		SamplerRatio: cfg.OTELSamplerRatio,
	}
	tracingCfg.ServiceName = "astronomer-server"
	tracingCfg.ServiceVersion = version.Version
	tracingCfg.Environment = cfg.Env
	tracingCfg.ServiceNamespace = "astronomer"
	tracingCfg.ServiceInstanceID = cfg.ProcessHostname
	otelShutdown, err := observability.InitTracing(context.Background(), logging.Slog(logger), tracingCfg)
	if err != nil {
		logger.Error().Err(err).Msg("failed to init otel tracing")
		os.Exit(1)
	}

	srv, err := server.NewApp(context.Background(), cfg, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to initialize server")
		os.Exit(1)
	}
	// auditWriter is started below once the DB pool is verified; the
	// declaration here keeps Shutdown reachable from the single defer in
	// case of an early exit.
	var auditWriter *audit.Writer
	if srv.DB() != nil {
		distributionDigest, digestErr := fluxdistribution.ControllerSetDigest()
		if digestErr != nil {
			logger.Error().Err(digestErr).Msg("failed to identify embedded Flux distribution")
			os.Exit(1)
		}
		if configured := cfg.DeliveryFluxVersion; configured != "" && configured != fluxdistribution.Version() {
			logger.Error().Str("configured", configured).Str("embedded", fluxdistribution.Version()).Msg("configured Flux version differs from embedded distribution")
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
			PublicKeys:          cfg.DeliveryFluxDistributionKeyring,
		})
		if releaseErr != nil {
			logger.Error().Err(releaseErr).Msg("failed to ensure signed delivery system release")
			os.Exit(1)
		}
		if changed {
			logger.Info().Str("version", version.Version).Str("distribution_digest", distributionDigest).Msg("signed delivery system release promoted")
		}
		queries := sqlc.New(srv.DB().Pool())
		if _, err := observability.EnsureInstanceID(context.Background(), queries); err != nil {
			logger.Error().Err(err).Msg("failed to ensure observability instance id")
			os.Exit(1)
		}
		logger = logger.With().Str("instance_id", observability.InstanceID()).Logger()
		logging.SetDefault(logger)
		// Async batched audit writer. The per-request synchronous
		// INSERT INTO audit_log used to add one DB round-trip to every
		// mutating handler's critical path; the writer drains a
		// bounded channel into multi-row INSERTs in a single
		// background goroutine. Crash window: ~250 ms or 50 events
		// (see writer.go for the trade-off discussion). When the
		// writer is nil — e.g. a test main — audit.Record falls back
		// to the sync insert through the supplied Querier.
		auditWriter = audit.NewWriter(queries, logging.Slog(logger))
		auditWriter.Start(context.Background())
		audit.SetWriter(auditWriter)
		srv.AddShutdownHook("audit writer", func(ctx context.Context) error {
			defer audit.SetWriter(nil)
			err := auditWriter.Shutdown(ctx)
			if err != nil {
				logger.Warn().Str("event", "server_audit_shutdown_error").Uint64("dropped_total", auditWriter.DropCount()).Err(err).Msg("audit writer shutdown error")
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
		}, logging.Slog(logger)); err != nil {
			logger.Error().Err(err).Msg("failed to ensure bootstrap admin")
			os.Exit(1)
		}
		// Seed platform_configuration.server_url from the Helm value so internal
		// controllers can build stable public URLs without manual setup.
		if err := auth.EnsurePlatformConfig(context.Background(), queries, cfg.ServerURL, "", logging.Slog(logger)); err != nil {
			logger.Error().Err(err).Msg("failed to ensure platform config")
			os.Exit(1)
		}
	}

	// Graceful shutdown on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv.AddShutdownHook("otel pipeline", func(ctx context.Context) error {
		err := otelShutdown(ctx)
		if err != nil {
			logger.Warn().Str("event", "server_otel_shutdown_error").Err(err).Msg("otel shutdown error")
		}
		return err
	})

	if srv.DB() != nil {
		if err := srv.AddRuntimeLoop("database-metrics-reporter", true, func(ctx context.Context) error {
			db.RunMetricsReporter(ctx, srv.DB().Pool(), logging.Slog(logger))
			return nil
		}); err != nil {
			logger.Error().Err(err).Msg("failed to register database metrics reporter")
			os.Exit(1)
		}
	}
	if cfg.ServerMetricsAddr != "" {
		if err := srv.AddRuntimeLoop("metrics-listener", true, func(ctx context.Context) error {
			return server.StartMetricsServer(ctx, cfg.ServerMetricsAddr, logger)
		}); err != nil {
			logger.Error().Err(err).Msg("failed to register metrics listener")
			os.Exit(1)
		}
	}

	runtimeResults := make(chan error, 1)
	go func() { runtimeResults <- srv.Start(cfg.ServerAddr) }()
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
			logger.Error().Str("event", "server_runtime_error").Err(result).Msg("runtime component exited")
		}
		stop()
	}
	logger.Info().Str("event", "server_stopping").Msg("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Str("event", "server_shutdown_error").Err(err).Msg("shutdown error")
		runtimeFailed = true
	}
	if !runtimeJoined {
		select {
		case err := <-runtimeResults:
			if err != nil {
				logger.Error().Str("event", "server_runtime_error").Err(err).Msg("server listener stopped with error")
				runtimeFailed = true
			}
		case <-shutdownCtx.Done():
			logger.Error().Str("event", "server_shutdown_error").Err(shutdownCtx.Err()).Msg("server listener did not join")
			runtimeFailed = true
		}
	}

	logger.Info().Str("event", "server_stopped").Msg("server stopped")
	if runtimeFailed {
		os.Exit(1)
	}
}
