package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
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
	JWT               *iauth.JWTManager
	Encryptor         *iauth.Encryptor
	AuthQueries       appmiddleware.TokenUserQuerier
	AuditWriter       any
	ArgoCDProxyTokens ArgoCDClusterProxyTokenQuerier
	PlatformHealth    *handler.PlatformHealthHandler
	AdminQueues       *handler.AdminQueuesHandler
	AdminTaskOutbox   *handler.AdminTaskOutboxHandler
	AdminDrill        *handler.AdminDrillHandler
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
	Auth                  *handler.AuthHandler
	// TOTP owns /api/v1/auth/totp/*. Pre-wired with Encryptor + JWT
	// + Queries by cmd/server before NewRouter runs. When nil (test
	// fakes, pre-encryption-key bootstrap), the TOTP routes are
	// omitted and Login continues to behave as the legacy password
	// flow.
	TOTP     *handler.TOTPHandler
	SSO      *handler.SSOHandler
	Clusters *handler.ClusterHandler
	// ClusterTemplates owns /api/v1/cluster-templates/* (CRUD) and the
	// per-cluster /api/v1/clusters/{cluster_id}/template/* bind/apply
	// surface. Migration 049. Nil-safe: omitted from the router when
	// not wired (test harnesses, pre-migration boots).
	ClusterTemplates *handler.ClusterTemplateHandler
	// ClusterRegistration owns /api/v1/clusters/{id}/registration/*
	// — the Rancher-style wizard endpoints from sprint 22 /
	// migration 078. Nil-safe.
	ClusterRegistration *handler.ClusterRegistrationHandler
	// ClusterRegistries owns /api/v1/clusters/{cluster_id}/registries/*
	// — the multi-registry-per-cluster admin UX from migration 050. The
	// legacy single-row /registry/ endpoints on the cluster handler are
	// left in place for back-compat. Nil-safe.
	ClusterRegistries *handler.ClusterRegistriesHandler
	// ClusterSnapshots owns /api/v1/clusters/{cluster_id}/snapshots/*,
	// /snapshot-schedules/* and /velero-status/ — the per-cluster
	// Velero self-service surface from migration 052. Nil-safe.
	ClusterSnapshots *handler.ClusterSnapshotsHandler
	// ControlPlaneSnapshots owns /api/v1/clusters/{cluster_id}/control-plane-snapshots/*
	// — the etcd/control-plane DR surface (migration 125). Nil unless
	// control_plane_snapshots_enabled is set, so the privileged-Job path is
	// unreachable by default.
	ControlPlaneSnapshots *handler.ControlPlaneSnapshotHandler
	// NativeAuthz consults native per-CRD RBAC rules on the k8s-proxy authz
	// hook (additive allow after a coarse deny). Nil unless native_rbac_enabled
	// is set, so the proxy authz path is byte-for-byte unchanged by default.
	NativeAuthz nativeAuthorizer
	// NativeRBAC serves the native-rule CRUD API (author/list/delete). Nil
	// unless native_rbac_enabled.
	NativeRBAC *handler.NativeRBACHandler
	// NamespaceScopedRBAC gates the namespace/project-scoped list gate on the
	// typed cluster resource routes. False = the routes use the standard
	// RequirePermission (unchanged behavior).
	NamespaceScopedRBAC bool
	// FleetOperations owns /api/v1/fleet-operations/* — coordinated
	// multi-cluster actions (drain, tool upgrade, apply-template fanout)
	// with label-selector targeting and bounded blast radius
	// (migration 056). Nil-safe.
	FleetOperations *handler.FleetOperationHandler
	// NetworkPolicies owns /api/v1/admin/network-policy-templates/* (CRUD)
	// and /api/v1/clusters/{cluster_id}/network-policies/applications/*
	// (per-cluster apply/list/delete) — migration 068. Nil-safe.
	NetworkPolicies *handler.NetworkPolicyHandler
	// Gatekeeper owns /api/v1/clusters/{id}/gatekeeper/constraints/* (P-04):
	// custom ConstraintTemplate/Constraint authoring, validate + server-side
	// apply through the tunnel, and authored-record CRUD.
	Gatekeeper *handler.GatekeeperConstraintsHandler
	Projects   *handler.ProjectHandler
	Tools      *handler.ToolHandler
	Audit      *handler.AuditHandler
	Alerting   *handler.AlertingHandler
	Anomaly    *handler.AnomalyHandler
	ArgoCD     *handler.ArgoCDHandler
	Backups    *handler.BackupHandler
	Catalog    *handler.CatalogHandler
	// ChartRatings owns /api/v1/charts/{chart_id}/ratings/* and
	// /api/v1/catalog/recommendations/{popular,similar}/* — the
	// migration-055 catalog rating surface. Nil-safe: routes are
	// only mounted when this field is non-nil so tests that don't
	// need the surface (and don't supply the querier) keep building.
	ChartRatings   *handler.ChartRatingsHandler
	Logging        *handler.LoggingHandler
	Monitoring     *handler.MonitoringHandler
	ControlPlane   *handler.ControlPlaneHandler
	Resources      *handler.ResourceHandler
	PlatformCharts *handler.PlatformChartRepoHandler
	// Docs serves the embedded OpenAPI spec + Swagger UI at
	// /api/v1/openapi.yaml + /api/v1/docs/. Public — no JWT required.
	Docs *handler.DocsHandler
	// SSOPresets serves the canonical GitHub/Google/Azure AD/GitLab/
	// Okta preset catalog at /api/v1/settings/sso/presets/.
	SSOPresets   *handler.SSOPresetsHandler
	RBAC         *handler.RBACHandler
	RBACQueries  appmiddleware.RBACQuerier
	RBACEngine   *rbac.Engine
	Security     *handler.SecurityHandler
	ServiceProxy *handler.ServiceProxyHandler
	Workloads    *handler.WorkloadHandler
	Hub          *tunnel.Hub
	Proxy        *tunnel.ProxyHandler
	// InternalK8s receives cross-pod K8sRequest forwards from sibling
	// server replicas. Mounted OUTSIDE the JWT auth middleware — it
	// does its own PSK validation. Nil-safe; absent when no encryption
	// key is configured (single-replica disables the fallback).
	InternalK8s *tunnel.InternalK8sHandler
	// InternalHelm receives cross-pod HelmRequest forwards from sibling
	// server replicas. Same PSK-auth contract as InternalK8s; mounted
	// outside the JWT chain. Nil-safe.
	InternalHelm *tunnel.InternalHelmHandler
	Exec         *tunnel.ExecConsumer
	Logs         *tunnel.LogsConsumer
	// RemoteServer is the new remotedialer-based tunnel running alongside
	// Hub during the migration. Mounted at /api/v1/connect/{cluster_id}/.
	RemoteServer *tunnel2.RemoteServer
	// EventStream serves Server-Sent Events for live UI updates (cluster
	// connect/disconnect, heartbeats). Optional; nil-safe.
	EventStream *handler.EventStreamHandler
	// StreamTickets issues short-lived one-use credentials for browser
	// EventSource/WebSocket connections, avoiding long-lived JWTs in URLs.
	StreamTickets     *handler.StreamTicketHandler
	StreamTicketStore *iauth.StreamTicketStore
	// RemoteQueries is wired into the v2 demonstration handlers below — it's
	// the same *sqlc.Queries the rest of the app uses, exposed under a
	// distinct field so the migration code can resolve cluster rows directly
	// without depending on the cluster handler's private queries field.
	RemoteQueries *sqlc.Queries
	// ResourcesSearch fans a single resource-list query out across every
	// active cluster (Phase A3 of the Rancher-parity plan).
	ResourcesSearch *handler.ResourcesSearchHandler
	// AgentFleet exposes read-only fleet inventory for connected and
	// disconnected adopted-cluster agents.
	AgentFleet *handler.AgentFleetHandler
	// ApiserverAudit ingests kube-apiserver audit events streamed by the
	// per-cluster agent and exposes them for operator read-back
	// (migration 112). Nil-safe — when unwired the routes are omitted.
	ApiserverAudit *handler.ApiserverAuditHandler
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
	// CompliancePosture (T1.2) is the CISO-facing fleet-wide score
	// rollup: weighted combination of CIS, image-vulns, netpol
	// coverage, and audit retention. Read-only.
	CompliancePosture *handler.CompliancePostureHandler
	// License (T7.4) is the read-only entitlement scaffold. Returns
	// {state: "open-source", features_enabled: [...]}; ships now so
	// future LicenseExpiringSoon condition wiring has a stable
	// contract.
	License *handler.LicenseHandler
	// PlatformSettings owns /api/v1/admin/settings/* + the two pre-auth
	// /api/v1/settings/{branding,banner}/ readers. Migration 046.
	PlatformSettings *handler.PlatformSettingsHandler
	// Extensions owns /api/v1/extensions/* — manifest validation plus
	// install/enable/disable controls for UI extension registry entries.
	Extensions *handler.ExtensionHandler
	// PlatformDefaultTemplate (sprint 074) owns
	// /api/v1/admin/platform-settings/default-cluster-template/*.
	PlatformDefaultTemplate *handler.PlatformDefaultTemplateHandler
	// PlatformBaselineCoverage (sprint 075) owns the read-only
	// /coverage/ subroute reporting slug resolution status.
	PlatformBaselineCoverage *handler.PlatformBaselineCoverageHandler
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
	// SCIM owns the /scim/v2/* provisioning surface (migration 114).
	// Mounted OUTSIDE the JWT auth chain — SCIM clients (Okta, Azure AD,
	// OneLogin) authenticate with a static bearer token validated by the
	// handler's own Auth middleware. Nil-safe: when unwired (test fakes,
	// pre-migration boots) the routes are omitted.
	SCIM *handler.SCIMHandler
	// SCIMTokenAdmin owns /api/v1/admin/scim-tokens/* — the superuser
	// surface to mint/list/revoke the static bearer tokens the /scim/v2/*
	// chain authenticates against. Unlike SCIM itself this lives INSIDE
	// the JWT auth chain. Nil-safe: omitted when unwired.
	SCIMTokenAdmin *handler.SCIMTokenAdminHandler
}

type ArgoCDClusterProxyTokenQuerier interface {
	GetArgoCDClusterProxyTokenByHash(ctx context.Context, tokenHash string) (sqlc.ArgocdClusterProxyToken, error)
	TouchArgoCDClusterProxyToken(ctx context.Context, id uuid.UUID) error
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
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(appmiddleware.RequestLogger)
	r.Use(chimiddleware.Recoverer)
	r.Use(appmiddleware.Metrics)
	// Normalise `/api/v1/foo` → `/api/v1/foo/` before chi matches so
	// the frontend's no-trailing-slash REST calls hit the same route
	// the trailing-slash form does. Without this, DELETE /clusters/{id}
	// 404s because the route is mounted as /{id}/ — the user-facing
	// symptom is "the cluster delete button in the UI silently fails."
	// Scoped to /api/v1/* so static helm-repo / argocd assets aren't
	// affected.
	r.Use(appmiddleware.NormalizeAPITrailingSlash)
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
		_ = json.NewEncoder(w).Encode(map[string]string{
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
	if deps.Docs != nil {
		// Public OpenAPI + Swagger UI. Outside the JWT auth chain so
		// operators can browse the API surface before they have a
		// token (it's the entry point for figuring OUT how to get a
		// token in the first place).
		r.Get("/api/v1/openapi.yaml", deps.Docs.ServeOpenAPI)
		r.Get("/api/v1/docs", deps.Docs.ServeSwaggerUI)
		r.Get("/api/v1/docs/", deps.Docs.ServeSwaggerUI)
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
		// Authorization gate (R-01): authentication alone is not enough — the
		// proxy injects the shared upstream ArgoCD admin token, so without an
		// RBAC check any authenticated viewer would be logged into ArgoCD as
		// admin. ArgoCDAuthz fails closed when the RBAC engine/querier are nil,
		// so it's safe to mount unconditionally after argoAuth.
		argoAuthz := appmiddleware.ArgoCDAuthz(deps.RBACEngine, deps.RBACQueries, newLocalClusterResolver(deps.RemoteQueries))
		r.With(argoAuth, argoAuthz).Handle("/argocd", deps.ArgoCDUIProxy)
		r.With(argoAuth, argoAuthz).Handle("/argocd/*", deps.ArgoCDUIProxy)
	}

	// SCIM 2.0 provisioning (migration 114). Mounted at top-level
	// `/scim/v2/*` (NOT under `/api/v1`) and OUTSIDE the JWT auth chain —
	// SCIM clients authenticate with a static bearer token validated by
	// the handler's own Auth middleware. Nil-safe.
	if deps.SCIM != nil {
		r.Route("/scim/v2", func(r chi.Router) {
			r.Use(deps.SCIM.Auth)
			r.Post("/Users", deps.SCIM.CreateUser)
			r.Get("/Users", deps.SCIM.ListUsers)
			r.Get("/Users/{id}", deps.SCIM.GetUser)
			r.Put("/Users/{id}", deps.SCIM.PutUser)
			r.Patch("/Users/{id}", deps.SCIM.PatchUser)
			r.Delete("/Users/{id}", deps.SCIM.DeleteUser)
			r.Get("/Groups", deps.SCIM.ListGroups)
			r.Get("/Groups/{id}", deps.SCIM.GetGroup)
			// Discovery (read-only, static) — Azure AD/Okta probe these
			// before provisioning.
			r.Get("/ServiceProviderConfig", deps.SCIM.ServiceProviderConfig)
			r.Get("/ResourceTypes", deps.SCIM.ResourceTypes)
			r.Get("/Schemas", deps.SCIM.Schemas)
		})
	}

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// REST-only timeout — does NOT apply to WS routes registered at the
		// top level (see r.Get("/api/v1/ws/...") below).
		r.Use(chimiddleware.Timeout(30 * time.Second))
		// /bootstrap/ and /bootstrap/complete/ were removed when the server
		// switched to the Rancher-style admin-on-first-boot model: the
		// startup hook in cmd/server/main.go (auth.EnsureBootstrapAdmin)
		// creates the admin user. No HTTP endpoint is needed for platform
		// first-setup any more.

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
			// Enroll start/confirm accept EITHER a live session OR the
			// PurposeTOTPEnrollOnly challenge Login issues when MFA enrollment is
			// enforced and the user has not yet enrolled. Without the challenge
			// path a forced-enrollment user has no session and could never enroll.
			enrollAuth := enrollChallengeOrAuth(deps.JWT, deps.AuthQueries)
			r.With(enrollAuth).Post("/auth/totp/enroll/start/", deps.TOTP.EnrollStart)
			r.With(enrollAuth).Post("/auth/totp/enroll/confirm/", deps.TOTP.EnrollConfirm)
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
			// The activity feed exposes a rolling stream of operations + named
			// resources drawn from the audit log; gate it like the audit-log read
			// (requireAuth + audit_logs read/list) instead of leaving it public.
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requireAnyPermission(
					deps.RBACEngine,
					deps.RBACQueries,
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
				),
			).Get("/activity/", deps.Resources.ListActivity)
			r.Route("/settings", func(r chi.Router) {
				r.Get("/general/", deps.Resources.GetGeneralSettings)
				r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).
					Put("/general/", deps.Resources.UpdateGeneralSettings)
				r.Get("/sso/", deps.Resources.ListSSOProviders)
				r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSSO, rbac.VerbCreate)).
					Post("/sso/", deps.Resources.CreateSSOProvider)
				r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSSO, rbac.VerbDelete)).
					Delete("/sso/{id}/", deps.Resources.DeleteSSOProvider)
				// Preset catalog (GitHub / Google / Azure AD / GitLab /
				// Okta). Public-readable so the login page can render
				// branded buttons before the user is authenticated.
				if deps.SSOPresets != nil {
					r.Get("/sso/presets/", deps.SSOPresets.List)
				}
				r.With(
					requireAuth(deps.JWT, deps.AuthQueries),
					requireAnyPermission(
						deps.RBACEngine,
						deps.RBACQueries,
						permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
						permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
					),
				).Get("/audit-logs/", deps.Resources.ListAuditLogs)
				if deps.Monitoring != nil {
					// NEW-1: the shared Thanos/Alertmanager monitoring-stack
					// install/upgrade/replace/uninstall routes run helm against
					// the management/monitoring cluster but were never wired
					// through the GATE-0 write-scope backstop. A read-scoped API
					// token must not trigger these helm mutations. requireAuth
					// stashes the token so the scope middleware can see it (it
					// would otherwise bypass an unauthenticated request), then
					// monitoringWriteScope enforces clusters:write on the
					// mutating helm verbs (reads/preview/status pass through).
					monitoringMutate := r.With(requireAuth(deps.JWT, deps.AuthQueries), appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteClusters))
					r.Get("/monitoring/backend/", deps.Monitoring.GetBackendConfig)
					r.Put("/monitoring/backend/", deps.Monitoring.UpdateBackendConfig)
					r.Get("/monitoring/operations/", deps.Monitoring.ListOperations)
					r.Get("/monitoring/operations/{id}/", deps.Monitoring.GetOperation)
					r.Post("/monitoring/operations/{id}/retry/", deps.Monitoring.RetryOperation)
					r.Get("/monitoring/thanos/status/", deps.Monitoring.GetSharedThanosStatus)
					r.Post("/monitoring/thanos/preview/", deps.Monitoring.PreviewSharedThanosStack)
					monitoringMutate.Post("/monitoring/thanos/install/", deps.Monitoring.InstallSharedThanosStack)
					monitoringMutate.Put("/monitoring/thanos/upgrade/", deps.Monitoring.UpgradeSharedThanosStack)
					monitoringMutate.Post("/monitoring/thanos/replace/", deps.Monitoring.ReplaceSharedThanosStack)
					monitoringMutate.Delete("/monitoring/thanos/uninstall/", deps.Monitoring.UninstallSharedThanosStack)
					r.Get("/monitoring/alertmanager/status/", deps.Monitoring.GetSharedAlertmanagerStatus)
					r.Post("/monitoring/alertmanager/preview/", deps.Monitoring.PreviewSharedAlertmanager)
					monitoringMutate.Post("/monitoring/alertmanager/install/", deps.Monitoring.InstallSharedAlertmanager)
					monitoringMutate.Put("/monitoring/alertmanager/upgrade/", deps.Monitoring.UpgradeSharedAlertmanager)
					monitoringMutate.Post("/monitoring/alertmanager/replace/", deps.Monitoring.ReplaceSharedAlertmanager)
					monitoringMutate.Delete("/monitoring/alertmanager/uninstall/", deps.Monitoring.UninstallSharedAlertmanager)
				}
			})
			// The user directory (usernames, emails, last-login, active state) is
			// operator PII and must not be readable unauthenticated. Reads require
			// auth + users:read/list, mirroring the write routes' users RBAC.
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requireAnyPermission(
					deps.RBACEngine,
					deps.RBACQueries,
					permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbRead},
					permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbList},
				),
			).Get("/users/", deps.Resources.ListUsers)
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbRead),
			).Get("/users/{id}/", deps.Resources.GetUser)
		}

		if deps.Auth != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/settings/tokens/", deps.Auth.ListTokens)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/settings/tokens/", deps.Auth.CreateToken)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/settings/tokens/{id}/", deps.Auth.RevokeToken)
		}
		if deps.StreamTickets != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/streams/tickets/", deps.StreamTickets.Create)
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
		if deps.CompliancePosture != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/compliance/posture/", deps.CompliancePosture.Get)
		}
		if deps.License != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/license/", deps.License.Get)
		}
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
			// T28b — DLQ mutators. Retry moves an archived task back to
			// pending; Discard removes it entirely. Both gated by the
			// handler's own superuser check; audited.
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/queues/{queue}/dlq/{id}/retry/", deps.AdminQueues.RetryDLQ)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/admin/queues/{queue}/dlq/{id}/", deps.AdminQueues.DiscardDLQ)
		}

		// Durable task-outbox inspector — committed DB task intents that
		// have not yet made it to Redis/Asynq. Superuser-gated inside the
		// handler; retry moves non-delivered rows back to pending for the
		// dispatcher to send again.
		if deps.AdminTaskOutbox != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/task-outbox/", deps.AdminTaskOutbox.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/task-outbox/dead/", deps.AdminTaskOutbox.ListDead)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/task-outbox/{id}/retry/", deps.AdminTaskOutbox.Retry)
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
			// Push webhook: NOT JWT-gated (a git provider can't present a JWT) —
			// the handler authenticates via the X-Astronomer-Webhook-Secret shared
			// secret and 503s when no secret is configured.
			r.Post("/gitops/sources/{id}/webhook/", deps.GitOps.Webhook)
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
			// namespace allowlist (`branding`, `banner`,
			// `registration`). Feature flags are authenticated below.
			r.Get("/settings/branding/", deps.PlatformSettings.PublicBranding)
			r.Get("/settings/banner/", deps.PlatformSettings.PublicBanner)
			r.Get("/settings/registration/", deps.PlatformSettings.PublicRegistration)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/settings/features/", deps.PlatformSettings.Features)
		}

		// SCIM provisioning-token admin (migration 114). Mints/lists/
		// revokes the static bearer tokens the top-level /scim/v2/* chain
		// authenticates against. INSIDE the JWT auth chain and superuser-
		// gated inside the handler (same pattern as /admin/settings/*).
		// The create response returns the plaintext astro_scim_<random>
		// token exactly once; only the hash is stored.
		if deps.SCIMTokenAdmin != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/scim-tokens/", deps.SCIMTokenAdmin.Create)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/scim-tokens/", deps.SCIMTokenAdmin.List)
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/admin/scim-tokens/{id}/", deps.SCIMTokenAdmin.Delete)
		}

		// Rancher-style one-liner manifest fetch. Unauthenticated by
		// design: the token in the URL IS the credential, exactly
		// like the agent token embedded in the manifest it returns.
		// Lives outside the `authenticated` subrouter for the same
		// reason as the pre-auth readers above. The `.yaml` suffix
		// makes the trailing-slash middleware leave it alone, so
		// `curl -sfL <server>/api/v1/register/<token>.yaml | kubectl
		// apply -f -` works without redirect dance.
		if deps.Clusters != nil {
			// L3: the bootstrap manifest carries the registration token in
			// plaintext; a per-IP request-rate cap is correct here (stateless,
			// no reconnect, bad token is a 404). Middleware on an existing route
			// does NOT change the route pattern, so routes.json/openapi are
			// unaffected.
			r.With(appmiddleware.LoginRateLimit(cfg.TunnelRegisterRateLimitPerMinute, time.Minute)).
				Get("/register/{token}", deps.Clusters.GetManifestByToken)
			// Short-TTL HMAC-signed manifest URL — no token in the URL,
			// the signature over (cluster_id, expiry) is the credential.
			// Registered before /register/{token} would otherwise match
			// "signed" as a token; chi's static-vs-param routing prefers
			// the literal segment so order is informational.
			// IP-keyed rate limit: the signed URL is replayable within its
			// 15m TTL and each hit mints a fresh registration token, so cap
			// the request rate the same way the auth routes do.
			r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Get("/register/signed/{cluster_id}", deps.Clusters.GetSignedManifest)
			// Companion endpoint for the `curl --cacert ca.crt …`
			// variant; returns operator-uploaded PEM bundle when the
			// platform runs behind a private CA. 404 when unset.
			r.Get("/register/ca.crt", deps.Clusters.GetCABundle)
		}

		// Sprint 074 — platform-default cluster template. The
		// /admin/platform-settings/default-cluster-template/* surface
		// manages the auto-attach baseline (typically the seeded
		// "Platform baseline" — trivy-operator, kube-state-metrics,
		// node-exporter, fluent-bit, ingress-nginx, cert-manager,
		// gatekeeper) that the cluster
		// Create handler binds to every newly-registered cluster.
		// Superuser-gated inside the handler. Reapply takes a
		// {cluster_id} path param so an operator can back-fill an
		// existing cluster after changing the baseline.
		if deps.PlatformDefaultTemplate != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Get)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Update)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/platform-settings/default-cluster-template/reapply/{cluster_id}/", deps.PlatformDefaultTemplate.Reapply)
		}

		// Sprint 075 — read-only platform-baseline slug-coverage check.
		if deps.PlatformBaselineCoverage != nil {
			r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get(
				"/admin/platform-settings/default-cluster-template/coverage/",
				deps.PlatformBaselineCoverage.Coverage,
			)
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
			// Default-deny scope backstop: a read-only API token can never
			// reach a mutating handler, regardless of whether the specific
			// subtree opted into a write scope. Wired right after auth so
			// the token row is in context. `required=""` keeps this purely
			// a read-only-token rejector — subtree-level
			// RequireWriteScopeForMutations / requireScope still enforce the
			// specific write scope on top, and RBAC remains the primary gate.
			// GET/HEAD/OPTIONS, JWT sessions, and legacy empty-scope tokens
			// pass through untouched (see RequireWriteScopeForMutations).
			authenticated.Use(appmiddleware.RequireWriteScopeForMutations(""))
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
	if deps.ClusterRegistration != nil {
		// Per-cluster wizard SSE stream — same long-lived contract as
		// the global stream above, registered outside the timeout group
		// for the same reason. It still carries the same auth/RBAC
		// protection as the registration status route mounted inside
		// /api/v1 above.
		r.With(
			requireStreamTicketOrAuth(deps.JWT, deps.AuthQueries, deps.StreamTicketStore, iauth.StreamKindRegistration, "id"),
			requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead),
		).Get("/api/v1/clusters/{id}/registration/events/", deps.ClusterRegistration.StreamEvents)
	}
	if deps.Workloads != nil {
		// Live pod watch SSE stream — streams ADDED/MODIFIED/DELETED events
		// through the agent tunnel instead of the UI polling the pod list.
		// Long-lived, so register outside the /api/v1 Timeout middleware group
		// (same contract as the event/registration SSE streams above). Auth is
		// a one-use stream ticket (scoped to the cluster) or a normal token,
		// plus the pods:read RBAC gate.
		//
		// F7: use the namespace-scoped LIST gate instead of the cluster-wide
		// requirePermission so a namespace-confined tenant can open the watch for
		// a namespace they own (or a bare watch when they hold pods:read in ≥1
		// namespace). With namespace_scoped_rbac_enabled OFF this is byte-identical
		// to requirePermission(pods, read) — no behavior change. The admission is
		// paired with per-frame filtering in WatchPods (same gate+filter invariant
		// as ListPods) so an admitted scoped caller never receives frames for a
		// namespace outside their allow-set; a cluster-wide/superuser caller passes
		// the plain check and watches everything unfiltered.
		r.With(
			requireStreamTicketOrAuth(deps.JWT, deps.AuthQueries, deps.StreamTicketStore, iauth.StreamKindLogs, "cluster_id"),
			requireListPermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbRead, deps.NamespaceScopedRBAC),
		).Get("/api/v1/clusters/{cluster_id}/pods/watch/", deps.Workloads.WatchPods)
	}
	if deps.RemoteServer != nil {
		// remotedialer hijacks the connection for a WS upgrade, so this MUST
		// be registered outside the /api/v1 group that applies a Timeout
		// middleware (the same reason the legacy ws/agent/tunnel route lives
		// out here).
		// A4 / M5: pre-upgrade per-IP failure-limiter gate (shared with the hub
		// connect path) so an over-threshold IP gets a clean 429 before
		// remotedialer hijacks the connection. Middleware on an existing route
		// does NOT change the route pattern.
		r.With(deps.RemoteServer.RateLimitMiddleware()).
			HandleFunc("/api/v1/connect/{cluster_id}/", deps.RemoteServer.ServeHTTP)
		// Demonstration endpoint — proves the new tunnel works end-to-end by
		// listing pods through a stock client-go clientset whose transport is
		// dialed through remotedialer. Real handlers will follow once the
		// migration is verified. Keep it out of production so demo-only
		// cluster data surfaces cannot linger as a supported API.
		if !isProductionConfig(cfg) {
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead),
			).Get("/api/v1/clusters/{id}/v2/pods/", remoteV2PodsHandler(cfg, deps))
		}
	}
	if deps.Proxy != nil {
		// k8s passthrough is the most common loop-DoS target — any
		// authenticated user can fire arbitrary list calls. Token bucket
		// is sized so a normal UI burst (clicking through tabs) passes;
		// a runaway loop trips within ~20 requests.
		r.With(
			rateLimit(appmiddleware.ClassK8sProxy),
			requireAuth(deps.JWT, deps.AuthQueries),
			requireK8sProxyScope(),
			requireK8sProxyPermission(deps.RBACEngine, deps.RBACQueries, deps.NativeAuthz, deps.NamespaceScopedRBAC),
			auditK8sProxySecretReads(deps.AuditWriter),
			auditK8sProxyMutations(deps.AuditWriter),
		).
			HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)
		if deps.ArgoCDProxyTokens != nil {
			// Dedicated machine-to-machine front door for built-in ArgoCD.
			// It deliberately does not accept user JWTs or user API tokens:
			// the bearer token is cluster-scoped and validated by hash
			// against argocd_cluster_proxy_tokens before the shared tunnel
			// proxy sees the request.
			r.With(
				rateLimit(appmiddleware.ClassArgoCDProxy),
				requireArgoCDClusterProxyToken(deps.ArgoCDProxyTokens),
				auditArgoCDK8sProxyMutations(deps.AuditWriter),
			).
				HandleFunc("/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)
		}
	}
	if deps.InternalK8s != nil {
		// Cross-pod fallback for the server-internal K8sRequester.
		// PSK-protected so non-sibling callers 403 — mounted outside the
		// JWT auth chain because sibling pods don't carry user JWTs.
		r.Post("/internal/tunnel/k8s/{cluster_id}", deps.InternalK8s.Handle)
	}
	if deps.InternalHelm != nil {
		// Cross-pod fallback for the server-internal HelmRequester.
		// Same PSK + outside-JWT-chain contract as the K8s counterpart.
		// Required for catalog install/upgrade/uninstall to work when
		// the request lands on a replica that doesn't own the WS.
		r.Post("/internal/tunnel/helm/{cluster_id}", deps.InternalHelm.Handle)
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

	// Kubectl shell WS handshake — session-aware front door that
	// validates the {id} row belongs to the caller, then upgrades the
	// WebSocket on this route and proxies frames inline onto the
	// cluster agent's exec relay (the same code path the
	// /api/v1/ws/exec/ route runs post-upgrade).
	//
	// We used to 307-redirect at /api/v1/ws/exec/{cluster_id}/{ns}/
	// {pod}/{container}/. Chromium followed the redirect transparently
	// before the Upgrade handshake but Firefox does not, and several
	// corporate proxies strip Upgrade headers across redirects, so the
	// shell route now terminates the WS here.
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

// enrollChallengeOrAuth guards a route with either a normal session or a
// PurposeTOTPEnrollOnly challenge (see AuthOrTOTPEnrollChallenge). Mirrors
// requireAuth's nil-jwt passthrough for test wiring.
func enrollChallengeOrAuth(jwt *iauth.JWTManager, queries appmiddleware.TokenUserQuerier) func(http.Handler) http.Handler {
	if jwt == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return appmiddleware.AuthOrTOTPEnrollChallenge(jwt, queries)
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

type permissionRequirement struct {
	resource rbac.Resource
	verb     rbac.Verb
}

func requireAnyPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, requirements ...permissionRequirement) func(http.Handler) http.Handler {
	if engine == nil || querier == nil || len(requirements) == 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := appmiddleware.GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				})
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				})
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			for _, requirement := range requirements {
				if engine.CheckPermission(bindings, requirement.resource, requirement.verb, clusterID, projectID) {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "permission_denied",
					"message": "You do not have permission to perform this action",
				},
			})
		})
	}
}

func permissionScopeIDs(r *http.Request) (uuid.UUID, uuid.UUID) {
	var clusterID, projectID uuid.UUID
	clusterParam := chi.URLParam(r, "cluster_id")
	if clusterParam == "" {
		clusterParam = chi.URLParam(r, "id")
	}
	if clusterParam != "" {
		if parsed, err := uuid.Parse(clusterParam); err == nil {
			clusterID = parsed
		}
	}
	projectParam := chi.URLParam(r, "project_id")
	if projectParam == "" {
		projectParam = chi.URLParam(r, "id")
	}
	if projectParam != "" {
		if parsed, err := uuid.Parse(projectParam); err == nil {
			projectID = parsed
		}
	}
	return clusterID, projectID
}

// nativeNamespaceLister is an OPTIONAL capability a nativeAuthorizer may
// implement (via a type assertion) to participate in the cluster-wide-list
// allow-set filter, not just the single-namespace Allow() check. It returns the
// namespace visibility the user's native per-CRD rules grant for a cluster-wide
// LIST of (apiGroup, resource, verb) on clusterID:
//
//   - all==true  → a native rule grants this list without namespace narrowing
//     (any namespace). names must be ignored.
//   - all==false → names is the exact allow-set of namespaces the native rules
//     grant for this list (possibly empty → contributes nothing).
//
// Implementations MUST apply the same conservative guards as rbac.NativeAllow
// (refuse privilege-escalation api groups; native rules never widen exec/logs),
// so folding these namespaces into the list filter can never grant more than an
// operator explicitly authored. *nativeRBACAuthorizer implements this.
type nativeNamespaceLister interface {
	AuthorizedNamespaces(ctx context.Context, userID, clusterID, apiGroup, resource, verb string) (all bool, names map[string]struct{})
}

func requireK8sProxyPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, native nativeAuthorizer, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := k8sProxyPermission(r)
			user, ok := appmiddleware.GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				})
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				})
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			// SECURITY: resolve the target namespace from the PARSED k8s request
			// path, never from a user-controlled ?namespace= query param. The
			// proxy forwards the path namespace (/api/v1/namespaces/<ns>/...), so
			// a namespace-scoped RBAC binding must be evaluated against that same
			// namespace. Trusting the query would let a caller authorized for one
			// namespace read another namespace's resources (incl. secrets) by
			// changing the query. parseK8sProxyObjectRef returns nil (→ empty
			// namespace) for cluster-scoped / discovery paths, which fails closed
			// against namespace-scoped bindings — matching the forwarded request.
			k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
			ref := parseK8sProxyObjectRef(k8sPath)
			namespace := ref["namespace"]
			if !engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace) {
				// Coarse RBAC denied. Consult the native per-CRD allow layer as
				// an ADDITIVE override: a native rule can grant this exact
				// (api_group, resource, verb) at this scope even when the coarse
				// custom_resources bucket doesn't. native is nil when the
				// feature is off, so default behavior is unchanged. The
				// evaluator itself refuses escalation groups + exec/logs, so a
				// native rule can never widen past those guards.
				if native == nil || !native.Allow(r.Context(), user.ID, clusterID.String(), namespace, ref["api_group"], ref["resource"], string(verb)) {
					// Namespace-scoped RBAC allow-through-and-filter gate. Both
					// the coarse and native checks denied because this scoped
					// user has no cluster-wide grant. If the flag is on and this
					// is a plain cluster-wide LIST (GET, VerbList, not a watch,
					// namespace=="") for which the user holds the (resource,
					// list) permission in at least one namespace on this cluster,
					// admit the request and stash the authorized-namespace
					// allow-set. The tunnel proxy filters the buffered list body
					// down to those namespaces. Namespaced paths were already
					// authorized above by CheckPermission; watches, mutations,
					// named GETs, and users with no namespace access all keep
					// failing closed here.
					if namespaceScoped &&
						r.Method == http.MethodGet &&
						verb == rbac.VerbList &&
						namespace == "" &&
						!isK8sProxyWatchRequest(r) &&
						ref["watch"] != "true" {
						all, names := engine.AuthorizedNamespaces(bindings, resource, verb, clusterID)
						// Fold native per-namespace list grants into the allow-set
						// too. Coarse project bindings already participate (via
						// AuthorizedNamespaces above), but a user whose ONLY grant
						// for this CRD is a native namespaced rule would otherwise
						// 403 on a cluster-wide LIST instead of getting a filtered
						// result — the native layer is only consulted as a single-
						// namespace Allow() above, never for list-filtering. The
						// optional nativeNamespaceLister capability (implemented by
						// *nativeRBACAuthorizer) enumerates the namespaces the
						// user's native rules grant for this (api_group, resource,
						// list) at this cluster; the same escalation-group / exec-
						// logs guards apply inside it, so it can never widen past
						// those. A nil authorizer or one that doesn't implement the
						// capability leaves behavior unchanged.
						if !all {
							if lister, ok := native.(nativeNamespaceLister); ok && lister != nil {
								nativeAll, nativeNames := lister.AuthorizedNamespaces(r.Context(), user.ID, clusterID.String(), ref["api_group"], ref["resource"], string(verb))
								if nativeAll {
									all = true
								} else if len(nativeNames) > 0 {
									if names == nil {
										names = make(map[string]struct{}, len(nativeNames))
									}
									for ns := range nativeNames {
										names[ns] = struct{}{}
									}
								}
							}
						}
						switch {
						case all:
							// Cluster-wide grant — shouldn't reach here since
							// CheckPermission would have passed. Serve unfiltered.
							next.ServeHTTP(w, r)
							return
						case len(names) > 0:
							ctx := tunnel.WithNamespaceFilter(r.Context(), names)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error": map[string]string{
							"code":    "permission_denied",
							"message": "You do not have permission to perform this action",
						},
					})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireK8sProxyScope() func(http.Handler) http.Handler {
	writeClusters := requireScope(iauth.ScopeWriteClusters)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutatingK8sProxyMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			writeClusters(next).ServeHTTP(w, r)
		})
	}
}

func auditK8sProxyMutations(auditWriter any) func(http.Handler) http.Handler {
	return auditK8sProxyMutationsWithAction(auditWriter, "cluster.k8s_proxy.forwarded", nil)
}

func auditK8sProxySecretReads(auditWriter any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, verb, ok := k8sProxySecretReadPermission(r); ok {
				clusterID := chi.URLParam(r, "cluster_id")
				k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
				detail := map[string]any{
					"method":   r.Method,
					"k8s_path": k8sPath,
					"verb":     string(verb),
				}
				if ref := parseK8sProxyObjectRef(k8sPath); len(ref) > 0 {
					for k, v := range ref {
						detail[k] = v
					}
				}
				handler.RecordAuditFromRequest(r, auditWriter, "cluster.secret.read", "cluster", clusterID, secretAuditResourceName(detail), detail)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewInternalArgoCDProxyRouter builds the handler for the dedicated internal
// ArgoCD->adopted-cluster k8s proxy listener (config.ArgoCDInternalProxyAddr).
//
// Unlike the public /api/v1/internal/argocd route, this listener is NOT
// token-gated. ArgoCD's GitOps apply path (CreateNamespace, manifest apply,
// even under ServerSideApply) sends requests with no Authorization header at
// all — kubectl treats discovery/apply as anonymous — so a per-request token
// can never gate it. Instead this runs on its own port that the public ingress
// never maps and a NetworkPolicy restricts to the argocd namespace: network
// isolation IS the authentication boundary. Routing uses the path cluster_id,
// and mutations are still audited.
func NewInternalArgoCDProxyRouter(deps RouterDependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(appmiddleware.NormalizeAPITrailingSlash)
	if deps.Proxy == nil {
		return r
	}
	rateLimit := func(class appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler {
		return appmiddleware.APIRateLimit(context.Background(), class, nil)
	}
	r.With(
		rateLimit(appmiddleware.ClassArgoCDProxy),
		auditArgoCDK8sProxyMutations(deps.AuditWriter),
	).HandleFunc("/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)
	return r
}

func auditArgoCDK8sProxyMutations(auditWriter any) func(http.Handler) http.Handler {
	return auditK8sProxyMutationsWithAction(auditWriter, "argocd.k8s_proxy.forwarded", map[string]any{
		"proxy": "argocd_internal",
	})
}

func auditK8sProxyMutationsWithAction(auditWriter any, action string, extraDetail map[string]any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMutatingK8sProxyMethod(r.Method) {
				clusterID := chi.URLParam(r, "cluster_id")
				k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
				detail := map[string]any{
					"method":   r.Method,
					"k8s_path": k8sPath,
				}
				if ref := parseK8sProxyObjectRef(k8sPath); len(ref) > 0 {
					for k, v := range ref {
						detail[k] = v
					}
				}
				for k, v := range extraDetail {
					detail[k] = v
				}
				handler.RecordAuditFromRequest(r, auditWriter, action, "cluster", clusterID, k8sPath, detail)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func k8sProxyPermission(r *http.Request) (rbac.Resource, rbac.Verb) {
	if r == nil || r.URL == nil {
		return rbac.ResourceClusters, rbac.VerbRead
	}

	k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
	ref := parseK8sProxyObjectRef(k8sPath)

	// F1 (M2): pod exec/attach/portforward is RCE-equivalent and MUST map to
	// the dedicated pods:exec verb. Detect the subresource from the parsed
	// object ref (robust to core-vs-apis prefix and trailing shape) rather
	// than a brittle hardcoded path matcher, so a mutating exec request can
	// never degrade to a generic pod write verb. The fallback to the raw URL
	// path keeps the gate working even when the chi wildcard param is unset
	// (e.g. direct handler calls in tests).
	if isHighRiskPodProxySubresourceRef(ref) || isHighRiskPodProxySubresource(r.URL.Path) {
		return rbac.ResourcePods, rbac.VerbExec
	}

	verb := k8sProxyVerb(r, ref)
	if ref["resource"] == "pods" && ref["subresource"] == "log" && !isMutatingK8sProxyMethod(r.Method) {
		return rbac.ResourcePods, rbac.VerbLogs
	}
	// F2 (M3): custom resources, unknown apigroups, and non-resource discovery
	// URLs are governed by an explicit, conservative policy rather than
	// collapsing to the generic clusters verb (which let per-resource RBAC
	// silently not apply to CRDs). Decide this BEFORE namedResourcePermission's
	// generic fallthrough. See k8sProxyResourcePolicy.
	if resource, ok := k8sProxyResourcePolicy(ref); ok {
		// F2 (M4): writing to the privilege-escalation API groups (RBAC,
		// admission webhooks, aggregated APIServices, CRD definitions) via the
		// proxy is cluster-admin-equivalent — e.g. POST a ClusterRoleBinding to
		// bind yourself to cluster-admin. The generic custom_resources grant
		// must NOT authorise that, so gate mutating verbs on these groups behind
		// the dedicated rbac permission (held only by owner/admin templates).
		// Reads/lists/watches stay on custom_resources so the explorer is
		// unchanged.
		if isMutatingK8sProxyMethod(r.Method) && isPrivilegeEscalationAPIGroup(ref["api_group"]) {
			return rbac.ResourceRBAC, verb
		}
		return resource, verb
	}
	resource, verb := namedResourcePermission(ref["resource"], verb)
	return resource, verb
}

// isPrivilegeEscalationAPIGroup reports whether a Kubernetes API group lets a
// writer escalate to cluster-admin: RBAC (ClusterRole/Binding), admission
// webhooks (intercept/mutate any request), aggregated APIServices, and CRD
// definitions (own the shape of arbitrary cluster resources).
func isPrivilegeEscalationAPIGroup(group string) bool {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"apiextensions.k8s.io":
		return true
	}
	return false
}

// k8sProxyResourcePolicy implements the F2 (M3) policy for shapes that the
// typed namedResourcePermission table does NOT recognise, so they no longer
// silently collapse to the generic ResourceClusters permission:
//
//   - Custom resources served under apis/<group>/<version>/... whose
//     <group> is not a core/built-in Kubernetes group map to the dedicated
//     ResourceCustomResources permission, so per-resource RBAC (e.g.
//     custom_resources:read / :update) governs CRD access instead of the
//     blanket clusters verb.
//   - Non-resource discovery URLs (/version, /healthz, /api, /apis and their
//     sub-paths) carry no parseable object ref; they are read-only and map to
//     ResourceClusters/VerbRead via the caller. They are intentionally NOT
//     claimed here (ok=false) so the existing read classification stands.
//
// The policy is conservative: it never broadens access. A request that maps
// to ResourceCustomResources requires that permission explicitly; absent a
// matching binding the request is denied (whereas previously a clusters
// binding would have allowed it).
func k8sProxyResourcePolicy(ref map[string]string) (rbac.Resource, bool) {
	if len(ref) == 0 {
		// Non-resource / discovery URL (parseK8sProxyObjectRef returned nil):
		// leave it to the generic read-only clusters classification.
		return "", false
	}
	resourceType := strings.ToLower(strings.TrimSpace(ref["resource"]))
	if resourceType == "" {
		return "", false
	}
	if _, known := knownK8sProxyResource(resourceType); known {
		// A built-in/typed resource — handled by namedResourcePermission.
		return "", false
	}
	// Custom resource under apis/<group>/<version>/...: map to the dedicated
	// custom-resources permission. Core-group (api/v1) unknown resources are
	// rare/internal and stay on the generic clusters classification to avoid
	// over-restricting discovery-ish core endpoints.
	if strings.TrimSpace(ref["api_group"]) != "" {
		return rbac.ResourceCustomResources, true
	}
	return "", false
}

func k8sProxyVerb(r *http.Request, ref map[string]string) rbac.Verb {
	if !isMutatingK8sProxyMethod(r.Method) {
		if isK8sProxyWatchRequest(r) || ref["watch"] == "true" {
			return rbac.VerbWatch
		}
		if ref["name"] == "" {
			return rbac.VerbList
		}
		return rbac.VerbRead
	}

	switch r.Method {
	case http.MethodPost:
		// F3 (L1): POST pods/{name}/eviction deletes the pod (the Eviction
		// subresource is a delete operation), so classify it as VerbDelete
		// for honest RBAC + audit rather than the generic POST-to-named-
		// subresource update verb.
		if ref["resource"] == "pods" && ref["subresource"] == "eviction" {
			return rbac.VerbDelete
		}
		if ref["name"] == "" {
			return rbac.VerbCreate
		}
		return rbac.VerbUpdate
	case http.MethodDelete:
		return rbac.VerbDelete
	default:
		return rbac.VerbUpdate
	}
}

func k8sProxySecretReadPermission(r *http.Request) (rbac.Resource, rbac.Verb, bool) {
	if r == nil || isMutatingK8sProxyMethod(r.Method) {
		return "", "", false
	}
	k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
	ref := parseK8sProxyObjectRef(k8sPath)
	if ref["resource"] != "secrets" {
		return "", "", false
	}
	verb := rbac.VerbRead
	if isK8sProxyWatchRequest(r) {
		verb = rbac.VerbWatch
	} else if ref["name"] == "" {
		verb = rbac.VerbList
	}
	return rbac.ResourceSecrets, verb, true
}

func secretAuditResourceName(detail map[string]any) string {
	namespace, _ := detail["namespace"].(string)
	name, _ := detail["name"].(string)
	switch {
	case namespace != "" && name != "":
		return namespace + "/" + name
	case name != "":
		return name
	case namespace != "":
		return namespace + "/secrets"
	default:
		return "secrets"
	}
}

func parseK8sProxyObjectRef(k8sPath string) map[string]string {
	parts := strings.Split(strings.Trim(k8sPath, "/"), "/")
	if len(parts) < 3 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	switch {
	case len(parts) >= 3 && parts[0] == "api":
		out["api_version"] = parts[1]
		idx = 2
	case len(parts) >= 4 && parts[0] == "apis":
		out["api_group"] = parts[1]
		out["api_version"] = parts[2]
		idx = 3
	default:
		return nil
	}
	if idx < len(parts) && parts[idx] == "watch" {
		out["watch"] = "true"
		idx++
	}
	if idx < len(parts) && parts[idx] == "namespaces" && idx+1 < len(parts) {
		out["namespace"] = parts[idx+1]
		idx += 2
	}
	if idx < len(parts) {
		out["resource"] = parts[idx]
	}
	if idx+1 < len(parts) {
		out["name"] = parts[idx+1]
	}
	if idx+2 < len(parts) {
		out["subresource"] = parts[idx+2]
	}
	return out
}

func requireServiceProxyPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			verb := rbac.VerbRead
			if isMutatingK8sProxyMethod(r.Method) {
				verb = rbac.VerbUpdate
			}
			requirePermission(engine, querier, rbac.ResourceClusters, verb)(next).ServeHTTP(w, r)
		})
	}
}

func requireGenericResourceListPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbList)
			requirePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

func requireNamedResourcePermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, routeParam string, requestedVerb rbac.Verb) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, routeParam), requestedVerb)
			requirePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

func namedResourcePermission(resourceType string, requestedVerb rbac.Verb) (rbac.Resource, rbac.Verb) {
	if resource, ok := knownK8sProxyResource(resourceType); ok {
		return resource, requestedVerb
	}
	if requestedVerb == rbac.VerbRead || requestedVerb == rbac.VerbList || requestedVerb == rbac.VerbWatch {
		return rbac.ResourceClusters, requestedVerb
	}
	return rbac.ResourceClusters, rbac.VerbUpdate
}

// knownK8sProxyResource maps a Kubernetes resource type (singular or plural,
// case-insensitive) to its astronomer RBAC resource. The second return value
// reports whether the type is a recognised built-in; callers use it to decide
// whether the F2 custom-resource policy should apply instead of the generic
// clusters fallthrough.
func knownK8sProxyResource(resourceType string) (rbac.Resource, bool) {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "services", "service", "endpoints", "endpoint":
		return rbac.ResourceServices, true
	case "ingresses", "ingress",
		"gateways", "gateway",
		"httproutes", "httproute",
		"gatewayclasses", "gatewayclass",
		"grpcroutes", "grpcroute",
		"tcproutes", "tcproute",
		"udproutes", "udproute",
		"tlsroutes", "tlsroute",
		"referencegrants", "referencegrant":
		return rbac.ResourceIngresses, true
	case "networkpolicies", "networkpolicy":
		return rbac.ResourceNetworkPolicies, true
	case "persistentvolumes", "persistentvolume", "pv",
		"persistentvolumeclaims", "persistentvolumeclaim", "pvc",
		"storageclasses", "storageclass":
		return rbac.ResourceStorage, true
	case "configmaps", "configmap":
		return rbac.ResourceConfigMaps, true
	case "secrets", "secret":
		return rbac.ResourceSecrets, true
	case "pods", "pod":
		return rbac.ResourcePods, true
	case "nodes", "node":
		return rbac.ResourceNodes, true
	case "deployments", "deployment",
		"daemonsets", "daemonset",
		"statefulsets", "statefulset",
		"replicasets", "replicaset",
		"jobs", "job",
		"cronjobs", "cronjob",
		"hpa", "horizontalpodautoscalers", "horizontalpodautoscaler",
		"poddisruptionbudgets", "poddisruptionbudget":
		return rbac.ResourceWorkloads, true
	default:
		return "", false
	}
}

func auditGenericSecretList(auditWriter any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(chi.URLParam(r, "resource_type"), "secrets") {
				clusterID := chi.URLParam(r, "cluster_id")
				namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
				detail := map[string]any{
					"method":        r.Method,
					"resource_type": "secrets",
					"verb":          string(rbac.VerbList),
					"scope":         "generic_resource_list",
				}
				resourceName := "secrets"
				if namespace != "" {
					detail["namespace"] = namespace
					resourceName = namespace + "/secrets"
				}
				handler.RecordAuditFromRequest(r, auditWriter, "cluster.secret.read", "cluster", clusterID, resourceName, detail)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireServiceProxyScope() func(http.Handler) http.Handler {
	return requireK8sProxyScope()
}

func requireArgoCDClusterProxyToken(queries ArgoCDClusterProxyTokenQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if queries == nil {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "ArgoCD cluster proxy authentication is not configured")
				return
			}
			clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
			if err != nil {
				writeRouteAuthError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
				return
			}
			token, ok := bearerToken(r)
			if !ok || !strings.HasPrefix(token, iauth.ArgoCDClusterProxyTokenPrefix) {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Valid ArgoCD cluster proxy token is required")
				return
			}
			row, err := queries.GetArgoCDClusterProxyTokenByHash(r.Context(), iauth.HashArgoCDClusterProxyToken(token))
			if err != nil || row.ClusterID != clusterID {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Invalid ArgoCD cluster proxy token")
				return
			}
			_ = queries.TouchArgoCDClusterProxyToken(r.Context(), row.ID)
			next.ServeHTTP(w, r)
		})
	}
}

func requireStreamTicketOrAuth(jwt *iauth.JWTManager, queries appmiddleware.TokenUserQuerier, tickets *iauth.StreamTicketStore, kind string, clusterParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var clusterID uuid.UUID
			if strings.TrimSpace(clusterParam) != "" {
				var err error
				clusterID, err = uuid.Parse(chi.URLParam(r, clusterParam))
				if err != nil {
					writeRouteAuthError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
					return
				}
			}
			userID, ok := iauth.AuthorizeStreamRequestWithTickets(r, queries, jwt, tickets, kind, clusterID)
			if !ok {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
				return
			}
			if userID != uuid.Nil {
				r = r.WithContext(appmiddleware.SetAuthenticatedUserForTest(r.Context(), &appmiddleware.AuthenticatedUser{
					ID:         userID.String(),
					AuthMethod: "stream_ticket",
				}))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func writeRouteAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func isMutatingK8sProxyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func isK8sProxyWatchRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	// Match the apiserver's own ?watch parsing (strconv.ParseBool: TRUE/t/T/1…),
	// not just "true"/"1" — otherwise ?watch=TRUE is misclassified as a unary
	// LIST, admitted by the namespace-scoped list gate, and forwarded as a watch
	// (a scoped-RBAC bypass). See isWatchRequest in the tunnel package.
	if v := r.URL.Query().Get("watch"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil && b {
			return true
		}
	}
	if strings.Contains(r.Header.Get("Accept"), "stream=watch") {
		return true
	}
	return strings.Contains(r.URL.Path, "/watch/")
}

// isHighRiskPodProxySubresourceRef reports whether the parsed object ref is a
// pod exec/attach/portforward subresource. Unlike the legacy path matcher
// below, it relies on the structured subresource field, so it is robust to:
//   - core (api/v1) vs apis prefix shape (parseK8sProxyObjectRef normalises
//     both into resource/name/subresource),
//   - trailing path segments after the subresource (proxied apiserver
//     subresource URLs do not carry further segments, but a trailing slash or
//     query no longer defeats detection),
//   - singular vs plural resource spelling.
//
// This is the F1 (M2) fix: detection must never miss, because a missed
// exec/attach/portforward would degrade to a generic pod *write* verb and
// bypass the dedicated pods:exec gate.
func isHighRiskPodProxySubresourceRef(ref map[string]string) bool {
	if len(ref) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ref["resource"])) {
	case "pods", "pod":
	default:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ref["subresource"])) {
	case "exec", "attach", "portforward":
		return true
	default:
		return false
	}
}

func isHighRiskPodProxySubresource(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+6 < len(segments); i++ {
		if segments[i] != "api" || segments[i+1] != "v1" || segments[i+2] != "namespaces" || segments[i+4] != "pods" {
			continue
		}
		switch segments[i+6] {
		case "exec", "attach", "portforward":
			return i+7 == len(segments)
		}
	}
	return false
}

func registerProtectedRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	// Domain register funcs are invoked in the SAME source order the
	// route blocks previously appeared inline, so chi mount/registration
	// order (and therefore the route surface) is unchanged. The Migration-044
	// scope closures (writeClusters/writeProjects/writeRBAC/mutationWriteScope)
	// are pure stateless constructors recreated locally inside each domain
	// func that needs them.
	registerClusterRoutes(r, deps)
	registerClusterAddonRoutes(r, deps)
	registerProjectRoutes(r, deps)
	registerDashboardRoutes(r, deps)
	registerToolsControlPlaneRoutes(r, deps)
	registerRBACAuditAgentRoutes(r, deps)
	registerAlertInhibitionRoutes(r, deps)
	registerGatekeeperConstraintRoutes(r, deps)
	registerMonitoringRoutes(r, deps)
	registerResourcesWorkloadsRoutes(r, deps)
	registerSecurityRoutes(r, cfg, deps, rateLimit)
	registerDexRoutes(r, deps)
}

// remoteV2PodsHandler is the demonstration endpoint for the new
// remotedialer-based tunnel. It looks up the cluster row by id (so callers
// can use either cluster.id UUID or — if we later choose — a name lookup),
// builds a client-go clientset whose transport is dialed through the WS
// tunnel, and lists pods in the requested namespace.
//
// Returns 503 if the agent is not currently connected.
func remoteV2PodsHandler(cfg *config.Config, deps RouterDependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID := chi.URLParam(r, "id")
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}

		// This route is hard-gated out of production (see route registration).
		// The v2 transport verifies the apiserver cert against the in-cluster
		// CA bundle by default; if that bundle is not provisioned in this
		// (non-production) environment, fall back to the explicit, loudly
		// logged insecure opt-in. Validate() refuses Insecure when Production
		// is true, so this can never graduate InsecureSkipVerify into prod.
		client, err := remoteproxy.K8sClientWithOptions(deps.RemoteServer, clusterID, remoteproxy.TLSOptions{
			Insecure:   true,
			Production: isProductionConfig(cfg),
		})
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
		if _, ok := handler.RequireSuperuser(w, r, deps.AuthQueries, handler.SuperuserGateConfig{
			StoreUnavailableStatus:  http.StatusInternalServerError,
			StoreUnavailableCode:    "internal_error",
			StoreUnavailableMessage: "User store not configured",
			ForbiddenMessage:        "Key status requires superuser privileges",
		}); !ok {
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

// newLocalClusterResolver returns an appmiddleware.LocalClusterResolver that
// finds the management ("local") cluster id the same way
// localClusterArgoCDTokenSource does — by scanning the cluster list for the
// is_local row. The id never changes once bootstrapped, so the first non-nil
// result is cached for the process lifetime; until then each call re-queries
// (the local-cluster row is created after the router is built). Returns nil
// when no querier is wired, which leaves the ArgoCD authz check at global
// scope (only global/superuser grants pass — still fail-closed).
func newLocalClusterResolver(queries *sqlc.Queries) appmiddleware.LocalClusterResolver {
	if queries == nil {
		return nil
	}
	var (
		mu     sync.Mutex
		cached uuid.UUID
	)
	return func(ctx context.Context) (uuid.UUID, error) {
		mu.Lock()
		if cached != uuid.Nil {
			id := cached
			mu.Unlock()
			return id, nil
		}
		mu.Unlock()
		clusters, err := queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 200, Offset: 0})
		if err != nil {
			return uuid.Nil, err
		}
		for _, c := range clusters {
			if c.IsLocal {
				mu.Lock()
				cached = c.ID
				mu.Unlock()
				return c.ID, nil
			}
		}
		return uuid.Nil, nil
	}
}
