package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/logging"
	"github.com/alphabravocompany/astronomer-go/pkg/telemetry"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	allowlistproviders "github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliveryresolver "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrollout"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/notify"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/redisconn"
	registrystartup "github.com/alphabravocompany/astronomer-go/internal/registry/startup"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// unavailableDeliveryDecryptor keeps public, credential-free source
// resolution usable in development while failing closed if an encrypted
// source is encountered. Production already refuses to start without the
// platform encryption key.
type unavailableDeliveryDecryptor struct{}

func (unavailableDeliveryDecryptor) Decrypt(crypto.Sealed) ([]byte, error) {
	return nil, fmt.Errorf("platform encryption key is unavailable")
}

func allowPrivateRCWebhooks(cfg *config.Config) bool {
	return cfg != nil && !strings.EqualFold(strings.TrimSpace(cfg.Env), "production") &&
		cfg.RCAllowPrivateWebhooks
}

func webhookHTTPClient(cfg *config.Config) *http.Client {
	// RC uses an owned disposable development cluster and an explicit opt-in so
	// the webhook dispatcher can prove product-path age decryption against a
	// host-side one-shot receiver. Production always retains the public-only
	// SSRF guard, even if the environment variable is set accidentally.
	if allowPrivateRCWebhooks(cfg) {
		return httpclient.SafeClientAllowPrivate(30 * time.Second)
	}
	return httpclient.SafeClient(30 * time.Second)
}

func alertNotificationTxRunner(database *db.DB) tasks.AlertNotificationRunTx {
	return func(ctx context.Context, fn func(tasks.AlertNotificationMutationTx) error) error {
		tx, err := database.Pool().Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin alert notification transaction: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit alert notification transaction: %w", err)
		}
		return nil
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		logging.Default().Error().Err(err).Msg("failed to load config")
		os.Exit(1)
	}
	log, err := logging.New(logging.Config{Level: cfg.LogLevel, Format: cfg.LogFormat, Component: "worker"})
	if err != nil {
		logging.Default().Error().Err(err).Msg("invalid logging configuration")
		os.Exit(1)
	}
	logging.SetDefault(log)
	log.Info().Str("event", "worker_starting").Str("version", version.Version).Str("commit", version.GitCommit).Str("built", version.BuildDate).Msg("starting astronomer worker binary")

	// Apply the server production security contract before starting any task.
	// Retained (not just probed) because the catalog:sync sweep needs it to
	// open the sealed helm_repositories.auth_config_* family. The
	// scheduled sweep and the interactive handler are separate readers of the
	// same credential; a worker without this key syncs private chart
	// repositories unauthenticated.
	//
	// monitoring:reconcile and alert:evaluate need it for the same reason
	// against the sealed monitoring_backends.auth_config_* family, and
	// the reconcile tick additionally re-seals what it resolved — so this
	// process needs both directions of the cipher, not just decrypt.
	platformEncryptor, encErr := cfg.LoadEncryptor()
	switch {
	case encErr == nil:
		log.Info().Str("current_key_id", platformEncryptor.CurrentKeyID()).
			Int("key_count", platformEncryptor.KeyCount()).
			Str("key_ids", strings.Join(platformEncryptor.KeyIDs(), ",")).
			Msg("encryption keyring loaded")
	case errors.Is(encErr, crypto.ErrNoKey):
		platformEncryptor = nil
		log.Warn().Msg("no encryption key configured (ASTRONOMER_ENCRYPTION_KEY / ASTRONOMER_ENCRYPTION_KEY_FILE); sealed credentials are unavailable")
	default:
		// A key IS configured but does not load: never continue without it.
		log.Error().Err(encErr).Msg("encryption keyring could not be loaded; refusing to start")
		os.Exit(1)
	}
	if secErr := config.ValidateProductionSecurity(cfg, platformEncryptor != nil); secErr != nil {
		// Only returns non-nil in production mode.
		log.Error().Err(secErr).Msg("production security config invalid; refusing to start")
		os.Exit(1)
	}
	// dev-keys-default-and-silent: the published chart sentinels are just as
	// forgeable in development, so report them on every boot in every env. The
	// worker decrypts the same credential columns the server does.
	observability.ReportInsecureDevKeys(logging.Slog(log), config.DevSentinelsInUse(cfg))

	if _, err := registrystartup.Resolve(context.Background(), cfg, logging.Slog(log), "worker"); err != nil {
		log.Error().Err(err).Msg("invalid registry policy")
		os.Exit(1)
	}

	// Telemetry — same telemetry.Init contract as the server. The worker's
	// asynq handlers extract traceparent from task payloads, so when both
	// processes point at the same OTLP endpoint one trace spans HTTP → asynq
	// → worker DB queries → tunnel calls. The returned handler serves the
	// shared client_golang + OTel registry on WORKER_METRICS_ADDR.
	telemetryCfg := workerTelemetryConfig(cfg)
	telemetryCfg.Logger = log
	otelShutdown, metricsHandler, err := telemetry.Init(context.Background(), telemetryCfg)
	if err != nil {
		log.Error().Err(err).Msg("failed to init telemetry")
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(ctx); err != nil {
			log.Warn().Err(err).Msg("otel shutdown error")
		}
	}()

	database, err := db.ConnectWithConfig(context.Background(), cfg.DatabaseURL, db.PoolConfig{
		MaxConns:          workerDBMaxConns(cfg.DBMaxConns, cfg.WorkerConcurrency),
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   time.Duration(cfg.DBMaxConnLifetimeMin) * time.Minute,
		MaxConnIdleTime:   time.Duration(cfg.DBMaxConnIdleMin) * time.Minute,
		HealthCheckPeriod: time.Duration(cfg.DBHealthCheckPeriodSec) * time.Second,
	})
	if err != nil {
		log.Error().Err(err).Msg("failed to connect database")
		os.Exit(1)
	}
	defer database.Close()
	taskLeader, leaderErr := leader.NewDedicated(context.Background(), database.Pool(), logging.Slog(log))
	if leaderErr != nil {
		log.Error().Err(leaderErr).Msg("failed to initialize leader-election pool")
		os.Exit(1)
	}
	defer taskLeader.Close()
	// Only the server applies migrations; a worker that comes up first waits
	// for the schema instead of crash-looping against missing tables, then
	// runs the same schema guard the server runs in NewApp.
	schemaWaitCtx, cancelSchemaWait := context.WithTimeout(context.Background(), 10*time.Minute)
	if waitErr := database.WaitForSchema(schemaWaitCtx, logging.Slog(log)); waitErr != nil {
		cancelSchemaWait()
		log.Error().Err(waitErr).Msg("database schema not ready; refusing to start")
		os.Exit(1)
	}
	cancelSchemaWait()
	if shErr := database.SchemaHealth(context.Background()); shErr != nil {
		log.Error().Err(shErr).Msg("schema health check failed; refusing to start")
		os.Exit(1)
	}
	if _, err := observability.EnsureInstanceID(context.Background(), sqlc.New(database.Pool())); err != nil {
		log.Error().Err(err).Msg("failed to ensure observability instance id")
		os.Exit(1)
	}
	log = log.With().Str("instance_id", observability.InstanceID()).Logger()
	logging.SetDefault(log)

	// Async batched audit writer — shared design with cmd/server. The
	// worker only records audit rows from the cluster-decommission
	// reconciler today, but the cost reduction is the same: no DB
	// round-trip on the task path. Bounded by the same Shutdown
	// deadline as the worker process below.
	auditQueries := sqlc.New(database.Pool())
	auditWriter := audit.NewWriter(auditQueries, logging.Slog(log))
	auditWriter.Start(context.Background())
	audit.SetWriter(auditWriter)
	defer audit.SetWriter(nil)

	// Enqueuer for tasks that fan out follow-up work (e.g. the alert
	// evaluator handing notification:send tasks to the notification
	// dispatcher). This is the worker process that runs HandleAlertEvaluation,
	// so it must be able to enqueue.
	runtimeRedisOpt, redisOptErr := redisconn.Parse(cfg.RedisURL)
	if redisOptErr != nil {
		log.Error().Err(redisOptErr).Msg("failed to parse redis uri for runtime enqueuer")
		os.Exit(1)
	}
	runtimeEnqueuer := asynq.NewClient(runtimeRedisOpt)
	// Process-lifetime client: this defer only runs as main returns, where a
	// close error has no remaining consumer.
	defer func() { _ = runtimeEnqueuer.Close() }()
	// P4.9 — Redis-attached events bus so worker-side writes (alert-event
	// ingestion/resolution, anomaly-baseline recompute) surface on the
	// server pods' SSE streams. Publish-only: the worker runs no relay and
	// has no local subscribers.
	eventBus := events.NewBus()
	if client, ok := runtimeRedisOpt.MakeRedisClient().(*redis.Client); ok && client != nil {
		eventBus.AttachRedis(client, events.DefaultRedisChannel, logging.Slog(log))
	}
	backupQueries := sqlc.New(database.Pool())
	backupExecutor := handler.NewAdminDrillHandler(backupQueries)
	backupExecutor.SetEncryptor(platformEncryptor)
	backupExecutor.SetBackupRuntime(cfg.ManagementBackupImage, cfg.ManagementBackupServiceAccount)
	if restCfg, kErr := rest.InClusterConfig(); kErr == nil {
		if client, clientErr := kubernetes.NewForConfig(restCfg); clientErr == nil {
			backupExecutor.SetKubernetes(client, cfg.PodNamespace, cfg.ReleaseName)
		}
	}
	coreRuntime := tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries:                       sqlc.New(database.Pool()),
		Log:                           logging.Slog(log),
		AgentImageRepo:                cfg.AgentImageRepository,
		ImageRegistry:                 cfg.RegistryLocal,
		AgentImageTag:                 cfg.AgentImageTag,
		SystemArtifactURL:             cfg.DeliveryFluxDistributionRepository,
		SystemArtifactDigest:          cfg.DeliveryFluxDistributionDigest,
		SystemOIDCIssuer:              cfg.DeliveryFluxDistributionOIDCIssuer,
		SystemOIDCIdentity:            cfg.DeliveryFluxDistributionCertificateIdentity,
		SystemPublicKeys:              cfg.DeliveryFluxDistributionKeyring,
		PlatformName:                  "Astronomer",
		ChartRecommendationPolicy:     catalog.NewRecommendationPolicy(cfg.ChartRatingBayesianAverage, cfg.ChartRatingBayesianWeight),
		AuditLogRetentionMonths:       cfg.AuditLogRetentionMonths,
		ClusterTombstoneRetentionDays: cfg.ClusterTombstoneRetentionDays,
		RegistrationTokenTTLHours:     cfg.RegistrationTokenTTLHours,
		Leader:                        taskLeader,
		Enqueuer:                      runtimeEnqueuer,
		Bus:                           eventBus,
		CatalogDecryptor:              tasks.CatalogDecryptorFor(platformEncryptor),
		MonitoringCipher:              tasks.MonitoringCipherFor(platformEncryptor),
		ManagementBackup:              backupExecutor,
		AlertNotificationRunTx:        alertNotificationTxRunner(database),
	}}
	if err := tasks.ConfigureCatalogNetwork(&coreRuntime.Deps, cfg); err != nil {
		log.Error().Err(err).Msg("catalog network policy is invalid")
		os.Exit(1)
	}
	allowlistQueries := sqlc.New(database.Pool())
	var allowlistMaterializer allowlistproviders.CloudCredentialMaterializer
	if platformEncryptor != nil {
		materializer, materializerErr := allowlistproviders.NewSQLCredentialMaterializer(allowlistQueries, platformEncryptor)
		if materializerErr != nil {
			log.Error().Err(materializerErr).Msg("failed to configure API-server allow-list credential materializer")
			os.Exit(1)
		}
		allowlistMaterializer = materializer
	} else {
		log.Warn().Msg("API-server allow-list cloud writes will fail closed: encryption key is unavailable")
	}
	allowlistRegistry := allowlistproviders.NewRegistry()
	allowlistRegistry.Register(allowlistproviders.NewEKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewGKEProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewAKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewDOKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewSelfManagedProvider())
	allowlistRuntime := tasks.ApiserverAllowlistRuntime{Deps: tasks.ApiserverAllowlistReconcileDeps{
		Queries:          allowlistQueries,
		Registry:         allowlistRegistry,
		ClusterShaper:    allowlistproviders.ClusterFromSQLC,
		AuditWriter:      allowlistQueries,
		AstronomerEgress: allowlist.ParseAstronomerEgress(cfg.TunnelEgressCIDRs),
	}}
	deliveryQueries := sqlc.New(database.Pool())
	deliveryVerifier, deliveryVerifierErr := deliveryresolver.NewExecVerifier(cfg.DeliverySourceTrustDirectory)
	if deliveryVerifierErr != nil {
		log.Error().Err(deliveryVerifierErr).Msg("failed to configure delivery source signature verifier")
		os.Exit(1)
	}
	deliverySourcePolicies, deliverySourcePoliciesErr := deliveryresolver.NewStaticPolicyProvider(
		cfg.DeliverySourceAllowedPrivateHosts, cfg.DeliverySourceEgressCIDRs, cfg.DeliverySourceProxyURL,
	)
	if deliverySourcePoliciesErr != nil {
		log.Error().Err(deliverySourcePoliciesErr).Msg("failed to configure delivery source network policy")
		os.Exit(1)
	}
	deliverySourceService := deliveryresolver.New(deliveryVerifier)
	deliverySourceService.SetSSHAllowed(cfg.DeliverySourceAllowSSH)
	var deliveryDecryptor deliveryresolver.ByteDecryptor = unavailableDeliveryDecryptor{}
	if platformEncryptor != nil {
		deliveryDecryptor = platformEncryptor
	} else {
		log.Warn().Msg("delivery resolver has no encryption key; credentialed sources will be rejected")
	}
	deliverySourceWorker, deliverySourceWorkerErr := deliveryresolver.NewPostgresWorker(
		database.Pool(), deliverySourceService, deliveryDecryptor,
		deliverySourcePolicies, "",
	)
	if deliverySourceWorkerErr != nil {
		log.Error().Err(deliverySourceWorkerErr).Msg("failed to configure delivery source resolver")
		os.Exit(1)
	}
	deliverySourceWorker.SetLimits(deliveryresolver.Limits{
		MaxArtifactBytes:  cfg.DeliverySourceMaxArtifactBytes,
		MaxHelmChartBytes: cfg.DeliverySourceMaxHelmChartBytes,
	})
	if cfg.DeliverySourceCAFile != "" {
		caBundle, caErr := os.ReadFile(cfg.DeliverySourceCAFile)
		if caErr != nil || len(caBundle) == 0 || len(caBundle) > 1<<20 {
			log.Error().Err(caErr).Msg("failed to load bounded delivery source CA bundle")
			os.Exit(1)
		}
		deliverySourceWorker.SetBaseCABundle(caBundle)
		clear(caBundle)
	}
	deliveryReconciler, deliveryReconcilerErr := deliveryrollout.NewPostgresReconciler(
		database.Pool(), maintenance.NewEvaluator(deliveryQueries), "",
	)
	if deliveryReconcilerErr != nil {
		log.Error().Err(deliveryReconcilerErr).Msg("failed to configure delivery rollout reconciler")
		os.Exit(1)
	}
	deliverySystemReconciler, deliverySystemReconcilerErr := systemrollout.New(database.Pool())
	if deliverySystemReconcilerErr != nil {
		log.Error().Err(deliverySystemReconcilerErr).Msg("failed to configure delivery system rollout reconciler")
		os.Exit(1)
	}
	deliveryRuntime := tasks.DeliveryRuntime{
		SourceResolver:          deliverySourceWorker,
		RolloutReconciler:       deliveryReconciler,
		SystemRolloutReconciler: deliverySystemReconciler,
	}
	maintenanceRuntime := tasks.MaintenanceRuntime{
		AgentTokens: tasks.AgentTokenRotateDeps{Queries: sqlc.New(database.Pool())},
	}
	gitopsRuntime := tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
		Queries:    sqlc.New(database.Pool()),
		Enqueuer:   runtimeEnqueuer,
		TaskOutbox: sqlc.New(database.Pool()),
		Decryptor:  platformEncryptor,
		Log:        logging.Slog(log),
	}}

	// Email dispatch (migration 047). The SMTP password is age-sealed,
	// so production startup validation refuses queue consumption unless the
	// encryptor, settings provider, and sender are all present.
	var emailDispatchDeps tasks.EmailDeps
	if platformEncryptor != nil {
		q := sqlc.New(database.Pool())
		provider := email.NewSQLSettingsProvider(q, platformEncryptor, 5*time.Second)
		sender := email.NewSender(provider, platformEncryptor, logging.Slog(log))
		branding := email.NewPlatformConfigBrandingProvider(q, "")
		sender.SetBrandingProvider(branding)
		emailDispatchDeps = tasks.EmailDeps{Queries: q, Sender: sender, Provider: provider}
		notificationEmails := email.NewEnqueuer(q, branding, logging.Slog(log))
		notificationEmails.SetOverrideLookup(func(ctx context.Context, key string) (email.Overrides, bool) {
			resolved, resolveErr := notify.Resolve(ctx, q, key)
			if resolveErr != nil || !resolved.HasOverride {
				return email.Overrides{}, false
			}
			return email.Overrides{Subject: resolved.Subject, BodyText: resolved.Body}, true
		})
		coreRuntime.Deps.NotificationEmail = notificationEmails
	} else {
		log.Warn().Msg("email dispatch disabled: ASTRONOMER_ENCRYPTION_KEY is not set")
	}

	// Webhook and SIEM dispatch are worker-owned outbound jobs. Their bus taps
	// live in the API process, but the durable delivery queues are drained here.
	var webhookDispatchDeps tasks.WebhookDeps
	var siemDispatchDeps tasks.SIEMDeps
	if platformEncryptor != nil {
		q := sqlc.New(database.Pool())
		webhookSender := webhook.NewSender(webhookHTTPClient(cfg))
		webhookSender.SetOverrideLookup(func(ctx context.Context, key string) (string, bool) {
			resolved, resolveErr := notify.Resolve(ctx, q, key)
			if resolveErr != nil || !resolved.HasOverride {
				return "", false
			}
			return resolved.Body, true
		})
		webhookDispatchDeps = tasks.WebhookDeps{Queries: q, Sender: webhookSender, Encryptor: platformEncryptor}
		siemDispatchDeps = tasks.SIEMDeps{
			Queries: q, Encryptor: platformEncryptor, HTTPClient: httpclient.SafeClient(30 * time.Second),
		}
	}
	queueInspector := asynq.NewInspector(runtimeRedisOpt)
	defer func() { _ = queueInspector.Close() }()
	dispatchRuntime := tasks.DispatchRuntime{
		Email:       emailDispatchDeps,
		Webhook:     webhookDispatchDeps,
		SIEM:        siemDispatchDeps,
		TaskOutbox:  tasks.TaskOutboxDispatchDeps{Queries: sqlc.New(database.Pool()), Enqueuer: runtimeEnqueuer},
		AuditOutbox: tasks.AuditOutboxDispatchDeps{Queries: sqlc.New(database.Pool())},
		AdminQueue:  tasks.AdminQueueOperationDeps{Queries: sqlc.New(database.Pool()), Inspector: queueInspector},
	}

	// Create worker and scheduler. Both fail-fast on invalid REDIS_URL —
	// the old silent-fallback behavior was a production footgun in
	// air-gapped / split-network deployments.
	queueTerminalPublisher, publisherErr := charlie.NewQueueTerminalFailurePublisher(sqlc.New(database.Pool()))
	if publisherErr != nil {
		log.Error().Msg("failed to configure Charlie queue terminal failure publisher")
		os.Exit(1)
	}
	charlieAlertPlanner, plannerErr := charlie.NewFindingAlertPlanner(database.Pool())
	if plannerErr != nil {
		log.Error().Msg("failed to configure Charlie alert reconciliation")
		os.Exit(1)
	}
	alertRuntime := tasks.CharlieAlertRuntime{
		Queries:    sqlc.New(database.Pool()),
		WriteFence: charlie.NewDistributedWriteFence(database.Pool()),
		Reconciler: charlieAlertPlanner,
	}
	standaloneRuntime := worker.StandaloneRuntime{
		Features: tasks.StandaloneRuntimeFeatures{ManagementBackup: cfg.ManagementBackupEnabled},
		Core:     coreRuntime,
		Delivery: deliveryRuntime, Dispatch: dispatchRuntime, Alerts: alertRuntime,
		Maintenance: maintenanceRuntime, Allowlists: allowlistRuntime, GitOps: gitopsRuntime,
	}
	w, werr := worker.NewWorker(cfg.RedisURL, cfg.WorkerConcurrency, logging.Slog(log), standaloneRuntime, worker.NewTerminalFailureErrorHandler(queueTerminalPublisher, logging.Slog(log)))
	if werr != nil {
		log.Error().Err(werr).Msg("failed to start worker")
		os.Exit(1)
	}
	if err := worker.ValidateTaskRegistry(); err != nil {
		log.Error().Err(err).Msg("invalid task ownership registry")
		os.Exit(1)
	}
	if config.IsProduction(cfg) {
		capabilities := map[worker.TaskCapability]bool{
			worker.CapabilityDatabase:     true,
			worker.CapabilityRedis:        true,
			worker.CapabilityOutboundHTTP: true,
			worker.CapabilityDelivery:     true,
			worker.CapabilityEncryption:   platformEncryptor != nil,
		}
		if err := worker.ValidateTaskCapabilities(worker.TaskOwnerWorker, capabilities); err != nil {
			log.Error().Err(err).Msg("worker task dependencies incomplete; refusing to consume queues")
			os.Exit(1)
		}
		if err := tasks.ValidateStandaloneRuntime(standaloneRuntime.Features, coreRuntime, deliveryRuntime, dispatchRuntime, alertRuntime, maintenanceRuntime, allowlistRuntime, gitopsRuntime); err != nil {
			log.Error().Err(err).Msg("worker runtime composition incomplete; refusing to consume queues")
			os.Exit(1)
		}
	}
	w.RegisterHandlers()

	s, serr := worker.NewScheduler(cfg.RedisURL, logging.Slog(log), worker.SchedulerFeatures{CRDOwnership: cfg.CRDEnabled})
	if serr != nil {
		log.Error().Err(serr).Msg("failed to start scheduler")
		os.Exit(1)
	}
	if err := s.RegisterPeriodicTasks(); err != nil {
		log.Error().Err(err).Msg("failed to register periodic tasks")
		os.Exit(1)
	}

	// Set up signal-driven shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db.StartMetricsReporter(ctx, database.Pool(), logging.Slog(log))
	worker.StartQueueMetricsReporter(ctx, queueInspector, logging.Slog(log))
	stopGenerationReporter := startMetricGenerationReporter(ctx, database.Pool().BeginTx)
	defer stopGenerationReporter()

	// Start worker and scheduler in background goroutines.
	errCh := make(chan error, 3)
	go func() {
		if err := w.Run(ctx); err != nil {
			errCh <- fmt.Errorf("worker: %w", err)
		}
	}()
	go func() {
		if err := s.Start(); err != nil {
			errCh <- fmt.Errorf("scheduler: %w", err)
		}
	}()
	go func() {
		// C-02: the metrics listener also serves /healthz so the chart's
		// worker probe (httpGet /healthz:9090) has a real target. Redis
		// reachability via the asynq client is the meaningful liveness signal
		// for a queue consumer — if it can't reach Redis it can't process or
		// enqueue anything.
		if err := worker.StartMetricsServer(ctx, cfg.WorkerMetricsAddr, runtimeEnqueuer, metricsHandler, logging.Slog(log)); err != nil {
			errCh <- fmt.Errorf("metrics server: %w", err)
		}
	}()

	// Never log cfg.RedisURL raw: with external Redis it carries the
	// password in the userinfo. Log only scheme+host so the endpoint is
	// still identifiable without leaking the credential.
	redisEndpoint := "<unparseable>"
	if u, perr := url.Parse(cfg.RedisURL); perr == nil && u.Host != "" {
		redisEndpoint = u.Scheme + "://" + u.Host
	}
	log.Info().Str("event", "worker_started").Str("redis_endpoint", redisEndpoint).Msg("astronomer-worker started")

	// Wait for shutdown signal or fatal error.
	select {
	case <-ctx.Done():
		log.Info().Str("event", "worker_stopping").Msg("received shutdown signal")
	case err := <-errCh:
		log.Error().Str("event", "worker_runtime_error").Err(err).Msg("fatal error")
	}

	stopGenerationReporter()
	w.Shutdown()
	s.Shutdown()

	// Drain pending audit events. Use a fresh timeout because the
	// signal-context (ctx) was already cancelled at this point.
	auditShutdownCtx, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := auditWriter.Shutdown(auditShutdownCtx); err != nil {
		log.Warn().Str("event", "worker_audit_shutdown_error").Uint64("dropped_total", auditWriter.DropCount()).Err(err).Msg("audit writer shutdown error")
	}
	auditCancel()

	log.Info().Str("event", "worker_stopped").Msg("astronomer-worker stopped")
}

// workerDBMaxConns keeps the standalone worker's SQL capacity above its job
// concurrency. Leader leases use a separate pool, so this headroom is reserved
// for task queries, dispatch bookkeeping, and metrics rather than pinned locks.
// An explicit operator value is authoritative.
func workerDBMaxConns(configured int32, concurrency int) int32 {
	if configured > 0 {
		return configured
	}
	const (
		minimum  = int32(25)
		headroom = int32(8)
	)
	if concurrency <= int(minimum-headroom) {
		return minimum
	}
	return int32(concurrency) + headroom
}
