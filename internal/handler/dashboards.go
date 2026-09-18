// Package handler — migration 058: dashboard widgets.
//
// Operators define small widgets — Grafana panel iframes, Prometheus
// sparklines, simple stat panels, or raw URL iframes — and pin them
// to one of three scopes: `global`, `cluster`, `project`. This file
// owns the REST surface:
//
//	Admin (superuser):
//	  GET    /api/v1/admin/dashboard-widgets/
//	  POST   /api/v1/admin/dashboard-widgets/
//	  GET    /api/v1/admin/dashboard-widgets/{id}/
//	  PUT    /api/v1/admin/dashboard-widgets/{id}/
//	  DELETE /api/v1/admin/dashboard-widgets/{id}/
//	  GET    /api/v1/admin/prometheus-datasources/
//	  POST   /api/v1/admin/prometheus-datasources/
//	  PUT    /api/v1/admin/prometheus-datasources/{id}/
//	  DELETE /api/v1/admin/prometheus-datasources/{id}/
//	  POST   /api/v1/admin/prometheus-datasources/{id}/test/
//
//	Public render (cluster:read scoped — wired through the route
//	layer's RequirePermission middleware):
//	  GET /api/v1/dashboards/global/
//	  GET /api/v1/dashboards/clusters/{id}/
//	  GET /api/v1/dashboards/projects/{id}/
//
// Render semantics:
//   - Grafana panel widgets DO NOT fetch data server-side. The iframe
//     URL is templated against the cluster's `cluster_uid` (when
//     applicable) and shipped to the client; the browser loads the
//     panel directly. This is intentional — Grafana already enforces
//     its own auth (operator must be logged into Grafana via the
//     same SSO).
//   - Prom sparkline + stat widgets are rendered server-side. A 30s
//     in-process cache shares one upstream fetch across concurrent
//     client polls.
//   - URL iframe is the escape hatch. Its URL is templated against
//     {{cluster_uid}} (and a {{project_id}} placeholder for project
//     scope) before shipping to the client.
//
// Iframe host allow-list (security): grafana_panel + url_iframe specs
// must point at a host listed in the dashboard.allowed_iframe_hosts
// platform setting (comma-separated, registered into the settings
// registry). Empty list = no iframe widgets render; operator must opt
// in by populating the setting. The handler returns 400 on Create /
// Update for widgets that violate this; the render path silently
// drops violators (defensive against a setting that's tightened
// after-the-fact).
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// DashboardQuerier is the narrow DB surface the handler reads + writes.
// *sqlc.Queries satisfies it; tests pass narrow fakes.
type DashboardQuerier interface {
	ListDashboardWidgetsPage(ctx context.Context, arg sqlc.ListDashboardWidgetsPageParams) ([]sqlc.DashboardWidget, error)
	CountDashboardWidgets(ctx context.Context) (int64, error)
	GetDashboardWidgetByID(ctx context.Context, id uuid.UUID) (sqlc.DashboardWidget, error)
	CreateDashboardWidget(ctx context.Context, arg sqlc.CreateDashboardWidgetParams) (sqlc.DashboardWidget, error)
	UpdateDashboardWidget(ctx context.Context, arg sqlc.UpdateDashboardWidgetParams) (sqlc.DashboardWidget, error)
	DeleteDashboardWidget(ctx context.Context, id uuid.UUID) error
	ListWidgetsForScope(ctx context.Context, arg sqlc.ListWidgetsForScopeParams) ([]sqlc.DashboardWidget, error)
	ListPrometheusDatasourcesPage(ctx context.Context, arg sqlc.ListPrometheusDatasourcesPageParams) ([]sqlc.ListPrometheusDatasourcesPageRow, error)
	CountPrometheusDatasources(ctx context.Context) (int64, error)
	ListEnabledPrometheusDatasources(ctx context.Context) ([]sqlc.PrometheusDatasource, error)
	GetPrometheusDatasourceByID(ctx context.Context, id uuid.UUID) (sqlc.PrometheusDatasource, error)
	GetPrometheusDatasourceByName(ctx context.Context, name string) (sqlc.PrometheusDatasource, error)
	CreatePrometheusDatasource(ctx context.Context, arg sqlc.CreatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error)
	UpdatePrometheusDatasource(ctx context.Context, arg sqlc.UpdatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error)
	DeletePrometheusDatasource(ctx context.Context, id uuid.UUID) error
	GetClusterUIDForID(ctx context.Context, id uuid.UUID) (string, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// DashboardMutationTx is the transaction-bound state + audit surface for
// widget and Prometheus datasource administration. Production supplies
// sqlc.New(tx), ensuring a successful API response cannot describe state whose
// mandatory audit intent failed to commit (or vice versa).
type DashboardMutationTx interface {
	DashboardQuerier
	audit.OutboxQuerier
}

type dashboardRunTxFunc func(context.Context, func(DashboardMutationTx) error) error

// DashboardHandler owns the admin CRUD + the public render endpoints.
type DashboardHandler struct {
	queries   DashboardQuerier
	runTx     dashboardRunTxFunc
	auditor   any
	encryptor *auth.Encryptor
	cache     *dashboards.Cache
	// settingsCache lets the handler read dashboard.allowed_iframe_hosts
	// at render time without hitting the DB on every request.
	settingsCache *SettingsCache
}

// NewDashboardHandler wires the handler with just the queries surface.
// All optional dependencies (auditor, encryptor, settings cache) are
// attached via Set* methods so server.go can compose them
// progressively without forcing every caller to thread the same
// dependencies.
func NewDashboardHandler(queries DashboardQuerier) *DashboardHandler {
	c := dashboards.NewCache(30 * time.Second)
	// Bind the cache's hit / miss hooks into the package-level counters
	// so the dashboard-load load-shedding behaviour is observable in
	// the metrics surface alongside the renders/duration histograms.
	c.SetMetrics(
		func() { dashboardPromCacheHitTotal.Inc() },
		func() { dashboardPromCacheMissTotal.Inc() },
	)
	return &DashboardHandler{
		queries: queries,
		cache:   c,
	}
}

// SetRunTx wires the production database transaction used to commit each
// dashboard state mutation and its audit outbox intent atomically.
func (h *DashboardHandler) SetRunTx(runTx dashboardRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *DashboardHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetAuditor wires the audit writer. Argument type is `any` because
// recordAudit type-asserts internally — see audit_helpers.go.
func (h *DashboardHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

// SetEncryptor wires the Fernet encryptor used to seal datasource
// auth secrets at rest. Optional: when nil the auth_encrypted column
// is written empty (no auth) and the /test/ endpoint returns 503
// not_configured for any datasource with stored auth.
func (h *DashboardHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetSettingsCache wires the shared platform-settings cache the
// render path uses to read dashboard.allowed_iframe_hosts. Optional —
// when nil, the registry's default (empty allow-list) applies and
// iframe widgets are rejected on write.
func (h *DashboardHandler) SetSettingsCache(c *SettingsCache) {
	if h == nil {
		return
	}
	h.settingsCache = c
}

// ── Metrics ────────────────────────────────────────────────────────────

var (
	dashboardWidgetRendersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "dashboard_widget_renders_total",
			Help:      "Server-side widget render outcomes by widget_type and outcome (ok/error/no_data).",
		},
		observability.MetricLabels("type", "outcome"),
	)
	dashboardPromCacheHitTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "dashboard_prom_cache_hit_total",
			Help:      "In-process Prometheus query cache hits across all widget renders.",
		},
	)
	dashboardPromCacheMissTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "dashboard_prom_cache_miss_total",
			Help:      "In-process Prometheus query cache misses across all widget renders.",
		},
	)
	dashboardPromQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Name:      "dashboard_prom_query_duration_seconds",
			Help:      "Prometheus query duration (cache miss path only).",
			Buckets:   prometheus.DefBuckets,
		},
		observability.MetricLabels("datasource"),
	)
)

func init() {
	prometheus.MustRegister(dashboardWidgetRendersTotal, dashboardPromCacheHitTotal, dashboardPromCacheMissTotal, dashboardPromQueryDuration)
}

// dashRenderCounter is a label-aware shorthand that prepends the
// instance_id label observability.MetricLabels stamped onto the
// CounterVec — without it the per-call sites would each need to
// duplicate the MetricValues call.
func dashRenderCounter(widgetType, outcome string) prometheus.Counter {
	return dashboardWidgetRendersTotal.WithLabelValues(observability.MetricValues(widgetType, outcome)...)
}

func dashPromDuration(datasource string) prometheus.Observer {
	return dashboardPromQueryDuration.WithLabelValues(observability.MetricValues(datasource)...)
}

// ── Admin: gate ────────────────────────────────────────────────────────

// gate is the superuser gate used by every /admin/* endpoint, mirroring
// platform_settings.gate / quotas.gate.
func (h *DashboardHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableCode:    "not_configured",
		StoreUnavailableMessage: "Dashboard store not configured",
		ForbiddenMessage:        "Dashboard administration requires superuser privileges",
	})
	return ok
}

// ── Wire DTOs ─────────────────────────────────────────────────────────

// WidgetGrid is the (x, y, w, h) sub-object on every widget response.
type WidgetGrid struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	W int32 `json:"w"`
	H int32 `json:"h"`
}

// WidgetRequest is the POST / PUT body. Spec is intentionally opaque
// (json.RawMessage) — the validator unmarshals it per widget_type and
// rejects unknown fields, but the storage layer doesn't normalise it.
// openapi:request DashboardWidgetRequest
type WidgetRequest struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	WidgetType     string          `json:"widget_type"`
	Spec           json.RawMessage `json:"spec"`
	Scope          string          `json:"scope"`
	ScopeIDs       []uuid.UUID     `json:"scope_ids"`
	Grid           WidgetGrid      `json:"grid"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Enabled        *bool           `json:"enabled,omitempty"`
}

// WidgetResponse is the GET / List / write-echo shape returned by the
// admin endpoints. The render endpoints use RenderedWidget instead.
type WidgetResponse struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	WidgetType     string          `json:"widget_type"`
	Spec           json.RawMessage `json:"spec"`
	Scope          string          `json:"scope"`
	ScopeIDs       []uuid.UUID     `json:"scope_ids"`
	Grid           WidgetGrid      `json:"grid"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Enabled        bool            `json:"enabled"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// RenderedWidget is the public-render response shape. SpecResolved
// carries the same fields as the stored Spec but with placeholders
// substituted (cluster_uid, project_id). Data carries the server-side
// fetched data when applicable.
type RenderedWidget struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	WidgetType     string          `json:"widget_type"`
	SpecResolved   json.RawMessage `json:"spec_resolved"`
	Grid           WidgetGrid      `json:"grid"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Data           *WidgetData     `json:"data,omitempty"`
}

// WidgetData is the per-widget rendered payload. SparklineSVG is the
// bytes of the rendered SVG (string, embedded). StatValue is the
// numeric scalar for stat widgets. StatOK is false when the upstream
// returned an empty result set — the client renders "—" in that case.
type WidgetData struct {
	SparklineSVG string  `json:"sparkline_svg,omitempty"`
	StatValue    float64 `json:"stat_value,omitempty"`
	StatUnit     string  `json:"stat_unit,omitempty"`
	StatFormat   string  `json:"stat_format,omitempty"`
	StatOK       bool    `json:"stat_ok,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// DatasourceRequest is the POST / PUT body for the datasource admin
// endpoints. Auth is split into Basic vs Bearer; an empty Auth section
// means "no auth".
// openapi:request PrometheusDatasourceRequest
type DatasourceRequest struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	BasicAuthUser string `json:"basic_auth_user,omitempty"`
	BasicAuthPass string `json:"basic_auth_pass,omitempty"`
	BearerToken   string `json:"bearer_token,omitempty"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

// DatasourceResponse omits the secret material entirely — the handler
// signals "auth is configured" via HasAuth.
type DatasourceResponse struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	HasAuth       bool      `json:"has_auth"`
	TLSSkipVerify bool      `json:"tls_skip_verify"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
}

// authBlob is the JSON shape inside the encrypted auth_encrypted
// column. Storing all three fields in one blob means a future "add
// scram-sha-256" doesn't need a migration.
type authBlob struct {
	BasicAuthUser string `json:"basic_auth_user,omitempty"`
	BasicAuthPass string `json:"basic_auth_pass,omitempty"`
	BearerToken   string `json:"bearer_token,omitempty"`
}

// ── Admin: widget CRUD ────────────────────────────────────────────────

// AdminList handles GET /api/v1/admin/dashboard-widgets/.
