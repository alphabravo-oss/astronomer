package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	allowlistproviders "github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
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
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// unavailableDeliveryDecryptor keeps public, credential-free source
// resolution usable in development while failing closed if an encrypted
// source is encountered. Production already refuses to start without the
// platform encryption key.
type unavailableDeliveryDecryptor struct{}

func (unavailableDeliveryDecryptor) DecryptBytes(string) ([]byte, error) {
	return nil, fmt.Errorf("platform encryption key is unavailable")
}

func allowPrivateRCWebhooks(cfg *config.Config) bool {
	return cfg != nil && !strings.EqualFold(strings.TrimSpace(cfg.Env), "production") &&
		strings.EqualFold(strings.TrimSpace(os.Getenv("ASTRONOMER_RC_ALLOW_PRIVATE_WEBHOOKS")), "true")
}

func webhookHTTPClient(cfg *config.Config) *http.Client {
	// RC uses an owned disposable development cluster and an explicit opt-in so
	// the webhook dispatcher can prove product-path Fernet decryption against a
	// host-side one-shot receiver. Production always retains the public-only
	// SSRF guard, even if the environment variable is set accidentally.
	if allowPrivateRCWebhooks(cfg) {
		return httpclient.SafeClientAllowPrivate(30 * time.Second)
	}
	return httpclient.SafeClient(30 * time.Second)
}

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

	// C-01: enforce the same production fail-fast the server runs. Without this
	// the worker stays Running with a typo'd ASTRONOMER_ENCRYPTION_KEY / bad
	// secret / plaintext DSN — silently no-op'ing its credential-migration
	// and email tasks — hiding exactly the misconfiguration
	// the check exists to surface. warn-only in dev (ValidateProductionSecurity
	// is a no-op outside production).
	encryptorReady := false
	// Retained (not just probed) because the catalog:sync sweep needs it to
	// unwrap helm_repositories.auth_config_encrypted — migration 145. The
	// scheduled sweep and the interactive handler are separate readers of the
	// same credential; a worker without this key syncs private chart
	// repositories unauthenticated.
	//
	// monitoring:reconcile and alert:evaluate need it for the same reason
	// against monitoring_backends.auth_config_encrypted (migration 146), and
	// the reconcile tick additionally re-seals what it resolved — so this
	// process needs both directions of the cipher, not just decrypt.
	var platformEncryptor *auth.Encryptor
	if cfg.EncryptionKey != "" {
		if e, encErr := auth.NewEncryptor(cfg.EncryptionKey); encErr == nil {
			encryptorReady = true
			platformEncryptor = e
		}
	}
	if secErr := config.ValidateProductionSecurity(cfg, encryptorReady); secErr != nil {
		// Only returns non-nil in production mode.
		log.Error("production security config invalid; refusing to start", "error", secErr)
		os.Exit(1)
	}
	// dev-keys-default-and-silent: the published chart sentinels are just as
	// forgeable in development, so report them on every boot in every env. The
	// worker decrypts the same credential columns the server does.
	observability.ReportInsecureDevKeys(log, config.DevSentinelsInUse(cfg))

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
	// C-01: fail fast on a corrupt schema_migrations row set (dirty=true or
	// multi-row drift), same guard the server runs in NewApp. A worker that
	// keeps sweeping against an indeterminate schema hides the .247-class
	// incident instead of CrashLooping on it.
	if shErr := database.SchemaHealth(context.Background()); shErr != nil {
		log.Error("schema health check failed; refusing to start", "error", shErr)
		os.Exit(1)
	}
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

	// Enqueuer for tasks that fan out follow-up work (e.g. the alert
	// evaluator handing notification:send tasks to the notification
	// dispatcher). This is the worker process that runs HandleAlertEvaluation,
	// so it must be able to enqueue.
	runtimeRedisOpt, redisOptErr := asynq.ParseRedisURI(cfg.RedisURL)
	if redisOptErr != nil {
		log.Error("failed to parse redis uri for runtime enqueuer", "error", redisOptErr)
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
		eventBus.AttachRedis(client, events.DefaultRedisChannel, log)
	}
	backupQueries := sqlc.New(database.Pool())
	backupExecutor := handler.NewAdminDrillHandler(backupQueries)
	backupExecutor.SetEncryptor(platformEncryptor)
	backupExecutor.SetBackupRuntime(os.Getenv("MANAGEMENT_BACKUP_IMAGE"), os.Getenv("MANAGEMENT_BACKUP_SERVICE_ACCOUNT"))
	if restCfg, kErr := rest.InClusterConfig(); kErr == nil {
		if client, clientErr := kubernetes.NewForConfig(restCfg); clientErr == nil {
			backupExecutor.SetKubernetes(client, os.Getenv("POD_NAMESPACE"), os.Getenv("RELEASE_NAME"))
		}
	}
	coreRuntime := tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries:                       sqlc.New(database.Pool()),
		Log:                           log,
		AgentImageRepo:                cfg.AgentImageRepository,
		AgentImageTag:                 cfg.AgentImageTag,
		SystemArtifactURL:             cfg.DeliveryFluxDistributionRepository,
		SystemArtifactDigest:          cfg.DeliveryFluxDistributionDigest,
		SystemOIDCIssuer:              cfg.DeliveryFluxDistributionOIDCIssuer,
		SystemOIDCIdentity:            cfg.DeliveryFluxDistributionCertificateIdentity,
		PlatformName:                  "Astronomer",
		AuditLogRetentionMonths:       cfg.AuditLogRetentionMonths,
		ClusterTombstoneRetentionDays: cfg.ClusterTombstoneRetentionDays,
		RegistrationTokenTTLHours:     cfg.RegistrationTokenTTLHours,
		Leader:                        leader.New(database.Pool(), log),
		Enqueuer:                      runtimeEnqueuer,
		Bus:                           eventBus,
		CatalogDecryptor:              tasks.CatalogDecryptorFor(platformEncryptor),
		MonitoringCipher:              tasks.MonitoringCipherFor(platformEncryptor),
		ManagementBackup:              backupExecutor,
	}}
	allowlistQueries := sqlc.New(database.Pool())
	var allowlistMaterializer allowlistproviders.CloudCredentialMaterializer
	if platformEncryptor != nil {
		materializer, materializerErr := allowlistproviders.NewSQLCredentialMaterializer(allowlistQueries, platformEncryptor)
		if materializerErr != nil {
			log.Error("failed to configure API-server allow-list credential materializer", "error", materializerErr)
			os.Exit(1)
		}
		allowlistMaterializer = materializer
	} else {
		log.Warn("API-server allow-list cloud writes will fail closed: encryption key is unavailable")
	}
	allowlistRegistry := allowlistproviders.NewRegistry()
	allowlistRegistry.Register(allowlistproviders.NewEKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewGKEProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewAKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewDOKSProvider(allowlistMaterializer))
	allowlistRegistry.Register(allowlistproviders.NewSelfManagedProvider())
	allowlistRuntime := tasks.ApiserverAllowlistRuntime{Deps: tasks.ApiserverAllowlistReconcileDeps{
		Queries:       allowlistQueries,
		Registry:      allowlistRegistry,
		ClusterShaper: allowlistproviders.ClusterFromSQLC,
		AuditWriter:   allowlistQueries,
	}}
	deliveryQueries := sqlc.New(database.Pool())
	deliveryVerifier, deliveryVerifierErr := deliveryresolver.NewExecVerifier(cfg.DeliverySourceTrustDirectory)
	if deliveryVerifierErr != nil {
		log.Error("failed to configure delivery source signature verifier", "error", deliveryVerifierErr)
		os.Exit(1)
	}
	deliverySourcePolicies, deliverySourcePoliciesErr := deliveryresolver.NewStaticPolicyProvider(
		cfg.DeliverySourceAllowedPrivateHosts, cfg.DeliverySourceEgressCIDRs, cfg.DeliverySourceProxyURL,
	)
	if deliverySourcePoliciesErr != nil {
		log.Error("failed to configure delivery source network policy", "error", deliverySourcePoliciesErr)
		os.Exit(1)
	}
	deliverySourceService := deliveryresolver.New(deliveryVerifier)
	deliverySourceService.SetSSHAllowed(cfg.DeliverySourceAllowSSH)
	var deliveryDecryptor deliveryresolver.ByteDecryptor = unavailableDeliveryDecryptor{}
	if platformEncryptor != nil {
		deliveryDecryptor = platformEncryptor
	} else {
		log.Warn("delivery resolver has no encryption key; credentialed sources will be rejected")
	}
	deliverySourceWorker, deliverySourceWorkerErr := deliveryresolver.NewPostgresWorker(
		database.Pool(), deliverySourceService, deliveryDecryptor,
		deliverySourcePolicies, "",
	)
	if deliverySourceWorkerErr != nil {
		log.Error("failed to configure delivery source resolver", "error", deliverySourceWorkerErr)
		os.Exit(1)
	}
	deliverySourceWorker.SetLimits(deliveryresolver.Limits{
		MaxArtifactBytes:  cfg.DeliverySourceMaxArtifactBytes,
		MaxHelmChartBytes: cfg.DeliverySourceMaxHelmChartBytes,
	})
	if cfg.DeliverySourceCAFile != "" {
		caBundle, caErr := os.ReadFile(cfg.DeliverySourceCAFile)
		if caErr != nil || len(caBundle) == 0 || len(caBundle) > 1<<20 {
			log.Error("failed to load bounded delivery source CA bundle", "error", caErr)
			os.Exit(1)
		}
		deliverySourceWorker.SetBaseCABundle(caBundle)
		clear(caBundle)
	}
	deliveryReconciler, deliveryReconcilerErr := deliveryrollout.NewPostgresReconciler(
		database.Pool(), maintenance.NewEvaluator(deliveryQueries), "",
	)
	if deliveryReconcilerErr != nil {
		log.Error("failed to configure delivery rollout reconciler", "error", deliveryReconcilerErr)
		os.Exit(1)
	}
	deliverySystemReconciler, deliverySystemReconcilerErr := systemrollout.New(database.Pool())
	if deliverySystemReconcilerErr != nil {
		log.Error("failed to configure delivery system rollout reconciler", "error", deliverySystemReconcilerErr)
		os.Exit(1)
	}
	deliveryRuntime := tasks.DeliveryRuntime{
		SourceResolver:          deliverySourceWorker,
		RolloutReconciler:       deliveryReconciler,
		SystemRolloutReconciler: deliverySystemReconciler,
	}
	maintenanceRuntime := tasks.MaintenanceRuntime{
		AgentTokens: tasks.AgentTokenRotateDeps{Queries: sqlc.New(database.Pool())},
		PlaintextCredentials: tasks.PlaintextCredentialMigrationDeps{
			Queries: sqlc.New(database.Pool()), Encryptor: platformEncryptor,
		},
	}
	gitopsRuntime := tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
		Queries:    sqlc.New(database.Pool()),
		Enqueuer:   runtimeEnqueuer,
		TaskOutbox: sqlc.New(database.Pool()),
		Decryptor:  platformEncryptor,
		Log:        log,
	}}

	// Email dispatch (migration 047). The SMTP password is Fernet-encrypted,
	// so production startup validation refuses queue consumption unless the
	// encryptor, settings provider, and sender are all present.
	var emailDispatchDeps tasks.EmailDeps
	if platformEncryptor != nil {
		q := sqlc.New(database.Pool())
		provider := email.NewSQLSettingsProvider(q, platformEncryptor, 5*time.Second)
		sender := email.NewSender(provider, platformEncryptor, log)
		sender.SetBrandingProvider(email.NewPlatformConfigBrandingProvider(q, ""))
		emailDispatchDeps = tasks.EmailDeps{Queries: q, Sender: sender, Provider: provider}
	} else {
		log.Warn("email dispatch disabled: ASTRONOMER_ENCRYPTION_KEY is not set")
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
		log.Error("failed to configure Charlie queue terminal failure publisher")
		os.Exit(1)
	}
	charlieAlertPlanner, plannerErr := charlie.NewFindingAlertPlanner(database.Pool())
	if plannerErr != nil {
		log.Error("failed to configure Charlie alert reconciliation")
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
	w, werr := worker.NewWorker(cfg.RedisURL, log, standaloneRuntime, worker.NewTerminalFailureErrorHandler(queueTerminalPublisher, log))
	if werr != nil {
		log.Error("failed to start worker", "error", werr)
		os.Exit(1)
	}
	if err := worker.ValidateTaskRegistry(); err != nil {
		log.Error("invalid task ownership registry", "error", err)
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
			log.Error("worker task dependencies incomplete; refusing to consume queues", "error", err)
			os.Exit(1)
		}
		if err := tasks.ValidateStandaloneRuntime(standaloneRuntime.Features, coreRuntime, deliveryRuntime, dispatchRuntime, alertRuntime, maintenanceRuntime, allowlistRuntime, gitopsRuntime); err != nil {
			log.Error("worker runtime composition incomplete; refusing to consume queues", "error", err)
			os.Exit(1)
		}
	}
	w.RegisterHandlers()

	s, serr := worker.NewScheduler(cfg.RedisURL, log, worker.SchedulerFeatures{CRDOwnership: cfg.CRDEnabled})
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
	worker.StartQueueMetricsReporter(ctx, queueInspector, log)

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
		// C-02: the metrics listener also serves /healthz so the chart's
		// worker probe (httpGet /healthz:9090) has a real target. Redis
		// reachability via the asynq client is the meaningful liveness signal
		// for a queue consumer — if it can't reach Redis it can't process or
		// enqueue anything.
		if err := worker.StartMetricsServer(ctx, cfg.WorkerMetricsAddr, runtimeEnqueuer, log); err != nil {
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
	observability.WithEvent(log, "worker_started").Info("astronomer-worker started", "redis_endpoint", redisEndpoint)

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
