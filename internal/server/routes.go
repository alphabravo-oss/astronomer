package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/handler/remoteproxy"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// RouterDependencies contains the optional dependencies used to register API routes.
type RouterDependencies struct {
	JWT            *iauth.JWTManager
	Encryptor      *iauth.Encryptor
	AuthQueries    appmiddleware.TokenUserQuerier
	PlatformHealth *handler.PlatformHealthHandler
	AdminQueues    *handler.AdminQueuesHandler
	AdminDrill     *handler.AdminDrillHandler
	// ManagementLogs is the read-side complement of the chart-side
	// Fluent Bit DaemonSet — GET /api/v1/admin/management-logs/.
	// Superuser-gated inside the handler. Nil-safe: omitted from the
	// router when the in-cluster k8s client / namespace pair isn't
	// wired (laptop dev, test fakes).
	ManagementLogs *handler.ManagementLogsHandler
	// GroupMappings is the migration-042 admin CRUD over
	// identity_group_mappings plus the per-user re-sync endpoint.
	GroupMappings *handler.GroupMappingsHandler
	// SMTP owns /api/v1/admin/smtp/* and /api/v1/admin/emails/.
	// Wired by NewApp once the encryptor is available; routes are
	// omitted (cleanly) when SMTP is unwired (test fakes, pre-
	// encryption-key bootstrap).
	SMTP *handler.SMTPHandler
	// EmailEnqueuer is the application-wide handle for every hook
	// site (lockout, totp enroll/disable, recovery regenerate, api
	// token created, alert fired). Wired in NewApp.
	EmailEnqueuer *email.Enqueuer
	// Webhooks owns /api/v1/admin/webhooks/* + the deliveries audit
	// sub-routes (migration 048). Nil when the encryptor isn't wired
	// (the secret is Fernet-encrypted, so we degrade off cleanly).
	Webhooks *handler.WebhookHandler
	// SIEMForwarders owns /api/v1/admin/siem-forwarders/* — admin
	// CRUD + test + status for the external SIEM pipeline
	// (migration 055). Nil when the encryptor isn't wired.
	SIEMForwarders *handler.SIEMHandler
	// NotificationTemplates owns /api/v1/admin/notification-templates/*
	// (migration 059). The handler reads/writes overrides on top of the
	// built-in registry in internal/notify; the email + webhook
	// dispatchers consume the overrides via SetOverrideLookup.
	NotificationTemplates *handler.NotificationTemplateHandler
	Auth           *handler.AuthHandler
	// TOTP owns /api/v1/auth/totp/*. Pre-wired with Encryptor + JWT
	// + Queries by cmd/server before NewRouter runs. When nil (test
	// fakes, pre-encryption-key bootstrap), the TOTP routes are
	// omitted and Login continues to behave as the legacy password
	// flow.
	TOTP         *handler.TOTPHandler
	SSO          *handler.SSOHandler
	Clusters     *handler.ClusterHandler
	// ClusterTemplates owns /api/v1/cluster-templates/* (CRUD) and the
	// per-cluster /api/v1/clusters/{cluster_id}/template/* bind/apply
	// surface. Migration 049. Nil-safe: omitted from the router when
	// not wired (test harnesses, pre-migration boots).
	ClusterTemplates *handler.ClusterTemplateHandler
	// ClusterRegistries owns /api/v1/clusters/{cluster_id}/registries/*
	// — the multi-registry-per-cluster admin UX from migration 050. The
	// legacy single-row /registry/ endpoints on the cluster handler are
	// left in place for back-compat. Nil-safe.
	ClusterRegistries *handler.ClusterRegistriesHandler
	// ClusterSnapshots owns /api/v1/clusters/{cluster_id}/snapshots/*,
	// /snapshot-schedules/* and /velero-status/ — the per-cluster
	// Velero self-service surface from migration 052. Nil-safe.
	ClusterSnapshots *handler.ClusterSnapshotsHandler
	// FleetOperations owns /api/v1/fleet-operations/* — coordinated
	// multi-cluster actions (drain, tool upgrade, apply-template fanout)
	// with label-selector targeting and bounded blast radius
	// (migration 056). Nil-safe.
	FleetOperations *handler.FleetOperationHandler
	// NetworkPolicies owns /api/v1/admin/network-policy-templates/* (CRUD)
	// and /api/v1/clusters/{cluster_id}/network-policies/applications/*
	// (per-cluster apply/list/delete) — migration 068. Nil-safe.
	NetworkPolicies *handler.NetworkPolicyHandler
	Projects         *handler.ProjectHandler
	Tools            *handler.ToolHandler
	Audit        *handler.AuditHandler
	Alerting     *handler.AlertingHandler
	Anomaly      *handler.AnomalyHandler
	ArgoCD       *handler.ArgoCDHandler
	Backups      *handler.BackupHandler
	Catalog      *handler.CatalogHandler
	// ChartRatings owns /api/v1/charts/{chart_id}/ratings/* and
	// /api/v1/catalog/recommendations/{popular,similar}/* — the
	// migration-055 catalog rating surface. Nil-safe: routes are
	// only mounted when this field is non-nil so tests that don't
	// need the surface (and don't supply the querier) keep building.
	ChartRatings *handler.ChartRatingsHandler
	Logging      *handler.LoggingHandler
	Monitoring   *handler.MonitoringHandler
	ControlPlane *handler.ControlPlaneHandler
	Resources    *handler.ResourceHandler
	PlatformCharts *handler.PlatformChartRepoHandler
	RBAC         *handler.RBACHandler
	RBACQueries  appmiddleware.RBACQuerier
	RBACEngine   *rbac.Engine
	Security     *handler.SecurityHandler
	ServiceProxy *handler.ServiceProxyHandler
	Workloads    *handler.WorkloadHandler
	Hub          *tunnel.Hub
	Proxy        *tunnel.ProxyHandler
	Exec         *tunnel.ExecConsumer
	Logs         *tunnel.LogsConsumer
	// RemoteServer is the new remotedialer-based tunnel running alongside
	// Hub during the migration. Mounted at /api/v1/connect/{cluster_id}/.
	RemoteServer *tunnel2.RemoteServer
	// EventStream serves Server-Sent Events for live UI updates (cluster
	// connect/disconnect, heartbeats). Optional; nil-safe.
	EventStream *handler.EventStreamHandler
	// RemoteQueries is wired into the v2 demonstration handlers below — it's
	// the same *sqlc.Queries the rest of the app uses, exposed under a
	// distinct field so the migration code can resolve cluster rows directly
	// without depending on the cluster handler's private queries field.
	RemoteQueries *sqlc.Queries
	// ResourcesSearch fans a single resource-list query out across every
	// active cluster (Phase A3 of the Rancher-parity plan).
	ResourcesSearch *handler.ResourcesSearchHandler
	// Readyz exposes control-plane dependency readiness checks.
	Readyz http.Handler
	// DexConfig owns CRUD for Dex connectors / settings and renders the
	// running Dex instance's ConfigMap (Phase B4 of the Rancher-parity plan).
	DexConfig *handler.DexHandler
	// ArgoCDUIProxy reverse-proxies browser traffic to the in-cluster
	// argocd-server, gated by Astronomer's JWT (header) or
	// astronomer_session cookie. Mounted at top-level `/argocd/*`.
	ArgoCDUIProxy *handler.ArgoCDUIProxy
	// SupportBundle generates a downloadable zip of platform diagnostics.
	// Superuser-gated inside the handler itself.
	SupportBundle *handler.SupportBundleHandler
	// Compliance generates the SOC 2 / ISO 27001 audit-prep bundle
	// for any date range. Superuser-gated inside the handler.
	Compliance *handler.ComplianceHandler
	// PlatformSettings owns /api/v1/admin/settings/* + the two pre-auth
	// /api/v1/settings/{branding,banner}/ readers. Migration 046.
	PlatformSettings *handler.PlatformSettingsHandler
	// PlatformDefaultTemplate owns
	// /api/v1/admin/platform-settings/default-cluster-template/* (sprint
	// 074). Manages the operator-configured default cluster_template
	// the cluster Create handler auto-attaches to every newly-registered
	// cluster. Nil-safe.
	PlatformDefaultTemplate *handler.PlatformDefaultTemplateHandler
	// SettingsCache is the shared process-local cache for platform
	// settings, consumed by the FeatureGate middleware below. Optional
	// — when nil, every feature-gated route falls through as enabled.
	SettingsCache *handler.SettingsCache
	// Quotas owns /api/v1/admin/quota-plans/* CRUD, the
	// /admin/quota-usage/ fleet snapshot, and the per-tenant
	// /projects/{id}/quota/ + /auth/me/quota/ readers. Migration 051.
	// Nil-safe — when not wired the quota routes are omitted.
	Quotas *handler.QuotaHandler
	// CloudCredentials owns /api/v1/projects/{project_id}/cloud-credentials/*
	// + /api/v1/cloud-credentials/providers/ (migration 053). The handler
	// is nil-safe — when unwired the routes are omitted and the materialize
	// worker still runs whatever rows exist in the DB through the drift
	// sweep.
	CloudCredentials *handler.CloudCredentialHandler
	// Maintenance owns /api/v1/admin/maintenance-windows/* and the
	// /api/v1/admin/deferred-operations/* admin surface (migration 057).
	// The same migration's gate is wired into the destructive mutation
	// handlers (cluster.Delete, project.Delete, tool.{Install,Upgrade,
	// Uninstall}, catalog.{CreateInstallation,DeleteInstallation},
	// cluster_template.Apply) via SetMaintenanceGate setters.
	// Nil-safe: when unwired the routes are omitted and the gate
	// short-circuits to "not blocked" on every mutation.
	Maintenance *handler.MaintenanceHandler
	// Dashboards owns /api/v1/admin/dashboard-widgets/*,
	// /api/v1/admin/prometheus-datasources/*, and the per-scope
	// /api/v1/dashboards/{global,clusters/{id},projects/{id}}/
	// render endpoints (migration 058). Nil-safe.
	Dashboards *handler.DashboardHandler
	// GitOps owns /api/v1/admin/gitops-sources/* (migration 060). CRUD over
	// gitops_registration_sources plus the per-source /sync/, /preview/, and
	// /clusters/ subroutes. Nil-safe — when unwired the routes are omitted
	// and the periodic gitops:sync worker still runs whatever rows exist
	// in the DB.
	GitOps *handler.GitOpsHandler
	// ProjectCatalogs owns /api/v1/projects/{project_id}/catalogs/*
	// (migration 061). When nil the per-project BYO catalog routes are
	// omitted; the existing /catalog/* admin surface is untouched.
	ProjectCatalogs *handler.ProjectCatalogHandler
	// ReadAuditPolicies owns /api/v1/admin/read-audit-policies/* (migration
	// 063). Superuser-gated CRUD over the read_audit_policies table.
	// Nil-safe — when unwired the routes are omitted; the read-side audit
	// middleware also no-ops because its PolicyEvaluator returns the empty
	// list.
	ReadAuditPolicies *handler.ReadAuditPolicyHandler
	// ReadAuditEvaluator is the in-process PolicyEvaluator shared between
	// the middleware and the handler (so policy writes invalidate the
	// 30s cache). Nil-safe.
	ReadAuditEvaluator *appmiddleware.PolicyEvaluator
	// ImageVulns owns the sprint-062 image-vulnerability surface:
	// /api/v1/clusters/{cluster_id}/vulnerabilities/* + /api/v1/security
	// /vulnerabilities/*. The handler is nil-safe — when unwired the
	// routes are omitted and the rest of /security continues to work.
	ImageVulns *handler.ImageVulnHandler
	// ComplianceBaselines owns /api/v1/admin/compliance-baselines/* and
	// the /admin/compliance-baseline-applications/* history endpoints
	// (migration 064 — sprint 17). Apply / Revert require a pgxpool
	// transaction; the handler is nil-safe and routes are omitted when
	// the handler isn't wired.
	ComplianceBaselines *handler.ComplianceBaselinesHandler
	// KubectlShell owns /api/v1/clusters/{cluster_id}/shell/* and the
	// /api/v1/admin/shell-sessions/* superuser views (migration 065 /
	// sprint 17). Nil-safe: when unwired the routes are omitted and
	// the frontend Shell tab hides itself based on the missing
	// feature flag in /me.
	KubectlShell *handler.KubectlShellHandler
	// ClusterGroups owns /api/v1/cluster-groups/* — operator-defined folder
	// hierarchy over clusters (migration 066). Tree depth capped at 3
	// (root + 2 levels). Nil-safe: omitted from the router when not wired.
	ClusterGroups *handler.ClusterGroupHandler
	// Vault owns /api/v1/admin/vault-connections/* (superuser) +
	// /api/v1/projects/{id}/default-vault-connection/ (project RBAC).
	// Migration 067. Nil-safe: when not wired the routes are omitted.
	Vault *handler.VaultHandler
	// ClusterResources owns the sprint-069 read-only "what's installed"
	// surface: /clusters/{cluster_id}/{ingress-classes,gateway-classes,
	// network-policies,resource-quotas,limit-ranges}/. Nil-safe.
	ClusterResources *handler.ClusterResourcesHandler
	// ApiserverAllowlist owns /api/v1/clusters/{cluster_id}/apiserver-allowlist/*
	// (migration 070). The reconciler worker is the auto-correct path;
	// this handler is the CRUD + on-demand reconcile surface. Nil-safe.
	ApiserverAllowlist *handler.ApiserverAllowlistHandler
	// ServiceMesh owns /api/v1/clusters/{cluster_id}/service-mesh/*
	// (migration 071). Read-only detection + on-demand re-detect; nil-safe.
	ServiceMesh *handler.ServiceMeshHandler
}

// NewRouter builds and returns the Chi router with all routes and middleware.
func NewRouter(cfg *config.Config, deps RouterDependencies) chi.Router {
	r := chi.NewRouter()

	// Per-endpoint-class rate limiter. Bucket store
	// lives for the lifetime of the process; the janitor inside cleans up
	// idle buckets so the map doesn't leak (same pattern as the login
	// limiter). One limiter shared across all four classes so
	// chart-tuned configs apply uniformly.
	rateLimitCtx := context.Background()
	rateLimit := func(class appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler {
		return appmiddleware.APIRateLimit(rateLimitCtx, class, nil)
	}

	// Middleware
	r.Use(appmiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(appmiddleware.RequestLogger)
	r.Use(chimiddleware.Recoverer)
	r.Use(appmiddleware.Metrics)
	// Rename the otelhttp server span to use chi's route pattern
	// once routing has run. otelhttp.NewHandler wraps the router with
	// only the HTTP method as a placeholder span name; this middleware
	// upgrades it to "METHOD /api/v1/path/{id}" so traces aggregate by
	// route instead of by raw URL.
	r.Use(chiRoutePatternSpanName)
	// NOTE: chimiddleware.Timeout is applied per-group below — it MUST NOT be
	// applied globally because /api/v1/ws/... carries long-lived WebSocket
	// connections that would otherwise be force-closed at the timeout.

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health check (with and without trailing slash)
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version.Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})
	r.Get("/health", healthHandler)
	r.Get("/health/", healthHandler)
	if deps.PlatformCharts != nil {
		r.Get("/helm-repo/astronomer/index.yaml", deps.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer/"+deps.PlatformCharts.ArchiveName(), deps.PlatformCharts.ServeArchive)
		r.Get("/helm-repo/astronomer-v2/index.yaml", deps.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer-v2/"+deps.PlatformCharts.ArchiveName(), deps.PlatformCharts.ServeArchive)
	}
	if deps.Readyz != nil {
		r.Handle("/readyz", deps.Readyz)
		r.Handle("/readyz/", deps.Readyz)
	}

	// ArgoCD UI reverse proxy — top-level `/argocd/*` (NOT under `/api/v1`)
	// because the upstream argocd-server is configured with
	// `server.rootpath: /argocd` and emits its SPA's asset / API URLs under
	// that prefix. Mounting here means we forward the path unchanged.
	//
	// Auth: Astronomer JWT carried either as `Authorization: Bearer <jwt>`
	// (XHR from inside the SPA bundle) or as the `astronomer_session`
	// cookie (top-level browser navigation). On unauthenticated browser
	// nav we redirect to /auth/login?returnTo=... instead of a JSON 401.
	if deps.ArgoCDUIProxy != nil {
		argoAuth := func(next http.Handler) http.Handler { return next }
		if deps.JWT != nil {
			argoAuth = appmiddleware.AuthBrowserOrBearer(deps.JWT, deps.AuthQueries, "/auth/login")
		}
		r.With(argoAuth).Handle("/argocd", deps.ArgoCDUIProxy)
		r.With(argoAuth).Handle("/argocd/*", deps.ArgoCDUIProxy)
	}

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// REST-only timeout — does NOT apply to WS routes registered at the
		// top level (see r.Get("/api/v1/ws/...") below).
		r.Use(chimiddleware.Timeout(30 * time.Second))
		// /bootstrap/ and /bootstrap/complete/ were removed when the server
		// switched to the Rancher-style admin-on-first-boot model: the
		// startup hook in cmd/server/main.go (auth.EnsureBootstrapAdmin)
		// creates the admin user, and POST /auth/change-password/ handles
		// the forced first-login rotation. No HTTP endpoint is needed for
		// platform first-setup any more.

		if deps.Auth != nil {
			r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/login/", deps.Auth.Login)
			r.Post("/auth/refresh/", deps.Auth.Refresh)
			r.Post("/auth/logout/", deps.Auth.Logout)
			// SLO landing endpoint (migration 054). PUBLIC by design —
			// the IdP bounces here after tearing down its session and
			// the JWT was already revoked before the redirect was
			// issued. Sets a one-shot "logged_out" cookie + 303s to
			// /dashboard/login so the SPA renders the confirmation
			// page.
			r.Get("/auth/logout-done/", deps.Auth.LogoutDone)
			r.Get("/auth/logout-done", deps.Auth.LogoutDone)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/change-password/", deps.Auth.ChangePassword)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/me/", deps.Auth.CurrentUser)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/tokens/", deps.Auth.ListTokens)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/tokens/", deps.Auth.CreateToken)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/auth/tokens/{id}/", deps.Auth.RevokeToken)
			// Password reset (migration 047). Both endpoints are
			// PUBLIC — the request path is rate-limited under the
			// same /auth bucket as login (brute force on the email
			// enumeration vector → rate-limit), and the complete
			// path is gated by the emailed token.
			r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/password-reset/request/", deps.Auth.PasswordResetRequest)
			r.With(appmiddleware.LoginRateLimit(10, time.Minute)).Post("/auth/password-reset/complete/", deps.Auth.PasswordResetComplete)
		}

		// 2FA / TOTP routes (migration 043). Verify is PUBLIC — its proof
		// of identity is the challenge_token issued by Login. The other
		// endpoints require an active session (they're self-service
		// enrollment / management for the logged-in user).
		if deps.TOTP != nil {
			// Same rate-limit class as /auth/login — a brute-forcer
			// hitting verify with 1m TOTP codes would otherwise have
			// 10s windows of guess room per minute.
			r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/totp/verify/", deps.TOTP.Verify)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/enroll/start/", deps.TOTP.EnrollStart)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/enroll/confirm/", deps.TOTP.EnrollConfirm)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/disable/", deps.TOTP.Disable)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/totp/status/", deps.TOTP.Status)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/recovery-codes/regenerate/", deps.TOTP.RegenerateRecoveryCodes)
		}

		// SSO OAuth handshake. Both routes are public — Login redirects to the
		// provider, Callback validates state + exchanges the code for tokens.
		if deps.SSO != nil {
			r.Get("/auth/login/{provider}", deps.SSO.Login)
			r.Get("/auth/login/{provider}/", deps.SSO.Login)
			r.Get("/auth/callback/{provider}", deps.SSO.Callback)
			r.Get("/auth/callback/{provider}/", deps.SSO.Callback)
		}

		if deps.Resources != nil {
			r.Get("/activity/", deps.Resources.ListActivity)
			r.Route("/settings", func(r chi.Router) {
				r.Get("/general/", deps.Resources.GetGeneralSettings)
				r.Put("/general/", deps.Resources.UpdateGeneralSettings)
				r.Get("/sso/", deps.Resources.ListSSOProviders)
				r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/sso/", deps.Resources.CreateSSOProvider)
				r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/sso/{id}/", deps.Resources.DeleteSSOProvider)
				r.Get("/audit-logs/", deps.Resources.ListAuditLogs)
				if deps.Monitoring != nil {
					r.Get("/monitoring/backend/", deps.Monitoring.GetBackendConfig)
					r.Put("/monitoring/backend/", deps.Monitoring.UpdateBackendConfig)
					r.Get("/monitoring/operations/", deps.Monitoring.ListOperations)
					r.Get("/monitoring/operations/{id}/", deps.Monitoring.GetOperation)
					r.Post("/monitoring/operations/{id}/retry/", deps.Monitoring.RetryOperation)
					r.Get("/monitoring/thanos/status/", deps.Monitoring.GetSharedThanosStatus)
					r.Post("/monitoring/thanos/preview/", deps.Monitoring.PreviewSharedThanosStack)
					r.Post("/monitoring/thanos/install/", deps.Monitoring.InstallSharedThanosStack)
					r.Put("/monitoring/thanos/upgrade/", deps.Monitoring.UpgradeSharedThanosStack)
					r.Post("/monitoring/thanos/replace/", deps.Monitoring.ReplaceSharedThanosStack)
					r.Delete("/monitoring/thanos/uninstall/", deps.Monitoring.UninstallSharedThanosStack)
					r.Get("/monitoring/alertmanager/status/", deps.Monitoring.GetSharedAlertmanagerStatus)
					r.Post("/monitoring/alertmanager/preview/", deps.Monitoring.PreviewSharedAlertmanager)
					r.Post("/monitoring/alertmanager/install/", deps.Monitoring.InstallSharedAlertmanager)
					r.Put("/monitoring/alertmanager/upgrade/", deps.Monitoring.UpgradeSharedAlertmanager)
					r.Post("/monitoring/alertmanager/replace/", deps.Monitoring.ReplaceSharedAlertmanager)
					r.Delete("/monitoring/alertmanager/uninstall/", deps.Monitoring.UninstallSharedAlertmanager)
				}
			})
			r.Get("/users/", deps.Resources.ListUsers)
			r.Get("/users/{id}/", deps.Resources.GetUser)
		}

		if deps.Auth != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/settings/tokens/", deps.Auth.ListTokens)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/settings/tokens/", deps.Auth.CreateToken)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/settings/tokens/{id}/", deps.Auth.RevokeToken)
		}

		if deps.SupportBundle != nil {
			// Authenticated; the handler enforces superuser gating itself so
			// non-admins get a clean 403 rather than a generic permission
			// middleware rejection.
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/support-bundle/", deps.SupportBundle.Download)
		}

		// Compliance export bundle. Same auth pattern as
		// /support-bundle/ — gated on superuser inside the handler.
		// The /export/ endpoint picks streaming vs async based on
		// the audit-row count; /exports/{id}/ polls the async job.
		if deps.Compliance != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance/export/", deps.Compliance.Export)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance/exports/{id}/", deps.Compliance.GetExportStatus)
		}

		// Compliance baselines (migration 064 — sprint 17). Four preset
		// profiles (PCI-DSS / HIPAA / FedRAMP-Moderate / SOC2) the
		// operator can apply in one click. Superuser-gated inside the
		// handler. Apply / Revert require the *pgxpool.Pool — the
		// handler is nil when not wired and routes are simply omitted.
		if deps.ComplianceBaselines != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/", deps.ComplianceBaselines.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/active/", deps.ComplianceBaselines.Active)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/{id}/", deps.ComplianceBaselines.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/{id}/diff/", deps.ComplianceBaselines.Diff)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baselines/{id}/apply/", deps.ComplianceBaselines.Apply)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baseline-applications/", deps.ComplianceBaselines.History)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baseline-applications/{id}/revert/", deps.ComplianceBaselines.Revert)
		}

		// Key-rotation status — surfaces how many encryption / JWT signing
		// keys are loaded. KeyCount > 1 means a rotation is mid-flight (see
		// docs/secret-rotation-runbook.md). Authenticated; the handler
		// gates on superuser internally rather than via middleware so the
		// failure mode is a clean 403.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/key-status/", keyStatusHandler(deps))

		// Platform health rollup — single JSON document with cluster +
		// queue health for the top-of-dashboard banner. Authenticated;
		// no superuser gate since the dashboard banner is for everyone.
		if deps.PlatformHealth != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/platform/health-summary/", deps.PlatformHealth.Summary)
		}

		// Admin queue inspector — depths + DLQ contents for the asynq
		// queues, gated on superuser inside the handler. Used by the
		// Operations tab in the dashboard.
		if deps.AdminQueues != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/queues/", deps.AdminQueues.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/queues/{queue}/dlq/", deps.AdminQueues.DLQ)
		}

		// Backup-restore drill viewer — surfaces rows that the weekly
		// management-plane-restore-drill CronJob writes to
		// backup_drill_results. Gates on superuser inside the handler.
		// Used by the Operations tab + the
		// AstronomerBackupRestoreDrillStale alert's runbook.
		if deps.AdminDrill != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/backup-drill/", deps.AdminDrill.GetLatest)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/backup-drill/history/", deps.AdminDrill.ListHistory)
		}

		// Management-plane log tail (FEATURES-051226 T03) — the
		// dashboard's "show me what's happening right now" view.
		// The durable long-term path is the chart-side Fluent Bit
		// DaemonSet (deploy/chart/templates/management-logging-*.yaml).
		// Superuser-gated inside the handler.
		if deps.ManagementLogs != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/management-logs/", deps.ManagementLogs.Tail)
		}

		// Identity-group sync admin endpoints (migration 042). CRUD
		// over identity_group_mappings + admin-triggered re-sync.
		// Superuser-gated inside the handler — same pattern as the
		// other /admin/* routes — so the failure mode is a clean
		// 403 instead of a generic permission rejection.
		// SMTP admin endpoints (migration 047). Superuser-gated
		// inside the handler — same pattern as the other /admin/*
		// routes so the failure mode is a clean 403.
		if deps.SMTP != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/smtp/", deps.SMTP.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Put("/admin/smtp/", deps.SMTP.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/smtp/test/", deps.SMTP.Test)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/emails/", deps.SMTP.List)
		}

		// Outbound webhook subscriptions (migration 048). Superuser-gated
		// inside each handler so the failure mode is a clean 403. The
		// dispatcher worker is the actual sender; these endpoints only
		// manage the config + view the delivery history.
		if deps.Webhooks != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/", deps.Webhooks.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/", deps.Webhooks.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/{id}/", deps.Webhooks.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/webhooks/{id}/", deps.Webhooks.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/webhooks/{id}/", deps.Webhooks.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/test/", deps.Webhooks.Test)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/{id}/deliveries/", deps.Webhooks.Deliveries)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/deliveries/{delivery_id}/retry/", deps.Webhooks.RetryDelivery)
		}

		// External SIEM forwarders (migration 055). Superuser-gated
		// inside each handler; the dispatcher worker is the actual
		// sender and these endpoints only manage the config + read
		// status.
		if deps.SIEMForwarders != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/", deps.SIEMForwarders.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/", deps.SIEMForwarders.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/{id}/test/", deps.SIEMForwarders.Test)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/{id}/status/", deps.SIEMForwarders.Status)
		}

		// Notification-template overrides (migration 059). Superuser-
		// gated inside the handler. Same nil-safe wiring pattern as
		// the SMTP routes above — the handler is non-nil whenever the
		// notification_templates table exists (every prod boot post-
		// migration). The dispatchers consume overrides via the
		// OverrideLookup closures wired in cmd/server/main.go.
		if deps.NotificationTemplates != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/", deps.NotificationTemplates.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/{key}/", deps.NotificationTemplates.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/notification-templates/{key}/", deps.NotificationTemplates.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/notification-templates/{key}/", deps.NotificationTemplates.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/notification-templates/{key}/preview/", deps.NotificationTemplates.Preview)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/{key}/variables/", deps.NotificationTemplates.Variables)
		}

		// GitOps cluster registration sources (migration 060). Superuser-gated
		// inside each handler — non-admins get a clean 403. The periodic
		// gitops:sync worker is the actual reconciler; these endpoints
		// manage the source config + expose the manual-sync, dry-run
		// preview, and per-source managed-clusters readers.
		if deps.GitOps != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/", deps.GitOps.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/", deps.GitOps.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/", deps.GitOps.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/gitops-sources/{id}/", deps.GitOps.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/gitops-sources/{id}/", deps.GitOps.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/{id}/sync/", deps.GitOps.Sync)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/preview/", deps.GitOps.Preview)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/clusters/", deps.GitOps.ListClusters)
		}

		if deps.GroupMappings != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/group-mappings/", deps.GroupMappings.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/group-mappings/", deps.GroupMappings.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/group-mappings/{id}/", deps.GroupMappings.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/group-mappings/{id}/", deps.GroupMappings.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/resync-groups/", deps.GroupMappings.ResyncUser)
		}

		// Rancher-style global settings hub (migration 046).
		//
		// /admin/settings/* — superuser-gated inside the handler. The
		// branding + banner /settings/{namespace}/ readers are PUBLIC
		// because the login page renders the branding/banner BEFORE
		// the user has a session; the handler's PublicSubset method
		// gates the allowed namespace through an explicit allowlist so
		// telemetry.endpoint and feature.* never leak pre-auth.
		if deps.PlatformSettings != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/settings/", deps.PlatformSettings.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/settings/{key}/", deps.PlatformSettings.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/settings/{key}/", deps.PlatformSettings.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/settings/{key}/", deps.PlatformSettings.Delete)
			// Pre-auth readers. Must be registered on `r` (NOT on the
			// `authenticated` subrouter mounted below) so the chi
			// dispatch hits these before falling through to the auth
			// middleware. The handler's PublicSubset enforces the
			// namespace allowlist (`branding`, `banner`).
			r.Get("/settings/branding/", deps.PlatformSettings.PublicBranding)
			r.Get("/settings/banner/", deps.PlatformSettings.PublicBanner)
		}

		// Sprint 074 — platform-default cluster template. The
		// /admin/platform-settings/default-cluster-template/* surface
		// manages the auto-attach baseline (typically the seeded
		// "Platform baseline" — trivy-operator, kube-state-metrics,
		// node-exporter, fluent-bit, cert-manager) that the cluster
		// Create handler binds to every newly-registered cluster.
		// Superuser-gated inside the handler. Reapply takes a
		// {cluster_id} path param so an operator can back-fill an
		// existing cluster after changing the baseline.
		if deps.PlatformDefaultTemplate != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/platform-settings/default-cluster-template/reapply/{cluster_id}/", deps.PlatformDefaultTemplate.Reapply)
		}

		// Per-tenant resource quotas (migration 051). Plan CRUD +
		// fleet-usage snapshot are superuser-gated inside the handler
		// (same pattern as platform_settings + smtp). The per-tenant
		// /quota/ readers are wired below alongside the projects and
		// auth groups so they inherit those RBAC/auth chains.
		if deps.Quotas != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-plans/", deps.Quotas.ListPlans)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/quota-plans/", deps.Quotas.CreatePlan)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-plans/{name}/", deps.Quotas.GetPlan)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/quota-plans/{name}/", deps.Quotas.UpdatePlan)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/quota-plans/{name}/", deps.Quotas.DeletePlan)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-usage/", deps.Quotas.FleetUsage)
			// Per-tenant readers. Authentication is required; the
			// handler degrades gracefully when called for a user/
			// project that doesn't exist.
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/projects/{id}/quota/", deps.Quotas.ProjectQuota)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/me/quota/", deps.Quotas.MyQuota)
		}

		// Maintenance windows (migration 057). Operator-defined time
		// windows that gate destructive ops. The handler is superuser-
		// gated inside each method so the failure mode is a clean 403.
		// Writers invalidate the in-memory window cache so operator
		// changes apply immediately rather than after the 30s TTL.
		if deps.Maintenance != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/", deps.Maintenance.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/maintenance-windows/", deps.Maintenance.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/active/", deps.Maintenance.ListActive)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/{id}/", deps.Maintenance.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/maintenance-windows/{id}/", deps.Maintenance.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/maintenance-windows/{id}/", deps.Maintenance.Delete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/deferred-operations/", deps.Maintenance.ListDeferred)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/deferred-operations/{id}/cancel/", deps.Maintenance.CancelDeferred)
		}

		// Read-audit policies (migration 063). Superuser-gated CRUD over
		// the read_audit_policies table. Writers invalidate the in-
		// process PolicyEvaluator cache so operator changes apply
		// immediately rather than after the 30s TTL.
		if deps.ReadAuditPolicies != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/read-audit-policies/", deps.ReadAuditPolicies.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/read-audit-policies/", deps.ReadAuditPolicies.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Delete)
		}

		// Dashboard widgets (migration 058) — admin CRUD over
		// dashboard_widgets + prometheus_datasources. Superuser-gated
		// inside the handler. Writes carry the scope-write API-token
		// gate so a stolen read-only token can't reshape the dashboard
		// surface.
		if deps.Dashboards != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/dashboard-widgets/", deps.Dashboards.AdminList)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/dashboard-widgets/", deps.Dashboards.AdminCreate)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminGet)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminUpdate)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminDelete)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/prometheus-datasources/", deps.Dashboards.AdminListDatasources)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/", deps.Dashboards.AdminCreateDatasource)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/prometheus-datasources/{id}/", deps.Dashboards.AdminUpdateDatasource)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/prometheus-datasources/{id}/", deps.Dashboards.AdminDeleteDatasource)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/{id}/test/", deps.Dashboards.AdminTestDatasource)
		}

		authenticated := r
		if deps.JWT != nil {
			authenticated = chi.NewRouter()
			authenticated.Use(appmiddleware.RequireAuthWithQueries(deps.JWT, deps.AuthQueries))
			if deps.RemoteQueries != nil {
				authenticated.Use(appmiddleware.AuditLogWithWriter(slog.Default(), deps.RemoteQueries))
			}
			// Migration 063 — read-side audit. Wire AFTER auth so we
			// know the actor, and BEFORE per-route handlers so the
			// middleware sees every authenticated read. Nil-safe: when
			// the evaluator or DB writer is unwired the middleware is
			// simply not attached.
			if deps.ReadAuditEvaluator != nil && deps.RemoteQueries != nil {
				authenticated.Use(appmiddleware.ReadAudit(deps.ReadAuditEvaluator, deps.RemoteQueries))
			}
			r.Mount("/", authenticated)
		}

		registerProtectedRoutes(authenticated, cfg, deps, rateLimit)
	})

	if deps.Hub != nil {
		r.Get("/api/v1/ws/agent/tunnel/{cluster_id}/", deps.Hub.HandleWebSocket)
	}
	if deps.EventStream != nil {
		// SSE event stream — keeps a long-lived response open so register
		// outside the /api/v1 Timeout middleware group.
		r.Get("/api/v1/events/stream/", deps.EventStream.Stream)
	}
	if deps.RemoteServer != nil {
		// remotedialer hijacks the connection for a WS upgrade, so this MUST
		// be registered outside the /api/v1 group that applies a Timeout
		// middleware (the same reason the legacy ws/agent/tunnel route lives
		// out here).
		r.HandleFunc("/api/v1/connect/{cluster_id}/", deps.RemoteServer.ServeHTTP)
		// Demonstration endpoint — proves the new tunnel works end-to-end by
		// listing pods through a stock client-go clientset whose transport is
		// dialed through remotedialer. Real handlers will follow once the
		// migration is verified.
		r.Get("/api/v1/clusters/{id}/v2/pods/", remoteV2PodsHandler(deps))
	}
	if deps.Proxy != nil {
		// k8s passthrough is the most common loop-DoS target — any
		// authenticated user can fire arbitrary list calls. Token bucket
		// is sized so a normal UI burst (clicking through tabs) passes;
		// a runaway loop trips within ~20 requests.
		r.With(rateLimit(appmiddleware.ClassK8sProxy)).
			HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)
	}
	if deps.Exec != nil {
		// Exec session opens hold a goroutine + WS connection until the
		// shell exits. Limit new-session opens so a misbehaving caller
		// can't spawn arbitrary parallel terminals.
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", deps.Exec.HandleExec)
	}
	if deps.Logs != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", deps.Logs.HandleLogs)
	}

	// Kubectl shell WS handshake — session-aware redirect onto the
	// existing /api/v1/ws/exec/{cluster_id}/{ns}/{pod}/{container}/ relay.
	// The redirect target is what actually serves the WS handshake; this
	// just translates a session_id (which the frontend has) into the
	// pod+container coords the relay needs (which it does NOT have).
	if deps.KubectlShell != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/", deps.KubectlShell.HandleWS)
	}

	return r
}

func requireAuth(jwt *iauth.JWTManager, queries appmiddleware.TokenUserQuerier) func(http.Handler) http.Handler {
	if jwt == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return appmiddleware.RequireAuthWithQueries(jwt, queries)
}

// requireScope returns the API-token scope-enforcement middleware
// configured for `scope`. JWT sessions bypass the check; legacy
// (pre-044, empty-`scopes`) tokens are allowed through. See
// `APITokenScopeEnforce` for the full semantics.
func requireScope(scope string) func(http.Handler) http.Handler {
	return appmiddleware.APITokenScopeEnforce(scope)
}

// featureGate wraps the migration-046 FeatureGate middleware so it
// degrades cleanly when the SettingsCache is unwired (test fakes,
// pre-bootstrap). A nil cache returns a pass-through middleware —
// every feature is treated as enabled, matching the behaviour
// operators expect on a fresh install before any setting is changed.
func featureGate(key string, cache *handler.SettingsCache) func(http.Handler) http.Handler {
	if cache == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return appmiddleware.FeatureGate(key, cache)
}

func requirePermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return appmiddleware.RequirePermission(engine, querier, resource, verb)
}

func registerProtectedRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	// Migration-044 API-token scope-enforcement middleware. JWT
	// sessions and legacy (pre-044, empty-`scopes`) tokens bypass;
	// post-044 tokens must carry the matching scope or `admin`/`*`.
	writeClusters := requireScope(iauth.ScopeWriteClusters)
	writeProjects := requireScope(iauth.ScopeWriteProjects)
	writeRBAC := requireScope(iauth.ScopeWriteRBAC)

	if deps.Clusters != nil {
		r.Route("/clusters", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbList)).Get("/", deps.Clusters.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbCreate)).Post("/", deps.Clusters.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/", deps.Clusters.Get)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbDelete)).Delete("/{id}/", deps.Clusters.Delete)
			// Cluster decommission status — poll endpoint paired with the
			// DELETE handler's 202 Accepted response. Returns the latest
			// cluster_decommissions row's phase progress so the operator can
			// follow the reconciler.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/decommission/", deps.Clusters.GetDecommission)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/health/", deps.Clusters.GetHealth)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/conditions/", deps.Clusters.ListConditions)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/register/", deps.Clusters.GenerateRegistrationToken)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/registry/", deps.Clusters.GetRegistryConfig)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/registry/", deps.Clusters.UpdateRegistryConfig)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/registry/", deps.Clusters.DeleteRegistryConfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/manifest/", deps.Clusters.GetManifest)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/kubeconfig/", deps.Clusters.GetKubeconfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/{id}/generate-kubeconfig/", deps.Clusters.GenerateKubeconfig)
			// Underscore alias the Next.js frontend currently calls. Both shapes
			// route to the same handler so older callers keep working.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/{id}/generate_kubeconfig/", deps.Clusters.GenerateKubeconfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/kubeconfig-preview/", deps.Clusters.PreviewKubeconfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/", deps.Clusters.GetMetrics)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/summary/", deps.Clusters.GetMetricsSummary)
			if deps.Monitoring != nil {
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/config/", deps.Monitoring.GetClusterConfig)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/config/", deps.Monitoring.UpdateClusterConfig)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/stack/status/", deps.Monitoring.GetStackStatus)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/{id}/monitoring/stack/preview/", deps.Monitoring.PreviewStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/{id}/monitoring/stack/install/", deps.Monitoring.InstallStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/stack/upgrade/", deps.Monitoring.UpgradeStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Post("/{id}/monitoring/stack/replace/", deps.Monitoring.ReplaceStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/{id}/monitoring/stack/uninstall/", deps.Monitoring.UninstallStack)
			}
		})
	}

	// Cluster templates (migration 049). Two mount points:
	//   - /cluster-templates/* — CRUD on templates, gated on the new
	//     cluster_templates resource so superusers and a dedicated
	//     "template administrator" role can manage them without
	//     requiring full clusters:write.
	//   - /clusters/{cluster_id}/template/* — bind/apply/detach, gated on
	//     ResourceClusters + VerbUpdate (the operator who can edit a
	//     cluster can apply a template to it).
	if deps.ClusterTemplates != nil {
		r.Route("/cluster-templates", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbList)).Get("/", deps.ClusterTemplates.List)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbCreate)).Post("/", deps.ClusterTemplates.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbRead)).Get("/{id}/", deps.ClusterTemplates.Get)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterTemplates.Update)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterTemplates.Update)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterTemplates.Delete)
		})
		// Per-cluster bind / status / reapply / detach.
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/", deps.ClusterTemplates.Apply)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/template/", deps.ClusterTemplates.GetApplication)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/reapply/", deps.ClusterTemplates.Reapply)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/template/", deps.ClusterTemplates.Detach)
	}

	// Network policy templates (migration 068). Two mount points:
	//   - /admin/network-policy-templates/* — superuser CRUD over the
	//     library. Builtin rows are read-only at the handler level.
	//   - /clusters/{cluster_id}/network-policies/applications/* — per-
	//     cluster apply/list/delete, gated on ResourceClusters +
	//     VerbUpdate (same authority as editing the cluster).
	if deps.NetworkPolicies != nil {
		r.Route("/admin/network-policy-templates", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbList)).Get("/", deps.NetworkPolicies.ListTemplates)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbCreate)).Post("/", deps.NetworkPolicies.CreateTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbRead)).Get("/{id}/", deps.NetworkPolicies.GetTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbUpdate)).Put("/{id}/", deps.NetworkPolicies.UpdateTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbDelete)).Delete("/{id}/", deps.NetworkPolicies.DeleteTemplate)
		})
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/applications/", deps.NetworkPolicies.ListApplications)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/", deps.NetworkPolicies.CreateApplications)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/network-policies/applications/{id}/", deps.NetworkPolicies.DeleteApplication)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/{id}/reapply/", deps.NetworkPolicies.Reapply)
	}

	// Cluster registries (migration 050) — multi-registry-per-cluster admin
	// UX, mounted alongside the legacy /clusters/{id}/registry/ single-row
	// route. All endpoints are gated on the parent cluster's RBAC verb so
	// "admin who can edit cluster X" implicitly also manages X's registry
	// pull secrets.
	if deps.ClusterRegistries != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/", deps.ClusterRegistries.List)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/", deps.ClusterRegistries.Create)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Update)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Delete)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/{id}/test/", deps.ClusterRegistries.Test)
	}

	// Cluster snapshots (migration 052) — per-cluster Velero
	// self-service. List/get are clusters:read; mutating ops are
	// clusters:update because the operator who can edit a cluster is
	// the same one who can snapshot it. The velero-status pre-flight
	// is clusters:read so the install-Velero CTA renders for any
	// reader.
	if deps.ClusterSnapshots != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/", deps.ClusterSnapshots.ListSnapshots)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/", deps.ClusterSnapshots.CreateSnapshot)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterSnapshots.GetSnapshot)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterSnapshots.DeleteSnapshot)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/{id}/restore/", deps.ClusterSnapshots.CreateRestore)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterSnapshots.ListSchedules)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterSnapshots.CreateSchedule)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.GetSchedule)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.UpdateSchedule)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.DeleteSchedule)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/velero-status/", deps.ClusterSnapshots.VeleroStatus)
	}

	// Apiserver allow-list (migration 070).
	if deps.ApiserverAllowlist != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/", deps.ApiserverAllowlist.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/apiserver-allowlist/", deps.ApiserverAllowlist.Update)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/apiserver-allowlist/reconcile/", deps.ApiserverAllowlist.Reconcile)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/snapshots/", deps.ApiserverAllowlist.Snapshots)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/preview/", deps.ApiserverAllowlist.Preview)
	}

	// Fleet operations (migration 056).
	if deps.FleetOperations != nil {
		r.Route("/fleet-operations", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbList)).Get("/", deps.FleetOperations.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbCreate)).Post("/", deps.FleetOperations.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbRead)).Get("/{id}/", deps.FleetOperations.Get)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbRead)).Get("/{id}/targets/", deps.FleetOperations.ListTargets)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/pause/", deps.FleetOperations.Pause)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/resume/", deps.FleetOperations.Resume)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/abort/", deps.FleetOperations.Abort)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/retry-failed/", deps.FleetOperations.RetryFailed)
		})
	}

	// Service mesh tile (migration 071).
	if deps.ServiceMesh != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/", deps.ServiceMesh.Get)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/clusters/{cluster_id}/service-mesh/detect/", deps.ServiceMesh.Detect)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/mtls/", deps.ServiceMesh.MTLS)
	}

	if deps.Projects != nil {
		r.With(featureGate("feature.projects", deps.SettingsCache)).Route("/projects", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/", deps.Projects.List)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbCreate)).Post("/", deps.Projects.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/", deps.Projects.Get)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/{id}/", deps.Projects.Update)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/", deps.Projects.Update)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/{id}/", deps.Projects.Delete)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/add-namespace/", deps.Projects.AddNamespace)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/remove-namespace/", deps.Projects.RemoveNamespace)
			// Policy PATCH is a targeted update of just the PSS + ResourceQuota
			// columns; gated on projects:update so an admin who can edit the
			// project can also retune its security posture.
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/policy/", deps.Projects.UpdatePolicy)
			// Quota-usage is read-only and reflects current cluster state, so
			// projects:read is the right gate. Multi-cluster fanout surfaces
			// per-cluster partial failures the way resources_search does.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/quota-usage/", deps.Projects.QuotaUsage)
		})
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/clusters/{cluster_id}/projects/", deps.Projects.ListByCluster)
	}

	// Cloud credentials (migration 053). Project-scoped CRUD with the
	// /test/ endpoint that hits each provider's "validate this
	// credential" SDK call. The public /providers/ list is exposed
	// outside the project tree so the UI's "Add credential" wizard can
	// load the form-builder schema without a project id.
	if deps.CloudCredentials != nil {
		r.Get("/cloud-credentials/providers/", deps.CloudCredentials.ListProviders)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/", deps.CloudCredentials.List)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/", deps.CloudCredentials.Create)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Get)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Delete)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/{id}/test/", deps.CloudCredentials.Test)
	}

	// Dashboard widgets — public render endpoints (migration 058).
	// Three scopes; per-cluster and per-project are RBAC-gated on
	// the parent resource:read verb so an operator who can view a
	// cluster automatically gets its widgets (no separate
	// dashboards:read role needed). The global endpoint requires
	// auth only — anyone logged in can see the platform overview.
	if deps.Dashboards != nil {
		r.Get("/dashboards/global/", deps.Dashboards.RenderGlobal)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/dashboards/clusters/{id}/", deps.Dashboards.RenderCluster)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/dashboards/projects/{id}/", deps.Dashboards.RenderProject)
	}

	// Per-project ("BYO") Helm catalogs (migration 061). Gated by the
	// project-update permission — same shape as cloud-credentials above.
	if deps.ProjectCatalogs != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/", deps.ProjectCatalogs.List)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/", deps.ProjectCatalogs.Create)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/{catalog_id}/subscribe/", deps.ProjectCatalogs.Subscribe)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/catalogs/{catalog_id}/", deps.ProjectCatalogs.Delete)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/{catalog_id}/charts/", deps.ProjectCatalogs.ListCharts)
	}

	// In-browser kubectl shell (migration 065 / sprint 17). Every
	// cluster-scoped route is gated on clusters:update — opening a
	// privileged shell is a write action. The WS endpoint is mounted
	// on the same protected sub-router but skips the per-handler
	// rate limiter (it's a single long-lived connection, not a burst
	// vector — the underlying /api/v1/ws/exec/ ratelimiter still
	// applies on the redirect target). Admin views are superuser-only
	// inside the handler itself (matches admin_drill.go).
	if deps.KubectlShell != nil {
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/", deps.KubectlShell.Open)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/", deps.KubectlShell.List)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/", deps.KubectlShell.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/{id}/close/", deps.KubectlShell.Close)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/commands/", deps.KubectlShell.Commands)
		// Admin views — gated by superuser-check inside the handler.
		r.Get("/admin/shell-sessions/", deps.KubectlShell.AdminListAll)
		r.Get("/admin/shell-sessions/{id}/commands/", deps.KubectlShell.AdminCommands)
	}

	// Cluster groups (migration 066). All routes gated by clusters:update
	// because group admin is a clusters-admin concept; the LIST/GET reads
	// are also gated to keep the boundary tight (operators who can't
	// administer clusters shouldn't see the operator-defined folder
	// structure either).
	if deps.ClusterGroups != nil {
		r.Route("/cluster-groups", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/", deps.ClusterGroups.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/", deps.ClusterGroups.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/", deps.ClusterGroups.Get)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/", deps.ClusterGroups.Delete)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/clusters/", deps.ClusterGroups.ListClusters)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/move/", deps.ClusterGroups.MoveClusters)
		})
	}

	// Sprint 069 — CRD-mirror v2 cluster-detail read surface. The full
	// /network-policies/ path returns every mirrored NetworkPolicy
	// (managed + operator-created); the parallel sprint-068
	// /network-policies/applications/ path owns the astronomer-managed
	// subset.
	if deps.ClusterResources != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/ingress-classes/", deps.ClusterResources.ListIngressClasses)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/gateway-classes/", deps.ClusterResources.ListGatewayClasses)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/", deps.ClusterResources.ListNetworkPolicies)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/resource-quotas/", deps.ClusterResources.ListResourceQuotas)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/limit-ranges/", deps.ClusterResources.ListLimitRanges)
	}

	// Vault integration (migration 067).
	if deps.Vault != nil {
		r.Get("/admin/vault-connections/", deps.Vault.List)
		r.Post("/admin/vault-connections/", deps.Vault.Create)
		r.Get("/admin/vault-connections/{id}/", deps.Vault.Get)
		r.Put("/admin/vault-connections/{id}/", deps.Vault.Update)
		r.Delete("/admin/vault-connections/{id}/", deps.Vault.Delete)
		r.Post("/admin/vault-connections/{id}/test/", deps.Vault.Test)
		r.Post("/admin/vault-connections/{id}/health/", deps.Vault.Health)

		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{id}/default-vault-connection/", deps.Vault.GetProjectDefault)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{id}/default-vault-connection/", deps.Vault.PutProjectDefault)
	}

	if deps.Tools != nil {
		r.Route("/tools", func(r chi.Router) {
			r.Get("/controller/status/", deps.Tools.ControllerStatus)
			r.Get("/operations/", deps.Tools.ListOperations)
			r.Get("/operations/{id}/", deps.Tools.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Tools.RetryOperation)
			r.Get("/", deps.Tools.List)
			r.Get("/{id}/", deps.Tools.Get)
			r.Get("/slug/{slug}/", deps.Tools.GetBySlug)
			r.Get("/{slug:[^/]+}/", deps.Tools.GetBySlug)
			r.Post("/{slug}/preview/", deps.Tools.Preview)
			r.Post("/{slug}/install/", deps.Tools.Install)
			r.Put("/{slug}/upgrade/", deps.Tools.Upgrade)
			r.Delete("/{slug}/uninstall/", deps.Tools.Uninstall)
			r.Post("/{slug}/adopt/", deps.Tools.Adopt)
		})
		r.Get("/clusters/{cluster_id}/tools/status/", deps.Tools.ClusterStatus)
	}

	if deps.ControlPlane != nil {
		r.Get("/controllers/status/", deps.ControlPlane.Status)
		r.Get("/controllers/policy/", deps.ControlPlane.GetPolicy)
		r.Put("/controllers/policy/", deps.ControlPlane.UpdatePolicy)
		r.Get("/controllers/alerts/", deps.ControlPlane.ListAlerts)
		r.Post("/controllers/alerts/{id}/acknowledge/", deps.ControlPlane.AcknowledgeAlert)
		r.Get("/controllers/silences/", deps.ControlPlane.ListSilences)
		r.Post("/controllers/silences/", deps.ControlPlane.CreateSilence)
		r.Delete("/controllers/silences/{id}/", deps.ControlPlane.DeleteSilence)
	}

	if deps.RBAC != nil {
		r.Route("/rbac", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/", deps.RBAC.ListGlobalRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-roles/", deps.RBAC.CreateGlobalRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/{id}/", deps.RBAC.GetGlobalRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/global-roles/{id}/", deps.RBAC.UpdateGlobalRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-roles/{id}/", deps.RBAC.DeleteGlobalRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/", deps.RBAC.ListClusterRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-roles/", deps.RBAC.CreateClusterRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/{id}/", deps.RBAC.GetClusterRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/cluster-roles/{id}/", deps.RBAC.UpdateClusterRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-roles/{id}/", deps.RBAC.DeleteClusterRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/", deps.RBAC.ListProjectRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-roles/", deps.RBAC.CreateProjectRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/{id}/", deps.RBAC.GetProjectRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/project-roles/{id}/", deps.RBAC.UpdateProjectRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-roles/{id}/", deps.RBAC.DeleteProjectRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-bindings/", deps.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-bindings/", deps.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-bindings/{id}/", deps.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-bindings/", deps.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-bindings/", deps.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-bindings/{id}/", deps.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-bindings/", deps.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-bindings/", deps.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-bindings/{id}/", deps.RBAC.DeleteProjectRoleBinding)
			// Python-named binding path aliases (so both old and new clients work).
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-role-bindings/", deps.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-role-bindings/", deps.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-role-bindings/{id}/", deps.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-role-bindings/", deps.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-role-bindings/", deps.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-role-bindings/{id}/", deps.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-role-bindings/", deps.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-role-bindings/", deps.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-role-bindings/{id}/", deps.RBAC.DeleteProjectRoleBinding)
			// Current user's effective roles + permission check.
			r.Get("/my-roles/", deps.RBAC.MyRoles)
			r.Get("/my-roles/check/", deps.RBAC.CheckMyRole)
		})
	}

	if deps.Audit != nil {
		r.Route("/audit", func(r chi.Router) {
			r.Get("/", deps.Audit.List)
			r.Get("/export/", deps.Audit.Export)
			r.Get("/{id}/", deps.Audit.Get)
		})
	}

	if deps.Alerting != nil {
		r.Route("/alerting", func(r chi.Router) {
			r.Get("/channels/", deps.Alerting.ListChannels)
			r.Post("/channels/", deps.Alerting.CreateChannel)
			r.Get("/channels/{id}/", deps.Alerting.GetChannel)
			r.Put("/channels/{id}/", deps.Alerting.UpdateChannel)
			r.Delete("/channels/{id}/", deps.Alerting.DeleteChannel)
			r.Post("/channels/{id}/test/", deps.Alerting.TestChannel)
			r.Get("/rules/", deps.Alerting.ListRules)
			r.Post("/rules/", deps.Alerting.CreateRule)
			r.Get("/rules/{id}/", deps.Alerting.GetRule)
			r.Put("/rules/{id}/", deps.Alerting.UpdateRule)
			r.Delete("/rules/{id}/", deps.Alerting.DeleteRule)
			r.Post("/rules/{id}/enable/", deps.Alerting.EnableRule)
			r.Post("/rules/{id}/disable/", deps.Alerting.DisableRule)
			r.Get("/events/", deps.Alerting.ListEvents)
			r.Get("/events/{id}/", deps.Alerting.GetEvent)
			r.Post("/events/{id}/acknowledge/", deps.Alerting.AcknowledgeEvent)
			r.Post("/events/{id}/resolve/", deps.Alerting.ResolveEvent)
			r.Get("/silences/", deps.Alerting.ListSilences)
			r.Post("/silences/", deps.Alerting.CreateSilence)
			r.Delete("/silences/{id}/", deps.Alerting.DeleteSilence)
			r.Post("/silences/{id}/expire/", deps.Alerting.ExpireSilence)
		})
		// Python-named alerts/* alias paths for the frontend's expected URLs.
		r.Route("/alerts", func(r chi.Router) {
			r.Post("/rules/{id}/enable/", deps.Alerting.EnableRule)
			r.Post("/rules/{id}/disable/", deps.Alerting.DisableRule)
			r.Post("/silences/{id}/expire/", deps.Alerting.ExpireSilence)
		})
	}

	// Sprint 072 — read-only anomaly baseline inspection.
	if deps.Anomaly != nil {
		r.Route("/anomaly-baselines", func(r chi.Router) {
			r.Get("/", deps.Anomaly.List)
			r.Get("/{id}/", deps.Anomaly.Get)
		})
	}

	if deps.ArgoCD != nil {
		r.With(featureGate("feature.argocd", deps.SettingsCache)).Route("/argocd", func(r chi.Router) {
			r.Get("/controller/status/", deps.ArgoCD.ControllerStatus)
			r.Get("/operations/", deps.ArgoCD.ListOperations)
			r.Get("/operations/{id}/", deps.ArgoCD.GetOperation)
			r.Post("/operations/{id}/retry/", deps.ArgoCD.RetryOperation)
			r.Get("/instances/", deps.ArgoCD.ListInstances)
			r.Post("/instances/", deps.ArgoCD.CreateInstance)
			r.Get("/instances/{id}/", deps.ArgoCD.GetInstance)
			r.Put("/instances/{id}/", deps.ArgoCD.UpdateInstance)
			r.Delete("/instances/{id}/", deps.ArgoCD.DeleteInstance)
			r.Get("/instances/{id}/applications/", deps.ArgoCD.LiveApplications)
			r.Get("/instances/{id}/cached-applications/", deps.ArgoCD.ListAppsByInstance)
			r.Get("/instances/{id}/health/", deps.ArgoCD.InstanceHealth)
			r.Get("/applications/", deps.ArgoCD.ListAllApps)
			r.Get("/applications/{id}/", deps.ArgoCD.GetApp)
			r.Post("/applications/{id}/sync/", deps.ArgoCD.SyncApp)
			r.Get("/applications/{id}/history/", deps.ArgoCD.AppHistory)
			r.Get("/applications/{id}/manifests/", deps.ArgoCD.AppManifests)
			r.Post("/applications/{id}/refresh/", deps.ArgoCD.RefreshApp)
			r.Post("/instances/{id}/applications/{name}/sync/", deps.ArgoCD.SyncAppByName)

			// Phase B1 — ArgoCD lifecycle additions.
			// Application / AppProject / ApplicationSet CRUD, cluster
			// registration into upstream ArgoCD, and repo credential
			// management. All endpoints write through to the upstream
			// instance using the typed client in internal/handler/argocd.
			r.Post("/instances/{id}/applications/", deps.ArgoCD.CreateApplication)
			r.Patch("/instances/{id}/applications/{name}/", deps.ArgoCD.PatchApplication)
			r.Delete("/instances/{id}/applications/{name}/", deps.ArgoCD.DeleteApplication)
			r.Get("/instances/{id}/projects/", deps.ArgoCD.ListProjects)
			r.Post("/instances/{id}/projects/", deps.ArgoCD.CreateProject)
			r.Patch("/instances/{id}/projects/{name}/", deps.ArgoCD.PatchProject)
			r.Delete("/instances/{id}/projects/{name}/", deps.ArgoCD.DeleteProject)
			r.Get("/instances/{id}/applicationsets/", deps.ArgoCD.ListApplicationSets)
			r.Post("/instances/{id}/applicationsets/", deps.ArgoCD.CreateApplicationSet)
			r.Delete("/instances/{id}/applicationsets/{name}/", deps.ArgoCD.DeleteApplicationSet)
			r.Get("/instances/{id}/clusters/", deps.ArgoCD.ListManagedClusters)
			r.Post("/instances/{id}/clusters/{cluster_id}/register/", deps.ArgoCD.RegisterManagedCluster)
			r.Delete("/instances/{id}/clusters/{cluster_id}/register/", deps.ArgoCD.UnregisterManagedCluster)
			r.Get("/instances/{id}/repos/", deps.ArgoCD.ListRepos)
			r.Post("/instances/{id}/repos/", deps.ArgoCD.CreateRepo)
			r.Delete("/instances/{id}/repos/", deps.ArgoCD.DeleteRepo)
			r.Post("/instances/{id}/repos/test/", deps.ArgoCD.TestRepo)
		})
	}

	if deps.Backups != nil {
		r.With(featureGate("feature.backups", deps.SettingsCache)).Route("/backups", func(r chi.Router) {
			r.Get("/controller/status/", deps.Backups.ControllerStatus)
			r.Get("/", deps.Backups.ListBackups)
			r.Post("/", deps.Backups.CreateBackup)
			// Alias for the frontend: GET /backups/runs/ lists backup runs in
			// the same shape as GET /backups/. The frontend's "runs" tab calls
			// this URL; without the alias the chi router 404s the path.
			r.Get("/runs/", deps.Backups.ListBackups)
			r.Get("/{id}/", deps.Backups.GetBackup)
			r.Delete("/{id}/", deps.Backups.DeleteBackup)
			r.Post("/{id}/restore/", deps.Backups.CreateRestoreByBackup)
			r.Get("/restores/", deps.Backups.ListRestores)
			r.Get("/storage/", deps.Backups.ListStorageConfigs)
			r.Post("/storage/", deps.Backups.CreateStorageConfig)
			r.Get("/storage/{id}/", deps.Backups.GetStorageConfig)
			r.Put("/storage/{id}/", deps.Backups.UpdateStorageConfig)
			r.Delete("/storage/{id}/", deps.Backups.DeleteStorageConfig)
			r.Post("/storage/{id}/test/", deps.Backups.TestStorageConfig)
			r.Post("/storage/{id}/test-connection/", deps.Backups.TestStorageConfig)
			// Python-named alias paths (storage-configs/) so both clients work.
			r.Get("/storage-configs/", deps.Backups.ListStorageConfigs)
			r.Post("/storage-configs/", deps.Backups.CreateStorageConfig)
			r.Get("/storage-configs/{id}/", deps.Backups.GetStorageConfig)
			r.Put("/storage-configs/{id}/", deps.Backups.UpdateStorageConfig)
			r.Delete("/storage-configs/{id}/", deps.Backups.DeleteStorageConfig)
			r.Post("/storage-configs/{id}/test-connection/", deps.Backups.TestStorageConfig)
			r.Get("/schedules/", deps.Backups.ListSchedules)
			r.Post("/schedules/", deps.Backups.CreateSchedule)
			r.Get("/schedules/{id}/", deps.Backups.GetSchedule)
			r.Put("/schedules/{id}/", deps.Backups.UpdateSchedule)
			r.Delete("/schedules/{id}/", deps.Backups.DeleteSchedule)
			r.Post("/schedules/{id}/trigger-now/", deps.Backups.TriggerSchedule)
		})
	}

	if deps.Catalog != nil {
		r.With(featureGate("feature.catalog", deps.SettingsCache)).Route("/catalog", func(r chi.Router) {
			r.Get("/controller/status/", deps.Catalog.ControllerStatus)
			r.Get("/operations/", deps.Catalog.ListOperations)
			r.Get("/operations/{id}/", deps.Catalog.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Catalog.RetryOperation)
			r.Get("/repositories/", deps.Catalog.ListRepos)
			r.Post("/repositories/", deps.Catalog.CreateRepo)
			r.Get("/repositories/{id}/", deps.Catalog.GetRepo)
			r.Put("/repositories/{id}/", deps.Catalog.UpdateRepo)
			r.Delete("/repositories/{id}/", deps.Catalog.DeleteRepo)
			r.Post("/repositories/{id}/sync/", deps.Catalog.SyncRepo)
			r.Post("/repositories/{id}/test-connection/", deps.Catalog.TestRepoConnection)
			r.Get("/charts/", deps.Catalog.ListCharts)
			r.Get("/charts/{id}/", deps.Catalog.GetChart)
			r.Get("/charts/{id}/versions/", deps.Catalog.ListChartVersions)
			r.Get("/charts/{id}/readme/", deps.Catalog.GetChartReadme)
			r.Get("/charts/{id}/values/", deps.Catalog.GetChartValues)
			r.Get("/installed/", deps.Catalog.ListInstalledCharts)
			r.Post("/installed/", deps.Catalog.CreateInstalledChart)
			r.Put("/installed/{id}/upgrade/", deps.Catalog.UpgradeInstalledChart)
			r.Post("/installed/{id}/rollback/", deps.Catalog.RollbackInstalledChart)
			r.Delete("/installed/{id}/", deps.Catalog.DeleteInstalledChart)
			r.Get("/installed/{id}/values/", deps.Catalog.GetInstalledChartValues)
		})
	}

	if deps.ChartRatings != nil {
		// Per-chart rating CRUD lives under /charts/{chart_id}/ratings/
		// rather than nested inside the catalog block above. Reason:
		// the catalog block is feature-gated behind feature.catalog;
		// ratings should remain visible even when the platform admin
		// has hidden the catalog UX, so they can't be lost on a feature
		// flag toggle.
		r.Route("/charts/{chart_id}/ratings", func(r chi.Router) {
			r.Post("/", deps.ChartRatings.CreateRating)
			r.Get("/", deps.ChartRatings.ListRatings)
			r.Get("/aggregate/", deps.ChartRatings.GetAggregate)
			r.Get("/mine/", deps.ChartRatings.GetMyRating)
			r.Put("/{rating_id}/", deps.ChartRatings.UpdateRating)
			r.Delete("/{rating_id}/", deps.ChartRatings.DeleteRating)
		})
		r.Route("/catalog/recommendations", func(r chi.Router) {
			r.Get("/popular/", deps.ChartRatings.PopularRecommendations)
			r.Get("/similar/{chart_id}/", deps.ChartRatings.SimilarRecommendations)
		})
	}

	if deps.Logging != nil {
		r.Route("/logging", func(r chi.Router) {
			r.Get("/controller/status/", deps.Logging.ControllerStatus)
			r.Get("/operations/", deps.Logging.ListOperations)
			r.Get("/operations/{id}/", deps.Logging.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Logging.RetryOperation)
			r.Get("/outputs/", deps.Logging.ListOutputs)
			r.Post("/outputs/", deps.Logging.CreateOutput)
			r.Put("/outputs/{id}/", deps.Logging.UpdateOutput)
			r.Delete("/outputs/{id}/", deps.Logging.DeleteOutput)
			r.Post("/outputs/{id}/test/", deps.Logging.TestOutput)
			r.Post("/outputs/{id}/enable/", deps.Logging.EnableOutput)
			r.Post("/outputs/{id}/disable/", deps.Logging.DisableOutput)
			r.Post("/outputs/{id}/query/", deps.Logging.QueryOutput)
			r.Get("/pipelines/", deps.Logging.ListPipelines)
			r.Post("/pipelines/", deps.Logging.CreatePipeline)
			r.Put("/pipelines/{id}/", deps.Logging.UpdatePipeline)
			r.Delete("/pipelines/{id}/", deps.Logging.DeletePipeline)
			r.Post("/pipelines/{id}/enable/", deps.Logging.EnablePipeline)
			r.Post("/pipelines/{id}/disable/", deps.Logging.DisablePipeline)
			r.Get("/pipelines/{id}/fluentbit-config/", deps.Logging.FluentbitConfig)
		})
	}

	if deps.Monitoring != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/clusters/{cluster_id}/metrics/", deps.Monitoring.PrometheusQuery)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/", deps.Monitoring.ListMetrics)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/summary/", deps.Monitoring.ListMetrics)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/metrics/", deps.Monitoring.PrometheusQueryRange)
		// /api/v1/monitoring/endpoints/ ViewSet (CRUD on monitoring backends).
		r.With(featureGate("feature.monitoring", deps.SettingsCache)).Route("/monitoring", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbList)).Get("/endpoints/", deps.Monitoring.ListEndpoints)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/endpoints/", deps.Monitoring.CreateEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/endpoints/{id}/", deps.Monitoring.GetEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/endpoints/{id}/", deps.Monitoring.UpdateEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/endpoints/{id}/", deps.Monitoring.DeleteEndpoint)
			// Legacy Python paths preserved as aliases that proxy to the cluster-scoped handlers.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/metrics/query/{cluster_id}/", deps.Monitoring.LegacyMetricsQuery)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/cluster-overview/{cluster_id}/", deps.Monitoring.LegacyClusterOverview)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/workload/{cluster_id}/{namespace}/{workload}/", deps.Monitoring.LegacyWorkloadMetrics)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/node/{cluster_id}/{node}/", deps.Monitoring.LegacyNodeMetrics)
		})
	}

	if deps.Resources != nil {
		r.Get("/clusters/{cluster_id}/resources/{group}/{version}/{kind}/", deps.Resources.ListResources)
		r.Get("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumes|persistentvolumeclaims|storageclasses|gateways|httproutes|gatewayclasses|grpcroutes|tcproutes|udproutes|tlsroutes|referencegrants)}/", deps.Resources.ListNamedResources)
		r.Post("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/", deps.Resources.CreateNamedResource)
		r.Delete("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/{namespace}/{name}/", deps.Resources.DeleteNamedResource)
		r.Delete("/clusters/{cluster_id}/resources/{resource_type:(?:persistentvolumes)}/{name}/", deps.Resources.DeleteNamedResource)
		r.Get("/clusters/{cluster_id}/resources/generic/{resource_type}/", deps.Resources.ListGenericResources)
		r.Get("/settings/", deps.Resources.GetGeneralSettings)
		// Per-resource REST verbs (Python: /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/).
		r.Get("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.GetNamedResource)
		r.Put("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.UpdateNamedResource)
		r.Delete("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.DeleteNamedResourceREST)
		// Node action endpoints (cordon/uncordon/drain).
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/cordon/", deps.Resources.CordonNode)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/uncordon/", deps.Resources.UncordonNode)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/drain/", deps.Resources.DrainNode)
		// User CRUD (List/Get already wired above; add Create/Update/Delete + reset-password).
		r.Post("/users/", deps.Resources.CreateUser)
		r.Put("/users/{id}/", deps.Resources.UpdateUser)
		r.Patch("/users/{id}/", deps.Resources.UpdateUser)
		r.Delete("/users/{id}/", deps.Resources.DeleteUser)
		r.Post("/users/{id}/reset-password/", deps.Resources.ResetUserPassword)
		// Admin-only auth hardening endpoints (migration 039).
		//
		// Superuser gating lives inside the handler — same pattern as the
		// other /admin/* routes here (keyStatusHandler, AdminQueues etc.).
		// We deliberately keep the auth requirement on the wrapper so a
		// non-superuser hits a clean 403 instead of falling through.
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/unlock/", deps.Resources.UnlockUser)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/force-logout/", deps.Resources.ForceLogoutUser)
		// 2FA admin override. Superuser-only inside the handler.
		if deps.TOTP != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/disable-totp/", deps.TOTP.AdminForceDisable)
		}
	}

	if deps.Security != nil {
		r.With(featureGate("feature.security", deps.SettingsCache)).Route("/security", func(r chi.Router) {
			r.Get("/controller/status/", deps.Security.ControllerStatus)
			r.Get("/templates/", deps.Security.ListTemplates)
			r.Post("/templates/", deps.Security.CreateTemplate)
			r.Get("/templates/{id}/", deps.Security.GetTemplate)
			r.Put("/templates/{id}/", deps.Security.UpdateTemplate)
			r.Delete("/templates/{id}/", deps.Security.DeleteTemplate)
			r.Get("/policies/", deps.Security.ListPolicies)
			r.Post("/policies/", deps.Security.CreatePolicy)
			r.Post("/policies/{id}/apply/", deps.Security.ApplyPolicy)
			r.Delete("/policies/{id}/", deps.Security.DeletePolicy)
			r.Get("/scans/", deps.Security.ListAllScans)
			r.Post("/scans/", deps.Security.CreateScan)
		})
		r.Get("/clusters/{cluster_id}/security/policy/", deps.Security.GetPolicy)
		r.Get("/clusters/{cluster_id}/security/scans/", deps.Security.ListScans)
		r.Get("/clusters/{cluster_id}/security/scans/{id}/", deps.Security.GetScan)
	}

	// --- Sprint 062: image vulnerability scanning -------------------------
	// Cluster-scoped routes gate on cluster:read; the fleet rollup pair
	// gates on security:read. Cluster routes live OUTSIDE the `/security`
	// mount so the existing CIS-benchmark routes stay untouched. The fleet
	// routes are nested under `/security/vulnerabilities/` so they pair
	// naturally with the CIS surface in the dashboard.
	if deps.ImageVulns != nil {
		ivClusterRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		ivSecurityRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSecurity, rbac.VerbRead)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/summary/", deps.ImageVulns.ClusterSummary)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/images/", deps.ImageVulns.ClusterTopImages)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/reports/{id}/", deps.ImageVulns.ClusterReportDetail)
		r.With(ivClusterRead).Post("/clusters/{cluster_id}/vulnerabilities/rescan/", deps.ImageVulns.ClusterRescan)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/summary/", deps.ImageVulns.FleetSummary)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/top-clusters/", deps.ImageVulns.FleetTopClusters)
	}

	if deps.Workloads != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/controller/status/", deps.Workloads.ControllerStatus)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/", deps.Workloads.ListOperations)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/{id}/", deps.Workloads.GetOperation)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbUpdate)).Post("/workloads/operations/{id}/retry/", deps.Workloads.RetryOperation)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbList)).Get("/clusters/{cluster_id}/workloads/", deps.Workloads.List)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.Workloads.Get)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/pods/", deps.Workloads.ListWorkloadPods)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbScale)).Patch("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/scale/", deps.Workloads.Scale)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRestart)).Post("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/restart/", deps.Workloads.Restart)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbDelete)).Delete("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.Workloads.Delete)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/namespaces/", deps.Workloads.ListNamespaces)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/nodes/", deps.Workloads.ListNodes)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/nodes/{node_name}/", deps.Workloads.GetNode)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/events/", deps.Workloads.ListEvents)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbList)).Get("/clusters/{cluster_id}/pods/", deps.Workloads.ListPods)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbDelete)).Delete("/workloads/pods/{cluster_id}/{namespace}/{pod}/", deps.Workloads.DeletePod)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbLogs)).Get("/workloads/pods/{cluster_id}/{namespace}/{pod}/logs/", deps.Workloads.PodLogs)
	}

	if deps.ServiceProxy != nil {
		r.Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/", deps.ServiceProxy)
		r.Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*", deps.ServiceProxy)
	}

	// --- Phase A3: cross-cluster resource search ---------------------------
	// Single endpoint that fans a list query out across every active cluster
	// in parallel. The handler enforces a per-cluster timeout and concurrency
	// cap so a single slow cluster cannot block the whole response. The
	// response includes per-cluster errors + counts so the UI can surface
	// partial failures gracefully.
	if deps.ResourcesSearch != nil {
		// Cross-cluster fan-out — each call hits every connected tunnel.
		// Rate limit per-user so a runaway typeahead can't DoS the fleet.
		r.With(rateLimit(appmiddleware.ClassSearch)).
			Get("/resources/search/", deps.ResourcesSearch.Search)
	}

	// --- Phase B5: CIS scans via cis-operator ------------------------------
	// Mounts the CIS-specific routes layered over the existing security
	// handler. These routes live below the same `/security` prefix as the
	// pre-existing scan endpoints, but are registered here (instead of in
	// the main `if deps.Security != nil` block above) so this phase remains
	// a self-contained, append-only addition that's easy to audit and
	// revert. The handler's CreateScan method is unchanged in routing —
	// the *behavior* of POST /security/scans/ now also creates a
	// ClusterScan CR, but the route is the same.
	if deps.Security != nil {
		// Wire the optional CIS dependencies onto the existing handler. We
		// do this here (instead of touching server.go) because all of the
		// inputs are already available in `deps` and `cfg`. The handler
		// is nil-safe for any of these — when they're absent the legacy
		// (DB-only) code path remains intact.
		if deps.Hub != nil {
			deps.Security.SetK8sRequester(handler.NewTunnelK8sRequester(deps.Hub))
		}
		if deps.RemoteQueries != nil {
			deps.Security.SetClusterQuerier(deps.RemoteQueries)
			deps.Security.SetIngestPersister(deps.RemoteQueries)
		}
		// Optional asynq client wiring kept for parity with other handlers
		// — the in-process poller does the actual ingestion today, but a
		// queue connection is still useful for any future cross-process
		// triggers (e.g. webhook → enqueue).
		if cfg != nil && cfg.RedisURL != "" {
			if redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL); err == nil {
				deps.Security.SetIngestQueue(asynq.NewClient(redisOpt))
			}
		}
		secGate := featureGate("feature.security", deps.SettingsCache)
		r.With(secGate).Get("/security/profiles/", deps.Security.ListProfiles)
		r.With(secGate).Get("/security/scans/{id}/", deps.Security.GetScanFull)
		r.With(secGate).Get("/security/scans/{id}/report.csv", deps.Security.ExportScanCSV)
	}

	// Phase B4 — Dex routes
	// Lightweight CRUD over Dex connector + settings rows, plus an /apply
	// endpoint that renders the rows into a ConfigMap and PATCHes it into
	// the management cluster (Dex hot-reloads). RegisterAsSSO is the one-
	// click ergonomic helper that creates a `dex` row in sso_configurations
	// pointing at the configured issuer URL — A1's generic OIDC path then
	// takes over.
	if deps.DexConfig != nil {
		r.Route("/auth/dex", func(r chi.Router) {
			r.Get("/connector-types/", deps.DexConfig.ListConnectorTypes)
			r.Get("/connectors/", deps.DexConfig.ListConnectors)
			r.Post("/connectors/", deps.DexConfig.CreateConnector)
			r.Get("/connectors/{id}/", deps.DexConfig.GetConnector)
			r.Patch("/connectors/{id}/", deps.DexConfig.UpdateConnector)
			r.Delete("/connectors/{id}/", deps.DexConfig.DeleteConnector)
			r.Get("/settings/", deps.DexConfig.GetSettings)
			r.Put("/settings/", deps.DexConfig.UpdateSettings)
			r.Post("/apply/", deps.DexConfig.Apply)
			r.Post("/register-as-sso/", deps.DexConfig.RegisterAsSSO)
		})
	}
}

// remoteV2PodsHandler is the demonstration endpoint for the new
// remotedialer-based tunnel. It looks up the cluster row by id (so callers
// can use either cluster.id UUID or — if we later choose — a name lookup),
// builds a client-go clientset whose transport is dialed through the WS
// tunnel, and lists pods in the requested namespace.
//
// Returns 503 if the agent is not currently connected.
func remoteV2PodsHandler(deps RouterDependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID := chi.URLParam(r, "id")
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}

		client, err := remoteproxy.K8sClient(deps.RemoteServer, clusterID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		pods, err := client.CoreV1().Pods(namespace).List(r.Context(), metav1.ListOptions{})
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]map[string]any, 0, len(pods.Items))
		for _, p := range pods.Items {
			out = append(out, map[string]any{
				"name":      p.Name,
				"namespace": p.Namespace,
				"phase":     string(p.Status.Phase),
				"node":      p.Spec.NodeName,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster_id": clusterID,
			"namespace":  namespace,
			"count":      len(out),
			"pods":       out,
		})
	}
}

// keyStatusHandler returns the number of loaded encryption + JWT signing
// keys. The runbook (docs/secret-rotation-runbook.md) tells operators to
// poll this during a rotation to confirm the new key is in fact loaded and
// that the old key has been dropped at the end of the procedure.
//
// Auth: superuser only — the count itself is harmless, but the diagnostic
// is intended for the operator running the rotation, not the general user
// population.
func keyStatusHandler(deps RouterDependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := appmiddleware.GetAuthenticatedUser(r.Context())
		if !ok {
			handler.RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
			return
		}
		callerID, err := uuid.Parse(caller.ID)
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
			return
		}
		if deps.AuthQueries == nil {
			handler.RespondError(w, http.StatusInternalServerError, "internal_error", "User store not configured")
			return
		}
		dbUser, err := deps.AuthQueries.GetUserByID(r.Context(), callerID)
		if err != nil {
			handler.RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
			return
		}
		if !dbUser.IsSuperuser {
			handler.RespondError(w, http.StatusForbidden, "forbidden",
				"Key status requires superuser privileges")
			return
		}

		encKeys := 0
		if deps.Encryptor != nil {
			encKeys = deps.Encryptor.KeyCount()
		}
		jwtKeys := 0
		if deps.JWT != nil {
			jwtKeys = deps.JWT.KeyCount()
		}

		// Read-only superuser endpoint that exposes the live key-rotation
		// state — leave an explicit audit trail. The mutating-HTTP audit
		// middleware skips GET, so this trail wouldn't otherwise exist.
		handler.RecordAuditFromRequest(r, deps.AuthQueries, "admin.key_status.viewed",
			"platform", "", "key-status", map[string]any{
				"encryption_keys": encKeys,
				"jwt_keys":        jwtKeys,
			})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"encryption_keys": encKeys,
			"jwt_keys":        jwtKeys,
			"as_of":           time.Now().UTC().Format(time.RFC3339),
		})
	}
}
