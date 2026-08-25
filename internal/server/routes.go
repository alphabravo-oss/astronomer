package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/handler/remoteproxy"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
)

// RouterDependencies contains the optional dependencies used to register API routes.
type RouterDependencies struct {
	JWT             *iauth.JWTManager
	Encryptor       *iauth.Encryptor
	AuthQueries     appmiddleware.TokenUserQuerier
	AuditWriter     any
	PlatformHealth  *handler.PlatformHealthHandler
	AdminQueues     *handler.AdminQueuesHandler
	AdminTaskOutbox *handler.AdminTaskOutboxHandler
	AdminDrill      *handler.AdminDrillHandler
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
	// NetworkPolicies owns /api/v1/admin/network-policy-templates/* (CRUD)
	// and /api/v1/clusters/{cluster_id}/network-policies/applications/*
	// (per-cluster apply/list/delete) — migration 068. Nil-safe.
	NetworkPolicies *handler.NetworkPolicyHandler
	// Gatekeeper owns /api/v1/clusters/{id}/gatekeeper/constraints/* (P-04):
	// custom ConstraintTemplate/Constraint authoring, validate + server-side
	// apply through the tunnel, and authored-record CRUD.
	Gatekeeper *handler.GatekeeperConstraintsHandler
	Projects   *handler.ProjectHandler
	// Delivery handlers own the project-scoped Flux-native control-plane
	// surface. Inventory also serves the separately authorized platform-wide
	// compatibility projection.
	// Nil-safe so partial test/bootstrap routers simply omit these routes.
	DeliverySources     *deliveryhandler.SourceHandler
	DeliveryBundles     *deliveryhandler.BundleHandler
	DeliveryTargets     *deliveryhandler.TargetHandler
	DeliveryRollouts    *deliveryhandler.RolloutHandler
	DeliveryDeployments *deliveryhandler.DeploymentHandler
	DeliveryInventory   *deliveryhandler.InventoryHandler
	DeliverySystem      *deliveryhandler.SystemRolloutHandler
	Tools               *handler.ToolHandler
	Audit               *handler.AuditHandler
	Alerting            *handler.AlertingHandler
	Anomaly             *handler.AnomalyHandler
	Backups             *handler.BackupHandler
	Catalog             *handler.CatalogHandler
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
	// ClusterAgent exposes read-only fleet inventory for connected and
	// disconnected adopted-cluster agents.
	ClusterAgent *handler.ClusterAgentHandler
	// ApiserverAudit ingests kube-apiserver audit events streamed by the
	// per-cluster agent and exposes them for operator read-back
	// (migration 112). Nil-safe — when unwired the routes are omitted.
	ApiserverAudit *handler.ApiserverAuditHandler
	// Readyz exposes control-plane dependency readiness checks.
	Readyz http.Handler
	// DexConfig owns CRUD for Dex connectors / settings and renders the
	// running Dex instance's ConfigMap (Phase B4 of the Rancher-parity plan).
	DexConfig *handler.DexHandler
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
	// CharlieOnboarding owns the local-only signed package validation and
	// consumption endpoints. It is nil unless database encryption and the
	// in-cluster Kubernetes Secret writer are both available.
	CharlieOnboarding *handler.CharlieOnboardingHandler
	// CharlieAdmin owns the fail-closed connection, agent, mode, automation,
	// access-preview, and diagnostics control plane. It never serves runtime
	// evidence and is absent unless the local database is available.
	CharlieAdmin *handler.CharlieAdminHandler
	// CharlieSessions is the browser-only, live-authorized proxy for private
	// Charlie chat. Nil keeps every route absent when the optional runtime is
	// not fully wired.
	CharlieSessions *handler.CharlieSessionHandler
	// CharlieThreads is the durable interactive conversation pointer (one active
	// thread per user). Sessions under a thread remain authorized agent runs.
	CharlieThreads *handler.CharlieThreadHandler
	// CharlieApprovals is the browser-facing product authority gate for exact,
	// signed, single-use write approvals.
	CharlieApprovals *handler.CharlieApprovalHandler
	// CharlieContext exposes only live-authorized, bounded product resource
	// identifiers and labels for the explicit chat context picker.
	CharlieContext *handler.CharlieContextHandler
	// CharlieFindings exposes bounded local notification summaries and proxies
	// central detail only after live product authorization. Nil keeps the
	// optional surface absent.
	CharlieFindings *handler.CharlieFindingHandler
	// CharlieOperations exposes bounded durable action-receipt status only
	// after the same live session authorization used for history and findings.
	CharlieOperations *handler.CharlieOperationHandler
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

const charlieAdminReconciliationTimeout = 7 * time.Minute

func longRunningCharlieAdminMutation(method, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if method == http.MethodPut && path == "/api/v1/admin/charlie/kubernetes-visibility" {
		return true
	}
	if method != http.MethodPost && method != http.MethodPatch {
		return false
	}
	switch path {
	case "/api/v1/admin/charlie/onboarding/consume",
		"/api/v1/admin/charlie/disconnect",
		"/api/v1/admin/charlie/mode":
		return true
	default:
		return false
	}
}

func apiRequestTimeout(duration time.Duration) func(http.Handler) http.Handler {
	bounded := chimiddleware.Timeout(duration)
	reconciliation := chimiddleware.Timeout(charlieAdminReconciliationTimeout)
	return func(next http.Handler) http.Handler {
		timed := bounded(next)
		longTimed := reconciliation(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimSuffix(r.URL.Path, "/")
			if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/charlie/sessions/") && strings.HasSuffix(path, "/events") {
				next.ServeHTTP(w, r)
				return
			}
			// Charlie installation, replacement, Kubernetes visibility, and mode
			// transitions synchronously verify a delivery reconciliation and a
			// two-replica StatefulSet rollout. The ordinary REST deadline can
			// expire after Kubernetes accepted the least-authority ceiling but
			// before the audited database transition commits. Keep these exact
			// administrator mutations bounded, but give the configured five-minute
			// rollout enough time plus a final bridge readback. Every operation is
			// revision-checked and idempotent, so a disconnected client can retry.
			if longRunningCharlieAdminMutation(r.Method, path) {
				longTimed.ServeHTTP(w, r)
				return
			}
			timed.ServeHTTP(w, r)
		})
	}
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
	r.Use(appmiddleware.TrustedRealIP(cfg.TrustedProxyCIDRs))
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(appmiddleware.RequestLogger)
	r.Use(chimiddleware.Recoverer)
	r.Use(appmiddleware.Metrics)
	// Keep the server-level ReadTimeout at zero for WebSockets/SSE while still
	// bounding every REST/SCIM mutation body by bytes and socket read time.
	r.Use(appmiddleware.BoundRequestBodies(appmiddleware.DefaultMaxRequestBodyBytes, appmiddleware.DefaultRequestBodyTimeout))
	// Normalise `/api/v1/foo` → `/api/v1/foo/` before chi matches so
	// the frontend's no-trailing-slash REST calls hit the same route
	// the trailing-slash form does. Without this, DELETE /clusters/{id}
	// 404s because the route is mounted as /{id}/ — the user-facing
	// symptom is "the cluster delete button in the UI silently fails."
	// Scoped to /api/v1/* so static helm-repo assets are not
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

	registerPublicRoutes(r, cfg, deps)
	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// REST-only timeout. Charlie's authenticated event stream is explicitly
		// exempt because chi's timeout writer cannot expose http.Flusher and
		// would both break SSE and terminate healthy turns at 30 seconds.
		r.Use(apiRequestTimeout(30 * time.Second))
		// /bootstrap/ and /bootstrap/complete/ were removed when the server
		// switched to the Rancher-style admin-on-first-boot model: the
		// startup hook in cmd/server/main.go (auth.EnsureBootstrapAdmin)
		// creates the admin user. No HTTP endpoint is needed for platform
		// first-setup any more.

		registerAPIEntryRoutes(r, cfg, deps)
		authenticated := r
		if deps.JWT != nil {
			authenticated = chi.NewRouter()
			if deps.RemoteQueries != nil {
				// Authentication failures return before authenticated middleware
				// can observe the request. Record only Charlie's unauthenticated
				// mutation denials here, using its content-free audit contract.
				authenticated.Use(appmiddleware.CharlieAuthenticationDenialAuditWithWriter(slog.Default(), deps.RemoteQueries))
			}
			authenticated.Use(appmiddleware.RequireAuthWithQueries(deps.JWT, deps.AuthQueries))
			if deps.RemoteQueries != nil {
				// Keep the normal mutation auditor immediately after auth so it
				// receives actor context and still observes write-scope/RBAC denials.
				authenticated.Use(appmiddleware.AuditLogWithWriter(slog.Default(), deps.RemoteQueries))
			}
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

	registerLongLivedRoutes(r, cfg, deps, rateLimit)
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

// requireQueryNamespacePermission is requirePermission for a route whose
// handler narrows its own upstream query to ?namespace=. Gate namespace ==
// handler namespace, so naming one can only shrink the result set. Everything
// else must use requirePermission, which ignores the query and therefore fails
// closed for a namespace-narrowed caller. See
// appmiddleware.RequireQueryNamespacePermission.
func requireQueryNamespacePermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return appmiddleware.RequireQueryNamespacePermission(engine, querier, resource, verb)
}

// requireCollectionPermission gates a top-level collection route (GET
// /clusters/, GET /projects/), admitting callers whose grant is cluster- or
// project-scoped instead of global. The handler behind it filters the page —
// see the RequireCollectionPermission doc for why the gate alone is not enough.
func requireCollectionPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return appmiddleware.RequireCollectionPermission(engine, querier, resource, verb)
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
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			for _, requirement := range requirements {
				if engine.CheckPermission(bindings, requirement.resource, requirement.verb, clusterID, projectID) {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
		})
	}
}

// permissionScopeIDs is the SECOND scope resolver in this codebase and its rule
// is knowingly different from the first. Read appmiddleware.permissionScope
// (internal/server/middleware/rbac.go) before touching either.
//
// The difference: permissionScope only falls back to a bare {id} when the route
// subtree declared that {id} names a cluster (ClusterScopeFromIDParam) or the
// gated resource is clusters/projects. This one falls back UNCONDITIONALLY and
// binds the same {id} as BOTH the cluster and the project scope. Its callers —
// requireAnyPermission, requireK8sProxyPermission and the workloads gate in
// routes_resources_workloads.go — sit on routes where {id} is sometimes neither:
// /admin/alerting/inhibitions/{id}/ binds an inhibition id as a cluster AND a
// project, and /clusters/{id}/v2/pods/ binds the cluster uuid as a project id
// too.
//
// TODO(authz-scope-resolvers): collapse this onto permissionScope, passing the
// gated resource (or a nil resource meaning "infer nothing"). It is left as-is
// deliberately rather than silently: the rule is WRONG but it fails CLOSED at
// every live call site, because rbac.bindingApplies only matches a project
// binding whose ProjectID equals the bound value, and no project id equals a
// cluster or inhibition uuid — the ids come from different tables. A collision
// there is the exploit, and unifying the two resolvers is what removes it. The
// same divergence between two resolvers one file apart is how the monitoring
// scope bug survived from 016fdbb to 686b794.
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
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
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
			// F1 (M5): a mutating nodes/{name}/proxy request reaches the
			// kubelet's own HTTP surface, whose /run/ and /exec/ endpoints run
			// arbitrary commands in any container on the node. That is pod exec
			// by another name, so it must satisfy pods:exec IN ADDITION to
			// nodes:proxy. A single CheckPermission call cannot express AND, so
			// the conjunct lives here. Node paths are cluster-scoped (namespace
			// is empty), so this requires a cluster-wide pods:exec grant — and
			// deliberately not the native allow layer, which refuses to widen
			// exec at all.
			if resource == rbac.ResourceNodes && verb == rbac.VerbProxy && isMutatingK8sProxyMethod(r.Method) &&
				!engine.CheckPermission(bindings, rbac.ResourcePods, rbac.VerbExec, clusterID, projectID, namespace) {
				writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
				return
			}
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
					// is a cluster-wide LIST or WATCH (GET, VerbList|VerbWatch,
					// namespace=="") for which the user holds the (resource,
					// list) permission in at least one namespace on this cluster,
					// admit the request and stash the authorized-namespace
					// allow-set. The tunnel proxy filters list bodies and watch
					// event frames down to those namespaces (F7-b). Namespaced
					// paths were already authorized above by CheckPermission;
					// mutations, named GETs, and users with no namespace access
					// keep failing closed here.
					//
					// Watch requests use VerbWatch for CheckPermission but we
					// compute the allow-set with VerbList: namespace bindings
					// commonly grant list+read without an explicit watch verb,
					// and list is the correct predicate for "which namespaces'
					// objects may appear on the stream".
					if namespaceScoped &&
						r.Method == http.MethodGet &&
						(verb == rbac.VerbList || verb == rbac.VerbWatch) &&
						namespace == "" {
						allowVerb := verb
						if verb == rbac.VerbWatch {
							allowVerb = rbac.VerbList
						}
						all, names := engine.AuthorizedNamespaces(bindings, resource, allowVerb, clusterID)
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
					writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
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
	return auditK8sProxyMutationsWithAction(auditWriter, "cluster.k8s_proxy", nil)
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

func auditK8sProxyMutationsWithAction(auditWriter any, action string, extraDetail map[string]any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutatingK8sProxyMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			clusterID := chi.URLParam(r, "cluster_id")
			k8sPath := "/" + strings.Trim(chi.URLParam(r, "*"), "/")
			detail := k8sProxyAuditDetail(r.Method, k8sPath, extraDetail)
			resourceName := k8sProxyAuditResourceName(detail)

			// A remote member-cluster mutation cannot share a PostgreSQL
			// transaction with the local audit row. Persist a content-free intent
			// synchronously before entering the tunnel instead. If PostgreSQL is
			// unavailable, fail closed before any remote effect is possible.
			intentDetail := cloneStringAnyMap(detail)
			intentDetail["phase"] = "intent"
			intentDetail["terminal_state"] = "unknown_until_outcome"
			intentDetail["reconcile_if_outcome_missing"] = true
			if err := recordMandatoryK8sProxyAudit(r.Context(), r, auditWriter, action+".intent", clusterID, resourceName, 0, 0, intentDetail); err != nil {
				slog.Default().Error("mandatory kubernetes proxy audit intent failed",
					"cluster_id", clusterID,
					"method", r.Method,
					"error", err,
				)
				writeRouteAuthError(w, http.StatusServiceUnavailable, "audit_unavailable", "Mandatory audit storage is unavailable; the Kubernetes mutation was not forwarded")
				return
			}

			started := time.Now()
			wrapped := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}

			// The response may already have been committed, so an outcome-write
			// failure cannot truthfully replace it with a 503 or roll back the
			// member-cluster effect. The durable intent remains the repair signal;
			// emit a safe operational error and preserve status/body compatibility.
			outcomeDetail := cloneStringAnyMap(detail)
			outcomeDetail["phase"] = "outcome"
			outcomeDetail["outcome"] = k8sProxyAuditOutcome(status)
			outcomeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			if err := recordMandatoryK8sProxyAudit(outcomeCtx, r, auditWriter, action+".outcome", clusterID, resourceName, status, time.Since(started).Milliseconds(), outcomeDetail); err != nil {
				slog.Default().Error("mandatory kubernetes proxy audit outcome failed; durable intent requires reconciliation",
					"cluster_id", clusterID,
					"method", r.Method,
					"status_code", status,
					"error", err,
				)
			}
		})
	}
}

func k8sProxyAuditDetail(method, k8sPath string, extra map[string]any) map[string]any {
	detail := map[string]any{"method": method}
	// Only parsed, allow-listed object coordinates are retained. In
	// particular, never copy the request body, raw YAML/JSON, headers, or raw
	// query string into compliance evidence.
	if ref := parseK8sProxyObjectRef(k8sPath); len(ref) > 0 {
		for _, key := range []string{"api_group", "api_version", "namespace", "resource", "name", "subresource"} {
			if value := strings.TrimSpace(ref[key]); value != "" {
				detail[key] = value
			}
		}
	}
	// Historical callers may add the safe logical proxy identifier. Do not
	// accept arbitrary keys here: this boundary must never become a path for a
	// manifest or credential-bearing request fragment to reach the audit log.
	if proxy, ok := extra["proxy"].(string); ok && strings.TrimSpace(proxy) != "" {
		detail["proxy"] = strings.TrimSpace(proxy)
	}
	return detail
}

func k8sProxyAuditResourceName(detail map[string]any) string {
	resource, _ := detail["resource"].(string)
	name, _ := detail["name"].(string)
	if resource == "" {
		return "kubernetes-api"
	}
	if name == "" {
		return resource
	}
	return resource + "/" + name
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func k8sProxyAuditOutcome(status int) string {
	switch {
	case status >= 200 && status < 400:
		return "completed"
	case status >= 400 && status < 500:
		return "rejected"
	default:
		return "failed"
	}
}

func recordMandatoryK8sProxyAudit(ctx context.Context, r *http.Request, writer any, action, clusterID, resourceName string, status int, durationMS int64, detail map[string]any) error {
	v1, ok := writer.(audit.Querier)
	if !ok || v1 == nil || r == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}

	var userID uuid.UUID
	authMethod := ""
	if user, ok := appmiddleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		userID, _ = uuid.Parse(user.ID)
		authMethod = user.AuthMethod
	}
	return audit.RecordMandatory(ctx, v1, audit.Event{
		Source:          "service",
		CorrelationID:   appmiddleware.GetCorrelationID(r.Context()),
		UserID:          audit.UserIDFromUUID(userID),
		ActorAuthMethod: authMethod,
		Action:          action,
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    resourceName,
		StatusCode:      int32(status),
		DurationMs:      durationMS,
		RequestID:       appmiddleware.GetRequestID(r.Context()),
		IPAddress:       appmiddleware.RemoteIPAddr(r),
		HTTPMethod:      r.Method,
		// Store the stable route template, not the raw path. Parsed object
		// coordinates above are sufficient for operators, while a raw
		// subresource/proxy suffix can contain arbitrary user-controlled text.
		Path:   "/api/v1/clusters/{cluster_id}/k8s/*",
		Detail: detail,
	})
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

	// F1 (M5): the apiserver's `proxy` subresource tunnels an arbitrary request
	// to the target's OWN endpoint, which is a different capability from
	// reading or writing the target object: nodes/{name}/proxy reaches the
	// kubelet (including /run/<ns>/<pod>/<container>, i.e. command execution in
	// any container on the node), pods/{name}/proxy and services/{name}/proxy
	// reach the workload's port directly. Without this branch those requests
	// degrade to the target's generic read/update verb and the dedicated
	// `proxy` verb the role catalog already grants gates nothing.
	// parseK8sProxyObjectRef drops every segment after the subresource, so
	// /nodes/n1/proxy and /nodes/n1/proxy/run/... both land here. Decide it
	// BEFORE the pods/log branch and the k8sProxyResourcePolicy fallthrough.
	// The extra pods:exec conjunct for node proxy lives in
	// requireK8sProxyPermission — one permission pair cannot express AND.
	if strings.ToLower(strings.TrimSpace(ref["subresource"])) == "proxy" {
		if resource, ok := knownK8sProxyResource(ref["resource"]); ok {
			return resource, rbac.VerbProxy
		}
		if resource, ok := k8sProxyResourcePolicy(ref); ok {
			return resource, rbac.VerbProxy
		}
		// Unknown core-group resource with a proxy subresource: fail closed on
		// clusters:proxy (which no template grants) rather than degrading to
		// the generic clusters read/update verb.
		return rbac.ResourceClusters, rbac.VerbProxy
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
	// Keep in lockstep with rbac.isPrivilegeEscalationGroup (native rules):
	// CSR minting and TokenReview/TokenRequest are cluster-admin equivalent
	// and must not fall through to custom_resources write (SEC-04).
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"apiextensions.k8s.io",
		"certificates.k8s.io",
		"authentication.k8s.io":
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
	if strings.EqualFold(r.URL.Query().Get("force"), "true") && r.URL.Query().Get("dryRun") == "" {
		return rbac.VerbManage
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

// requireGenericResourceListPermission gates the generic list route.
// ResourceHandler.ListGenericResources builds its upstream path from the same
// ?namespace=, so the query-scoped gate is the honest one here.
func requireGenericResourceListPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbList)
			requireQueryNamespacePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// requireNamedResourcePermission gates the typed resource routes on the
// resource implied by the {resource_type}/{type} route param. The namespace
// comes from the route only: the named GET/PUT/DELETE forms carry {namespace},
// and the collection forms are cluster-wide unless mounted through
// requireNamedResourceListPermission / requireNamedResourceCreatePermission.
func requireNamedResourcePermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, routeParam string, requestedVerb rbac.Verb) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, routeParam), requestedVerb)
			requirePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// requireNamedResourceListPermission gates
// GET /clusters/{cluster_id}/resources/{resource_type}/, whose handler
// (ResourceHandler.ListNamedResources) builds /api/v1/namespaces/<ns>/<type>
// from the same ?namespace=. Gate namespace == handler namespace.
func requireNamedResourceListPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbList)
			requireQueryNamespacePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// createBodyMaxBytes caps how much of a create body the namespace gate below
// will buffer. Namespaced manifests posted through this route are small; a
// larger body is refused outright rather than authorized on a partial parse.
const createBodyMaxBytes = 1 << 20

// requireNamedResourceCreatePermission gates
// POST /clusters/{cluster_id}/resources/{resource_type}/ on the namespace
// inside the REQUEST BODY.
//
// SECURITY: ResourceHandler.CreateNamedResource derives its target namespace
// from metadata.namespace (resourceNamespace(body)), never from the URL. Gating
// that route on a URL-supplied namespace authorized one namespace while the
// handler wrote to another — a project member holding services/ingresses in
// their own namespace could create an Ingress (hostname + TLS hijack) or a
// NetworkPolicy (tenant DoS) anywhere on the cluster. So the gate parses the
// same field the handler will use. A body that names no namespace is refused:
// the upstream path would then be cluster-wide/implicit, which no
// namespace-narrowed grant covers.
func requireNamedResourceCreatePermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, createBodyMaxBytes+1))
			if err != nil || len(body) > createBodyMaxBytes {
				writeRouteAuthError(w, http.StatusRequestEntityTooLarge, "invalid_body", "Request body is too large")
				return
			}
			// Hand the handler back an identical, re-readable body.
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			namespace := k8sManifestNamespace(body)
			if namespace == "" {
				writeRouteAuthError(w, http.StatusBadRequest, "invalid_body", "metadata.namespace is required")
				return
			}
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbCreate)
			appmiddleware.RequirePermissionForNamespace(engine, querier, resource, verb,
				func(*http.Request) string { return namespace })(next).ServeHTTP(w, r)
		})
	}
}

// k8sManifestNamespace pulls metadata.namespace out of a JSON manifest. It
// mirrors handler.resourceNamespace; a body that does not parse yields "" and
// the caller refuses the request.
func k8sManifestNamespace(body []byte) string {
	var payload struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Metadata.Namespace)
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
	registerDeliveryRoutes(r, deps)
	registerDashboardRoutes(r, deps)
	registerToolsControlPlaneRoutes(r, deps)
	registerRBACAuditAgentRoutes(r, deps)
	registerAlertInhibitionRoutes(r, deps)
	registerGatekeeperConstraintRoutes(r, deps)
	registerMonitoringRoutes(r, deps)
	registerResourcesWorkloadsRoutes(r, deps)
	registerSecurityRoutes(r, cfg, deps, rateLimit)
	registerDexRoutes(r, deps)
	registerCharlieRoutes(r, deps, rateLimit)
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
// keys, plus any credential still set to a published development sentinel
// (dev-keys-default-and-silent) so the UI can show a red banner. The runbook
// (docs/secret-rotation-runbook.md) tells operators to poll this during a
// rotation to confirm the new key is in fact loaded and that the old key has
// been dropped at the end of the procedure.
//
// Auth: superuser only — the count itself is harmless, but the diagnostic
// is intended for the operator running the rotation, not the general user
// population.
func keyStatusHandler(cfg *config.Config, deps RouterDependencies) http.HandlerFunc {
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
		insecureDevKeys := config.DevSentinelsInUse(cfg)
		if insecureDevKeys == nil {
			insecureDevKeys = []string{}
		}

		// Read-only superuser endpoint that exposes the live key-rotation
		// state — leave an explicit audit trail. The mutating-HTTP audit
		// middleware skips GET, so this trail wouldn't otherwise exist.
		handler.RecordAuditFromRequest(r, deps.AuthQueries, "admin.key_status.viewed",
			"platform", "", "key-status", map[string]any{
				"encryption_keys":   encKeys,
				"jwt_keys":          jwtKeys,
				"insecure_dev_keys": insecureDevKeys,
			})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"encryption_keys":   encKeys,
			"jwt_keys":          jwtKeys,
			"insecure_dev_keys": insecureDevKeys,
			"as_of":             time.Now().UTC().Format(time.RFC3339),
		})
	}
}
