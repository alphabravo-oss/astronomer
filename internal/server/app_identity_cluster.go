package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/jackc/pgx/v5"
)

func (c *productionComposition) initializeIdentityAndClusterHandlers(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	jwtManager := c.jwtManager
	encryptor := c.encryptor
	ssoManager := c.ssoManager
	bus := c.bus
	hub := c.hub
	requester := c.requester
	queue := c.queue
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	monitoringHandler := c.monitoringHandler
	workloadHandler := c.workloadHandler
	authHandler := handler.NewAuthHandlerWithTokens(queries, queries, jwtManager)
	authHandler.SetRunTx(sqlcMutationTxRunner[handler.AuthMutationTx](database))
	authHandler.SetPasswordRehasher(queries)
	authHandler.SetRoleBindings(queries)
	authHandler.SetAuditWriter(queries)
	authHandler.SetLogger(logger)
	// Auth hardening (migration 039): account lockout + JWT session revocation.
	authHandler.SetLockoutQuerier(queries)
	authHandler.SetRevocationQuerier(queries)
	authHandler.SetLockoutPolicy(cfg.LoginFailureThreshold, time.Duration(cfg.LockoutDurationMinutes)*time.Minute)
	// Session lifetime is independent of encryption, SSO, and TOTP. Wire the
	// runtime policy for every deployment so password login and refresh cannot
	// silently fall back to boot configuration when encrypted features are off.
	configureSessionTimeoutPolicy(authHandler, jwtManager, queries, logger)
	// Single sign-out (migration 054 / NIST 800-53 AC-12). Wired when
	// the encryptor is available: the stored upstream id_token is
	// Fernet-encrypted at rest, so without the key Logout has nothing
	// to decrypt. The post_logout_redirect_uri is derived from the
	// callback base URL — keeping it adjacent to the SSO redirect URI
	// means the same dashboard hostname is registered with the IdP for
	// both legs of the OIDC flow.
	authHandler.SetSSOSessionStore(queries)
	if encryptor != nil {
		authHandler.SetEncryptor(encryptor)
		callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
		authHandler.SetPostLogoutRedirectURL(callbackBase + "/auth/logout-done/")
	}

	// 2FA / TOTP (migration 043). The handler needs the Fernet
	// encryptor to wrap secrets at rest; without it we skip wiring so
	// /auth/totp/* returns 503 not_configured if invoked. The Login
	// gate degrades to the legacy password-only flow.
	var totpHandler *handler.TOTPHandler
	if encryptor != nil {
		totpHandler = handler.NewTOTPHandler(queries, queries, encryptor, jwtManager)
		totpHandler.SetIssuer(cfg.TOTPIssuer)
		totpHandler.SetAuditWriter(queries)
		totpHandler.SetLogger(logger)
		totpHandler.SetPasswordRehasher(queries)
		totpHandler.SetRequireAll(cfg.TOTPRequire)
		authHandler.SetTOTPGate(totpHandler)
		authHandler.SetTOTPRequireAll(cfg.TOTPRequire)
		// Runtime MFA-enforcement policy: read the admin-toggleable
		// `totp.required` platform setting on every login so flipping it
		// (via the settings API or a compliance baseline) takes effect
		// without a redeploy. OR'd with the static cfg.TOTPRequire knob
		// inside the handler.
		authHandler.SetTOTPPolicy(func(ctx context.Context) bool {
			row, err := queries.GetPlatformSetting(ctx, "totp.required")
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(row.Value) == 0) {
				return false // setting never written -> documented default
			}
			if err != nil {
				logger.Error("totp.required policy read failed; failing closed (enforcing MFA)", "error", err)
				return true // transient/unknown DB error -> enforce, do not bypass
			}
			var v bool
			if json.Unmarshal(row.Value, &v) != nil {
				return true // unparseable value -> enforce rather than silently disable
			}
			return v
		})
	}
	// Wire the same revocation + cutoff backend into the JWT validator
	// so the auth middleware enforces the deny-list on every authenticated
	// request — without it, Logout would write a row no validator ever
	// consults.
	jwtManager.SetRevocationChecker(handler.NewJWTRevocationChecker(queries))

	var ssoHandler *handler.SSOHandler
	if ssoManager != nil {
		ssoHandler = handler.NewSSOHandler(ssoManager, queries, jwtManager, "/")
		// SLO persistence (migration 054). Wire both the writer + the
		// encryptor so the Callback can store Fernet-encrypted upstream
		// id_tokens onto the sso_sessions row keyed by the access JWT's
		// JTI. Without either, the Callback silently skips persistence
		// and Logout degrades to "JWT revoked locally only".
		ssoHandler.SetSSOSessionWriter(queries)
		if encryptor != nil {
			ssoHandler.SetEncryptor(encryptor)
		}
	}

	// Phase B4 — Dex shim handler. Always wire it (even pre-encryption-key)
	// so the connector wizard is browseable at /auth/dex/connector-types/;
	// secret round-trips silently no-op when the encryptor is nil and the
	// /apply endpoint short-circuits with a 503 when the K8s requester is
	// unavailable.
	dexHandler := handler.NewDexHandler(queries)
	dexHandler.SetRunTx(sqlcMutationTxRunner[handler.DexMutationTx](database))
	dexHandler.SetEncryptor(encryptor)
	dexHandler.SetK8sRequester(requester)
	dexHandler.SetLogger(logger)

	clusterHandler := handler.NewClusterHandler(queries)
	clusterHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterMutationTx](database))
	// MUST be set: the /clusters/ collection gate admits cluster-scoped callers
	// and this is what filters their page down to their own clusters.
	clusterHandler.SetAuthorization(rbacEngine, rbacQuerier)
	clusterHandler.SetEncryptor(encryptor)
	clusterHandler.SetAgentDisconnector(hub)
	clusterHandler.SetAgentImage(cfg.AgentImageRepository, cfg.AgentImageTag)
	clusterHandler.SetDeliverySystemBootstrap(
		cfg.DeliveryFluxDistributionRepository,
		cfg.DeliveryFluxDistributionDigest,
		cfg.DeliveryFluxDistributionOIDCIssuer,
		cfg.DeliveryFluxDistributionCertificateIdentity,
	)
	clusterHandler.SetRegistrationTokenTTL(time.Duration(cfg.RegistrationTokenTTLHours) * time.Hour)
	// HMAC key for short-TTL signed manifest-download URLs. Falls back to
	// the JWT signing secret when a dedicated one isn't configured so a
	// single-secret install still gets signed URLs.
	manifestSecret := strings.TrimSpace(cfg.ManifestSigningSecret)
	if manifestSecret == "" {
		manifestSecret = cfg.SecretKey
	}
	clusterHandler.SetManifestSigningSecret(manifestSecret)
	// Fan cluster.* lifecycle events out to SSE subscribers on Create / Update
	// / Delete. The bus implements the EventPublisher interface naturally.
	clusterHandler.SetEventPublisher(busPublisherAdapter{bus: bus})
	clusterHandler.SetGrafanaFolderReconciler(monitoringHandler)
	// Wizard handler (migration 078 / sprint 22). The handler owns the
	// phase-machine service; we hand a reference to the cluster handler
	// (so Create writes the first two step rows), to the tunnel hub (so
	// the first heartbeat advances awaiting_agent → connected), and to
	// the cluster_template:apply task wiring below.
	clusterRegistrationHandler := handler.NewClusterRegistrationHandler(queries, bus)
	clusterRegistrationHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterRegistrationMutationTx](database))
	clusterRegistrationHandler.Service().SetMetricsHook(observability.NewRegistrationMetricsHook())
	clusterHandler.SetRegistrationService(clusterRegistrationHandler.Service())
	if hub != nil {
		hub.SetRegistrationAdvancer(clusterRegistrationHandler.Service())
	}
	// Wire the asynq client into the DELETE handler so the cluster
	// decommission reconciler fires immediately on remove-cluster click.
	// The periodic sweep is the safety net when redis is briefly down.
	clusterHandler.SetDecommissionQueue(queue)
	clusterHandler.SetTaskOutbox(queries)
	// Wire metrics: tunnel requester for remote clusters, in-cluster clients
	// for the local cluster. Both are nil-safe; missing deps fall back to zero.
	clusterHandler.SetMetricsRequester(requester)
	clusterHandler.SetDirectKubeconfigRequester(requester)
	// localK8s and localNamespace are reused below to construct the support
	// bundle handler; SetMetricsLocalClient / SetKubernetesClient consume
	// localK8s too.
	localK8s, localMetrics, localDynamic := inClusterClients()
	if localK8s != nil {
		clusterHandler.SetMetricsLocalClient(localK8s, localMetrics)
	}
	localNamespace := detectReleaseNamespace()
	localReleaseName := strings.TrimSpace(os.Getenv("RELEASE_NAME"))
	if localReleaseName == "" {
		localReleaseName = "astronomer"
	}
	localChartVersion := strings.TrimSpace(os.Getenv("CHART_VERSION"))
	charlieFeatures := charlieLiveFeatures{queries: queries}
	var charlieOnboardingHandler *handler.CharlieOnboardingHandler
	var charlieAgentRuntime *charlie.KubernetesRuntimeActivator
	var charlieHelm charlie.HelmReleaser
	if localK8s != nil && encryptor != nil {
		secretWriter, err := charlie.NewKubernetesAgentSecretWriter(localK8s, "astronomer-charlie", []byte(cfg.SecretKey))
		if err != nil {
			charlie.LogOperationalFailure(context.Background(), logger, "bootstrap.secret_writer_unavailable", "")
		} else {
			charlieHelm = charlie.NewInClusterHelmReleaser("astronomer-charlie")
			runtime, runtimeErr := charlie.NewKubernetesRuntimeActivator(localK8s, localNamespace, charlieHelm)
			if runtimeErr != nil {
				charlie.LogOperationalFailure(context.Background(), logger, "bootstrap.runtime_activator_unavailable", "")
			} else {
				charlieAgentRuntime = runtime
			}
			charlieOnboardingHandler = handler.NewCharlieOnboardingHandler(&charlie.OnboardingConsumer{
				Store:            charlie.PGOnboardingTransactionStore{Pool: database.Pool()},
				Secrets:          secretWriter,
				Encryptor:        encryptor,
				BridgeServerDNS:  "charlie-agent-bridge.astronomer-charlie.svc",
				MCPServerDNS:     "astronomer-charlie-mcp." + localNamespace + ".svc",
				ProductNamespace: localNamespace,
				Runtime:          runtime,
				Auditor:          charlie.NewDBLifecycleAuditor(queries),
			})
		}
	}

	// Share the same metrics provider with the workload handler so per-node
	// CPU/memory usage on the node-detail page comes from the same fetch (and
	// the same cache) as the dashboard cluster card.
	workloadHandler.SetMetricsProvider(clusterHandler.MetricsProvider())
	c.authHandler = authHandler
	c.totpHandler = totpHandler
	c.ssoHandler = ssoHandler
	c.dexHandler = dexHandler
	c.clusterHandler = clusterHandler
	c.clusterRegistrationHandler = clusterRegistrationHandler
	c.localK8s = localK8s
	c.localMetrics = localMetrics
	c.localDynamic = localDynamic
	c.localNamespace = localNamespace
	c.localReleaseName = localReleaseName
	c.localChartVersion = localChartVersion
	c.charlieFeatures = charlieFeatures
	c.charlieOnboardingHandler = charlieOnboardingHandler
	c.charlieAgentRuntime = charlieAgentRuntime
	c.charlieHelm = charlieHelm
	return nil
}
