package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/notify"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func (c *productionComposition) initializeIntegrations(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	jwtManager := c.jwtManager
	encryptor := c.encryptor
	ssoManager := c.ssoManager
	bus := c.bus
	requester := c.requester
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	controlPlaneHandler := c.controlPlaneHandler
	authHandler := c.authHandler
	totpHandler := c.totpHandler
	ssoHandler := c.ssoHandler
	resourceHandler := handler.NewResourceHandlerWithQueries(queries, requester)
	resourceHandler.SetAuthorization(rbacEngine, rbacQuerier)
	resourceHandler.SetRunTx(sqlcMutationTxRunner[handler.ResourceSettingsMutationTx](database))
	resourceHandler.SetResourceOperationStore(queries)
	resourceHandler.SetResourceMutationRunTx(sqlcMutationTxRunner[handler.ResourceMutationTx](database))
	resourceHandler.SetNodeOperationStore(queries)
	resourceHandler.SetNodeMutationRunTx(sqlcMutationTxRunner[handler.NodeMutationTx](database))
	resourceHandler.SetNodeOperationReadAuthorizer(nodeOperationReadAuthorizer(rbacEngine, rbacQuerier))
	resourceHandler.SetUserRunTx(sqlcMutationTxRunner[handler.UserMutationTx](database))
	resourceHandler.SetEncryptor(encryptor)
	resourceHandler.SetSSOManager(ssoManager)
	resourceHandler.SetJWTManager(jwtManager)
	// Admin force-logout SLO clean-up (migration 054). The handler
	// enumerates the target user's sso_sessions rows and fires
	// best-effort back-channel end-session POSTs against each IdP
	// before deleting the rows. Wired unconditionally; the encryptor
	// gate inside the handler is what actually decides whether the
	// back-channel POST can fire.
	resourceHandler.SetSSOSessionStore(queries)
	resourceHandler.SetSSOBackchannelClient(handler.NewDefaultSSOBackchannelClient())
	// User delete cascades through *_role_bindings; signal the RBAC cache to
	// drop the per-user entry instead of waiting out the TTL.
	if cache := rbacQuerier.Cache(); cache != nil {
		resourceHandler.SetRBACCacheInvalidator(cache)
		// Group-sync (migration 042) mutates *_role_bindings under the
		// hood on every SSO login + every admin re-sync; the same
		// per-user cache invalidator handles both paths so the next
		// authenticated request reflects the post-sync state.
		if ssoHandler != nil {
			ssoHandler.SetRBACCacheInvalidator(cache)
		}
	}
	platformCharts, chartRepoErr := handler.NewPlatformChartRepoHandler()
	if chartRepoErr != nil {
		return chartRepoErr
	}

	// SMTP email (migration 047). Wired only when the encryptor is
	// available — the password column is Fernet-encrypted and a
	// missing key would mean we couldn't round-trip a saved password.
	// The Enqueuer is best-effort across the codebase: every hook
	// site calls EnqueueAndLog so a missing SMTP relay never breaks a
	// user-facing action.
	var (
		smtpHandler   *handler.SMTPHandler
		emailEnqueuer *email.Enqueuer
	)
	if encryptor != nil {
		settingsProvider := email.NewSQLSettingsProvider(queries, encryptor, 5*time.Second)
		brandingProvider := email.NewPlatformConfigBrandingProvider(queries, "")
		smtpHandler = handler.NewSMTPHandler(queries, encryptor, logger)
		smtpHandler.SetRunTx(sqlcMutationTxRunner[handler.SMTPMutationTx](database))
		smtpHandler.SetSettingsProvider(settingsProvider)
		smtpHandler.SetBrandingProvider(brandingProvider)
		smtpHandler.SetAuditWriter(queries)
		smtpHandler.SetBaselineOverrideChecker(handler.NewBaselineOverrideChecker(rbacEngine, rbacQuerier))
		emailEnqueuer = email.NewEnqueuer(queries, brandingProvider, logger)
		// Bridge to the notification_templates override layer
		// (migration 059). Without this the Enqueuer renders only the
		// embedded defaults; with it the operator's per-key override
		// (if present + enabled) takes effect at enqueue time.
		emailEnqueuer.SetOverrideLookup(func(ctx context.Context, key string) (email.Overrides, bool) {
			res, err := notify.Resolve(ctx, queries, key)
			if err != nil || !res.HasOverride {
				return email.Overrides{}, false
			}
			return email.Overrides{Subject: res.Subject, BodyText: res.Body}, true
		})
		notifier := &emailNotifierAdapter{e: emailEnqueuer}
		authHandler.SetEmailNotifier(notifier)
		if totpHandler != nil {
			totpHandler.SetEmailNotifier(notifier)
		}
		resourceHandler.SetEmailNotifier(notifier)
		controlPlaneHandler.SetEmailNotifier(notifier)
		authHandler.SetPasswordResetStore(queries)
	}

	// Outbound webhook subscriptions (migration 048). Wired only when
	// the encryptor is available — the HMAC signing secret is
	// Fernet-encrypted at rest. The Tap subscribes to the same in-memory
	// bus the SSE stream consumes; the dispatcher task drains pending
	// deliveries every 15s. Tap.Start runs further below alongside the
	// other reconciler goroutines once reconcileCtx is defined.
	var (
		webhookHandler *handler.WebhookHandler
		webhookTap     *webhook.Tap
	)
	if encryptor != nil {
		webhookHandler = handler.NewWebhookHandler(queries, encryptor, logger)
		webhookHandler.SetRunTx(sqlcMutationTxRunner[handler.WebhookMutationTx](database))
		webhookHandler.SetAuditWriter(queries)
		webhookHandler.SetBaselineOverrideChecker(handler.NewBaselineOverrideChecker(rbacEngine, rbacQuerier))
		webhookTap = webhook.NewTap(queries, bus, logger)
		webhookHandler.SetTap(webhookTap)
		// Bridge audit.Record → bus so audit.* events fan out into
		// webhook deliveries without every audit call site having to
		// know about webhooks.
		audit.SetBusPublisher(busPublisherAdapter{bus: bus})
	}

	// External SIEM forwarders (migration 055). Same gate as webhooks
	// (the auth blob is Fernet-encrypted) and parallel wiring: the bus
	// tap subscribes to the same events.Bus the SSE stream consumes,
	// and the dispatcher task drains the per-forwarder queue every 2s.
	// We start the tap further below once reconcileCtx is defined.
	var (
		siemHandler *handler.SIEMHandler
		siemTap     *siem.BusTap
	)
	if encryptor != nil {
		siemHandler = handler.NewSIEMHandler(queries, encryptor, logger)
		siemHandler.SetRunTx(sqlcMutationTxRunner[handler.SIEMMutationTx](database))
		siemHandler.SetAuditWriter(queries)
		siemHandler.SetEventBus(bus)
		siemTap = siem.NewBusTap(queries, bus, webhook.MatchFilters, logger)
		siemHandler.SetTap(siemTap)
	}

	// Migration 057: shared maintenance-window evaluator. One
	// evaluator backs both the admin handler (which invalidates on
	// writes) and every destructive mutation handler's gate. 30s TTL
	// keeps the per-mutation check cheap; default operator stance is
	// zero windows, so the steady-state cost is one cached read per
	// gated request.
	maintenanceEvaluator := maintenance.NewEvaluator(queries)
	// Warn-level startup audit for permitted-mode + empty-op-types
	// windows: those refuse ALL destructive ops outside the window,
	// which is the most dangerous configuration. The handler validation
	// won't reject this combination because it's a valid operator
	// choice, but it MUST be intentional.
	maintenanceStartupWarn(ctx, queries, logger)

	// Stream tickets must be validatable on ANY replica (the pod that mints the
	// ?ticket= and the pod nginx pins the WebSocket to are independently
	// load-balanced). Back them with Redis — already hard-required infra (asynq
	// + the tunnel locator use the same URL) — so a ticket is single-use
	// cluster-wide. Fall back to per-pod in-memory only if the URL won't parse
	// (single-replica dev), which correctly limits browser exec/logs/shell to
	// one replica there.
	streamTickets := auth.NewStreamTicketStore(time.Minute)
	if backend, terr := auth.NewRedisStreamTicketBackendFromURL(cfg.RedisURL); terr != nil {
		logger.Warn("stream tickets: redis backend unavailable, using per-pod in-memory store (multi-replica browser exec/logs/shell auth will fail ~50%)", "error", terr)
	} else {
		streamTickets = auth.NewStreamTicketStoreWithBackend(time.Minute, backend)
	}
	// CORR-R02: cross-pod event fan-out for SSE (webhook/SIEM taps skip Remote).
	if cfg.RedisURL != "" {
		if opt, rerr := asynq.ParseRedisURI(cfg.RedisURL); rerr == nil {
			if client, ok := opt.MakeRedisClient().(*redis.Client); ok && client != nil {
				bus.AttachRedis(client, events.DefaultRedisChannel, logger,
					events.WithRedisRelayQueueCapacity(cfg.EventRelayQueueCapacity))
				go bus.StartRedisRelay(ctx)
				logger.Info("events bus redis fan-out enabled",
					"channel", events.DefaultRedisChannel,
					"queue_capacity", bus.RelayStatus().Capacity)
			}
		}
	}
	streamTicketHandler := handler.NewStreamTicketHandler(streamTickets)
	streamTicketHandler.SetAuthorization(rbacEngine, rbacQuerier)
	settingsCache := handler.NewSettingsCache(queries, 30*time.Second)
	c.resourceHandler = resourceHandler
	c.platformCharts = platformCharts
	c.smtpHandler = smtpHandler
	c.emailEnqueuer = emailEnqueuer
	c.webhookHandler = webhookHandler
	c.webhookTap = webhookTap
	c.siemHandler = siemHandler
	c.siemTap = siemTap
	c.maintenanceEvaluator = maintenanceEvaluator
	c.streamTickets = streamTickets
	c.streamTicketHandler = streamTicketHandler
	c.settingsCache = settingsCache
	return nil
}
