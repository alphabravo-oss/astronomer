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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// DashboardQuerier is the narrow DB surface the handler reads + writes.
// *sqlc.Queries satisfies it; tests pass narrow fakes.
type DashboardQuerier interface {
	ListDashboardWidgets(ctx context.Context) ([]sqlc.DashboardWidget, error)
	GetDashboardWidgetByID(ctx context.Context, id uuid.UUID) (sqlc.DashboardWidget, error)
	CreateDashboardWidget(ctx context.Context, arg sqlc.CreateDashboardWidgetParams) (sqlc.DashboardWidget, error)
	UpdateDashboardWidget(ctx context.Context, arg sqlc.UpdateDashboardWidgetParams) (sqlc.DashboardWidget, error)
	DeleteDashboardWidget(ctx context.Context, id uuid.UUID) error
	ListWidgetsForScope(ctx context.Context, arg sqlc.ListWidgetsForScopeParams) ([]sqlc.DashboardWidget, error)
	ListPrometheusDatasources(ctx context.Context) ([]sqlc.PrometheusDatasource, error)
	ListEnabledPrometheusDatasources(ctx context.Context) ([]sqlc.PrometheusDatasource, error)
	GetPrometheusDatasourceByID(ctx context.Context, id uuid.UUID) (sqlc.PrometheusDatasource, error)
	GetPrometheusDatasourceByName(ctx context.Context, name string) (sqlc.PrometheusDatasource, error)
	CreatePrometheusDatasource(ctx context.Context, arg sqlc.CreatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error)
	UpdatePrometheusDatasource(ctx context.Context, arg sqlc.UpdatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error)
	DeletePrometheusDatasource(ctx context.Context, id uuid.UUID) error
	GetClusterUIDForID(ctx context.Context, id uuid.UUID) (string, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// DashboardHandler owns the admin CRUD + the public render endpoints.
type DashboardHandler struct {
	queries   DashboardQuerier
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
func (h *DashboardHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListDashboardWidgets(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]WidgetResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, widgetToResponse(row))
	}
	RespondJSON(w, http.StatusOK, out)
}

// AdminGet handles GET /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	row, err := h.queries.GetDashboardWidgetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, widgetToResponse(row))
}

// AdminCreate handles POST /api/v1/admin/dashboard-widgets/.
func (h *DashboardHandler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req WidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := h.validateWidgetRequest(r.Context(), req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	scopeIDs := req.ScopeIDs
	if scopeIDs == nil {
		scopeIDs = []uuid.UUID{}
	}
	row, err := h.queries.CreateDashboardWidget(r.Context(), sqlc.CreateDashboardWidgetParams{
		Name:           req.Name,
		Description:    req.Description,
		WidgetType:     req.WidgetType,
		Spec:           defaultJSONObject(req.Spec),
		Scope:          req.Scope,
		ScopeIds:       scopeIDs,
		GridX:          req.Grid.X,
		GridY:          req.Grid.Y,
		GridW:          defaultInt32(req.Grid.W, 4),
		GridH:          defaultInt32(req.Grid.H, 2),
		RefreshSeconds: defaultInt32(req.RefreshSeconds, 60),
		Enabled:        enabled,
		CreatedBy:      currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.dashboard_widget.created", "dashboard_widget", row.ID.String(), row.Name, map[string]any{
		"widget_type": row.WidgetType,
		"scope":       row.Scope,
	})
	RespondJSON(w, http.StatusCreated, widgetToResponse(row))
}

// AdminUpdate handles PUT /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	var req WidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := h.validateWidgetRequest(r.Context(), req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	scopeIDs := req.ScopeIDs
	if scopeIDs == nil {
		scopeIDs = []uuid.UUID{}
	}
	row, err := h.queries.UpdateDashboardWidget(r.Context(), sqlc.UpdateDashboardWidgetParams{
		ID:             id,
		Name:           req.Name,
		Description:    req.Description,
		WidgetType:     req.WidgetType,
		Spec:           defaultJSONObject(req.Spec),
		Scope:          req.Scope,
		ScopeIds:       scopeIDs,
		GridX:          req.Grid.X,
		GridY:          req.Grid.Y,
		GridW:          defaultInt32(req.Grid.W, 4),
		GridH:          defaultInt32(req.Grid.H, 2),
		RefreshSeconds: defaultInt32(req.RefreshSeconds, 60),
		Enabled:        enabled,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.dashboard_widget.updated", "dashboard_widget", row.ID.String(), row.Name, map[string]any{
		"widget_type": row.WidgetType,
		"scope":       row.Scope,
	})
	RespondJSON(w, http.StatusOK, widgetToResponse(row))
}

// AdminDelete handles DELETE /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	row, err := h.queries.GetDashboardWidgetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if err := h.queries.DeleteDashboardWidget(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.dashboard_widget.deleted", "dashboard_widget", id.String(), row.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ── Admin: datasource CRUD ────────────────────────────────────────────

// AdminListDatasources handles GET /api/v1/admin/prometheus-datasources/.
func (h *DashboardHandler) AdminListDatasources(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListPrometheusDatasources(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]DatasourceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, datasourceToResponse(row))
	}
	RespondJSON(w, http.StatusOK, out)
}

// AdminCreateDatasource handles POST /api/v1/admin/prometheus-datasources/.
func (h *DashboardHandler) AdminCreateDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req DatasourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := validateDatasourceRequest(req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	encrypted, err := h.sealAuth(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.CreatePrometheusDatasource(r.Context(), sqlc.CreatePrometheusDatasourceParams{
		Name:          req.Name,
		Url:           req.URL,
		AuthEncrypted: encrypted,
		TlsSkipVerify: req.TLSSkipVerify,
		Enabled:       enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.prometheus_datasource.created", "prometheus_datasource", row.ID.String(), row.Name, map[string]any{
		"url": row.Url,
	})
	RespondJSON(w, http.StatusCreated, datasourceToResponse(row))
}

// AdminUpdateDatasource handles PUT /api/v1/admin/prometheus-datasources/{id}/.
func (h *DashboardHandler) AdminUpdateDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	existing, err := h.queries.GetPrometheusDatasourceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	var req DatasourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	// PUT may carry empty auth fields meaning "preserve" — encode that
	// by reusing the existing ciphertext when ALL three fields are empty.
	encrypted := existing.AuthEncrypted
	if req.BasicAuthUser != "" || req.BasicAuthPass != "" || req.BearerToken != "" {
		enc, err := h.sealAuth(req)
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
			return
		}
		encrypted = enc
	}
	if err := validateDatasourceRequest(req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.UpdatePrometheusDatasource(r.Context(), sqlc.UpdatePrometheusDatasourceParams{
		ID:            id,
		Url:           req.URL,
		AuthEncrypted: encrypted,
		TlsSkipVerify: req.TLSSkipVerify,
		Enabled:       enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.prometheus_datasource.updated", "prometheus_datasource", row.ID.String(), row.Name, map[string]any{
		"url": row.Url,
	})
	RespondJSON(w, http.StatusOK, datasourceToResponse(row))
}

// AdminDeleteDatasource handles DELETE /api/v1/admin/prometheus-datasources/{id}/.
func (h *DashboardHandler) AdminDeleteDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	row, err := h.queries.GetPrometheusDatasourceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if err := h.queries.DeletePrometheusDatasource(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	recordAudit(r, h.auditor, "admin.prometheus_datasource.deleted", "prometheus_datasource", id.String(), row.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// AdminTestDatasource handles POST /api/v1/admin/prometheus-datasources/{id}/test/.
// Runs a single instant query against the upstream (`up{}`) — we don't
// care about the result body, just whether the round-trip succeeds and
// returns HTTP 2xx. A failure here typically points at network policy
// or auth misconfiguration.
func (h *DashboardHandler) AdminTestDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	row, err := h.queries.GetPrometheusDatasourceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	ds, err := h.resolveDatasource(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, _, err = dashboards.EvalStat(ctx, h.cache, ds, `up`)
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Datasource reachable"})
}

// ── Public render ─────────────────────────────────────────────────────

// RenderGlobal handles GET /api/v1/dashboards/global/.
func (h *DashboardHandler) RenderGlobal(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Dashboard store not configured")
		return
	}
	rows, err := h.queries.ListWidgetsForScope(r.Context(), sqlc.ListWidgetsForScopeParams{
		Scope:   "global",
		ScopeID: uuid.Nil,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := h.renderRows(r.Context(), rows, map[string]string{})
	RespondJSON(w, http.StatusOK, out)
}

// RenderCluster handles GET /api/v1/dashboards/clusters/{id}/.
func (h *DashboardHandler) RenderCluster(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Dashboard store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	clusterUID, err := h.queries.GetClusterUIDForID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	rows, err := h.queries.ListWidgetsForScope(r.Context(), sqlc.ListWidgetsForScopeParams{
		Scope:   "cluster",
		ScopeID: id,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := h.renderRows(r.Context(), rows, map[string]string{
		"cluster_uid": clusterUID,
		"cluster_id":  id.String(),
	})
	RespondJSON(w, http.StatusOK, out)
}

// RenderProject handles GET /api/v1/dashboards/projects/{id}/.
func (h *DashboardHandler) RenderProject(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Dashboard store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	rows, err := h.queries.ListWidgetsForScope(r.Context(), sqlc.ListWidgetsForScopeParams{
		Scope:   "project",
		ScopeID: id,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := h.renderRows(r.Context(), rows, map[string]string{
		"project_id": id.String(),
	})
	RespondJSON(w, http.StatusOK, out)
}

// renderRows is the shared render path used by all three public
// endpoints. It (a) resolves spec placeholders, (b) fetches data for
// prom_sparkline + prom_stat widgets (in series — fanout would punish
// upstream Prom), and (c) builds the WidgetData payload. Server-side
// errors are surfaced into the per-widget Data.Error string so a
// single broken widget doesn't fail the whole dashboard.
func (h *DashboardHandler) renderRows(ctx context.Context, rows []sqlc.DashboardWidget, vars map[string]string) []RenderedWidget {
	out := make([]RenderedWidget, 0, len(rows))
	// Resolve enabled datasources once per render so multiple
	// prom-typed widgets share the lookup.
	dsByName := map[string]sqlc.PrometheusDatasource{}
	if dsRows, err := h.queries.ListEnabledPrometheusDatasources(ctx); err == nil {
		for _, ds := range dsRows {
			dsByName[ds.Name] = ds
		}
	}
	allowed := h.allowedIframeHosts(ctx)
	for _, row := range rows {
		resolved, err := resolveSpec(row.Spec, vars)
		if err != nil {
			dashRenderCounter(row.WidgetType, "error").Inc()
			out = append(out, RenderedWidget{
				ID: row.ID, Name: row.Name, WidgetType: row.WidgetType,
				SpecResolved: row.Spec, Grid: widgetGrid(row), RefreshSeconds: row.RefreshSeconds,
				Data: &WidgetData{Error: "spec_resolve: " + err.Error()},
			})
			continue
		}
		rendered := RenderedWidget{
			ID:             row.ID,
			Name:           row.Name,
			WidgetType:     row.WidgetType,
			SpecResolved:   resolved,
			Grid:           widgetGrid(row),
			RefreshSeconds: row.RefreshSeconds,
		}
		switch row.WidgetType {
		case "grafana_panel":
			// Server doesn't fetch — but we enforce the iframe allow-list
			// so a tightened setting drops mis-configured widgets.
			host, _ := iframeHost(resolved, "base_url")
			if !hostInAllowList(host, allowed) {
				rendered.Data = &WidgetData{Error: "iframe_host_not_allowed"}
				dashRenderCounter(row.WidgetType, "error").Inc()
				break
			}
			dashRenderCounter(row.WidgetType, "ok").Inc()
		case "url_iframe":
			host, _ := iframeHost(resolved, "url")
			if !hostInAllowList(host, allowed) {
				rendered.Data = &WidgetData{Error: "iframe_host_not_allowed"}
				dashRenderCounter(row.WidgetType, "error").Inc()
				break
			}
			dashRenderCounter(row.WidgetType, "ok").Inc()
		case "prom_sparkline":
			rendered.Data = h.renderSparkline(ctx, resolved, dsByName)
		case "prom_stat":
			rendered.Data = h.renderStat(ctx, resolved, dsByName)
		default:
			dashRenderCounter(row.WidgetType, "error").Inc()
			rendered.Data = &WidgetData{Error: "unknown_widget_type"}
		}
		// Quietly drop prom_* widgets that point at a datasource which
		// doesn't exist. The seeded demo widgets reference "default" and
		// would otherwise paint a "datasource_not_found: default" tile
		// on every fresh install. Operators see widgets they actually
		// configured; never the broken seeds.
		if rendered.Data != nil && strings.HasPrefix(rendered.Data.Error, "datasource_not_found:") {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// renderSparkline fetches the matrix + rasterises the SVG. On any
// failure the function returns a non-nil Data with Error populated;
// the client renders "no data" rather than dropping the widget.
func (h *DashboardHandler) renderSparkline(ctx context.Context, spec json.RawMessage, dsByName map[string]sqlc.PrometheusDatasource) *WidgetData {
	var s struct {
		Datasource string `json:"datasource"`
		Query      string `json:"query"`
		Duration   string `json:"duration"`
		Step       string `json:"step"`
	}
	if err := json.Unmarshal(spec, &s); err != nil {
		dashRenderCounter("prom_sparkline", "error").Inc()
		return &WidgetData{Error: "invalid_spec: " + err.Error()}
	}
	dsRow, ok := dsByName[s.Datasource]
	if !ok {
		dashRenderCounter("prom_sparkline", "no_data").Inc()
		return &WidgetData{Error: "datasource_not_found: " + s.Datasource}
	}
	ds, err := h.resolveDatasource(dsRow)
	if err != nil {
		dashRenderCounter("prom_sparkline", "error").Inc()
		return &WidgetData{Error: err.Error()}
	}
	start := time.Now()
	matrix, err := dashboards.QueryRange(ctx, h.cache, ds, s.Query, cmp.Or(s.Duration, "1h"), cmp.Or(s.Step, "60s"), time.Now())
	dashPromDuration(dsRow.Name).Observe(time.Since(start).Seconds())
	if err != nil {
		dashRenderCounter("prom_sparkline", "error").Inc()
		return &WidgetData{Error: err.Error()}
	}
	dashRenderCounter("prom_sparkline", "ok").Inc()
	return &WidgetData{SparklineSVG: string(dashboards.RenderSparkline(matrix))}
}

// renderStat is the symmetric companion to renderSparkline.
func (h *DashboardHandler) renderStat(ctx context.Context, spec json.RawMessage, dsByName map[string]sqlc.PrometheusDatasource) *WidgetData {
	var s struct {
		Datasource string `json:"datasource"`
		Query      string `json:"query"`
		Unit       string `json:"unit"`
		Format     string `json:"format"`
	}
	if err := json.Unmarshal(spec, &s); err != nil {
		dashRenderCounter("prom_stat", "error").Inc()
		return &WidgetData{Error: "invalid_spec: " + err.Error()}
	}
	dsRow, ok := dsByName[s.Datasource]
	if !ok {
		dashRenderCounter("prom_stat", "no_data").Inc()
		return &WidgetData{Error: "datasource_not_found: " + s.Datasource, StatUnit: s.Unit, StatFormat: s.Format}
	}
	ds, err := h.resolveDatasource(dsRow)
	if err != nil {
		dashRenderCounter("prom_stat", "error").Inc()
		return &WidgetData{Error: err.Error()}
	}
	start := time.Now()
	v, ok2, err := dashboards.EvalStat(ctx, h.cache, ds, s.Query)
	dashPromDuration(dsRow.Name).Observe(time.Since(start).Seconds())
	if err != nil {
		dashRenderCounter("prom_stat", "error").Inc()
		return &WidgetData{Error: err.Error(), StatUnit: s.Unit, StatFormat: s.Format}
	}
	if !ok2 {
		dashRenderCounter("prom_stat", "no_data").Inc()
		return &WidgetData{StatOK: false, StatUnit: s.Unit, StatFormat: s.Format}
	}
	dashRenderCounter("prom_stat", "ok").Inc()
	return &WidgetData{StatValue: v, StatOK: true, StatUnit: s.Unit, StatFormat: s.Format}
}

// resolveDatasource turns a stored row into the dashboards.Datasource
// the renderer wants. Decrypts the auth blob when present; returns a
// 503-shaped error when auth is set but no encryptor is wired.
func (h *DashboardHandler) resolveDatasource(row sqlc.PrometheusDatasource) (dashboards.Datasource, error) {
	out := dashboards.Datasource{
		ID:            row.ID.String(),
		Name:          row.Name,
		URL:           row.Url,
		TLSSkipVerify: row.TlsSkipVerify,
	}
	if row.AuthEncrypted == "" {
		return out, nil
	}
	if h.encryptor == nil {
		return out, fmt.Errorf("datasource has stored auth but encryptor not configured")
	}
	plaintext, err := h.encryptor.Decrypt(row.AuthEncrypted)
	if err != nil {
		return out, fmt.Errorf("auth decrypt: %w", err)
	}
	var blob authBlob
	if err := json.Unmarshal([]byte(plaintext), &blob); err != nil {
		return out, fmt.Errorf("auth decode: %w", err)
	}
	out.BasicAuthUser = blob.BasicAuthUser
	out.BasicAuthPass = blob.BasicAuthPass
	out.BearerToken = blob.BearerToken
	return out, nil
}

// sealAuth is the inverse of resolveDatasource — JSON-encode the
// three auth fields and Fernet-encrypt the blob. Returns an empty
// string when none of the fields are set (encoded as "no auth").
func (h *DashboardHandler) sealAuth(req DatasourceRequest) (string, error) {
	if req.BasicAuthUser == "" && req.BasicAuthPass == "" && req.BearerToken == "" {
		return "", nil
	}
	if h.encryptor == nil {
		return "", fmt.Errorf("encryptor not configured; cannot store datasource auth")
	}
	blob := authBlob{
		BasicAuthUser: req.BasicAuthUser,
		BasicAuthPass: req.BasicAuthPass,
		BearerToken:   req.BearerToken,
	}
	b, err := json.Marshal(blob)
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(b))
}

// ── Validation ────────────────────────────────────────────────────────

// validateWidgetRequest enforces the structural invariants the handler
// + render path depend on. Spec-shape validation is per-type (so the
// renderer doesn't trip over a missing `query` field at run time).
func (h *DashboardHandler) validateWidgetRequest(ctx context.Context, req WidgetRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(req.Name) > 128 {
		return fmt.Errorf("name must be at most 128 characters")
	}
	switch req.WidgetType {
	case "grafana_panel", "prom_sparkline", "prom_stat", "url_iframe":
	default:
		return fmt.Errorf("invalid widget_type %q", req.WidgetType)
	}
	switch req.Scope {
	case "global", "cluster", "project":
	default:
		return fmt.Errorf("invalid scope %q", req.Scope)
	}
	if req.RefreshSeconds < 0 {
		return fmt.Errorf("refresh_seconds must be >= 0")
	}
	if req.Grid.W < 0 || req.Grid.H < 0 || req.Grid.X < 0 || req.Grid.Y < 0 {
		return fmt.Errorf("grid coordinates must be >= 0")
	}
	spec := defaultJSONObject(req.Spec)
	// Per-type spec validation.
	switch req.WidgetType {
	case "grafana_panel":
		var g struct {
			BaseURL      string `json:"base_url"`
			DashboardUID string `json:"dashboard_uid"`
			PanelID      any    `json:"panel_id"`
		}
		if err := json.Unmarshal(spec, &g); err != nil {
			return fmt.Errorf("invalid grafana_panel spec: %w", err)
		}
		if g.BaseURL == "" || g.DashboardUID == "" {
			return fmt.Errorf("grafana_panel requires base_url + dashboard_uid")
		}
		host, err := hostOf(g.BaseURL)
		if err != nil {
			return fmt.Errorf("invalid grafana_panel base_url: %w", err)
		}
		if !hostInAllowList(host, h.allowedIframeHosts(ctx)) {
			return fmt.Errorf("grafana_panel host %q not in dashboard.allowed_iframe_hosts", host)
		}
	case "url_iframe":
		var u struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(spec, &u); err != nil {
			return fmt.Errorf("invalid url_iframe spec: %w", err)
		}
		if u.URL == "" {
			return fmt.Errorf("url_iframe requires url")
		}
		host, err := hostOf(u.URL)
		if err != nil {
			return fmt.Errorf("invalid url_iframe url: %w", err)
		}
		if !hostInAllowList(host, h.allowedIframeHosts(ctx)) {
			return fmt.Errorf("url_iframe host %q not in dashboard.allowed_iframe_hosts", host)
		}
	case "prom_sparkline":
		var s struct {
			Datasource string `json:"datasource"`
			Query      string `json:"query"`
		}
		if err := json.Unmarshal(spec, &s); err != nil {
			return fmt.Errorf("invalid prom_sparkline spec: %w", err)
		}
		if s.Datasource == "" || s.Query == "" {
			return fmt.Errorf("prom_sparkline requires datasource + query")
		}
	case "prom_stat":
		var s struct {
			Datasource string `json:"datasource"`
			Query      string `json:"query"`
		}
		if err := json.Unmarshal(spec, &s); err != nil {
			return fmt.Errorf("invalid prom_stat spec: %w", err)
		}
		if s.Datasource == "" || s.Query == "" {
			return fmt.Errorf("prom_stat requires datasource + query")
		}
	}
	return nil
}

func validateDatasourceRequest(req DatasourceRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(req.Name) > 64 {
		return fmt.Errorf("name must be at most 64 characters")
	}
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must be http or https")
	}
	return nil
}

// allowedIframeHosts returns the comma-separated list of hosts the
// dashboard.allowed_iframe_hosts setting permits. Empty cache = empty
// allow-list (operator must opt-in). The setting is registered into
// platform_settings; the cache resolves it via StringValue.
func (h *DashboardHandler) allowedIframeHosts(ctx context.Context) []string {
	if h.settingsCache == nil {
		return nil
	}
	v := h.settingsCache.StringValue(ctx, "dashboard.allowed_iframe_hosts", "")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// hostInAllowList does a case-insensitive exact-host comparison. Empty
// list = block-all (the operator opt-in default).
func hostInAllowList(host string, allowed []string) bool {
	if host == "" {
		return false
	}
	for _, h := range allowed {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// hostOf extracts the bare host from a URL or template. Template
// placeholders inside the host part ({{cluster_uid}} etc.) are
// preserved literally; the comparison happens after templating in the
// render path, but pre-templating at create-time so the operator
// can't sneak past the allow-list by hiding the host in {{ }}.
func hostOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	h := u.Host
	if h == "" {
		return "", fmt.Errorf("url has no host")
	}
	return strings.ToLower(h), nil
}

// iframeHost extracts the host from a spec field at the given key.
// Returns ("", false) when the field is missing or unparseable —
// hostInAllowList then rejects the widget.
func iframeHost(spec json.RawMessage, key string) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(spec, &m); err != nil {
		return "", false
	}
	v, ok := m[key].(string)
	if !ok || v == "" {
		return "", false
	}
	h, err := hostOf(v)
	if err != nil {
		return "", false
	}
	return h, true
}

// ── Spec resolver ─────────────────────────────────────────────────────

// resolveSpec walks the JSON tree and replaces every {{var}} occurrence
// inside string values with the matching entry in vars. The function
// preserves the JSON structure exactly — only string leaves are
// rewritten. Unknown placeholders are left literal (the client may
// surface them or, more usually, the operator's template error is
// visible at first render).
func resolveSpec(spec json.RawMessage, vars map[string]string) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(spec, &v); err != nil {
		return spec, err
	}
	resolved := walkResolve(v, vars)
	out, err := json.Marshal(resolved)
	if err != nil {
		return spec, err
	}
	return out, nil
}

func walkResolve(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		return substitute(x, vars)
	case []any:
		for i, e := range x {
			x[i] = walkResolve(e, vars)
		}
		return x
	case map[string]any:
		for k, e := range x {
			x[k] = walkResolve(e, vars)
		}
		return x
	}
	return v
}

// substitute rewrites every `{{key}}` (single curly token, no
// whitespace) with vars[key]. Recognises `$cluster_uid` shorthand for
// Grafana-style variables too, so an operator who copy-pastes a
// Grafana panel URL doesn't have to rewrite the `$cluster_uid` into
// `{{cluster_uid}}` first.
func substitute(s string, vars map[string]string) string {
	out := s
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "$"+k, v)
	}
	return out
}

// ── Helpers ───────────────────────────────────────────────────────────

func widgetToResponse(row sqlc.DashboardWidget) WidgetResponse {
	return WidgetResponse{
		ID:             row.ID,
		Name:           row.Name,
		Description:    row.Description,
		WidgetType:     row.WidgetType,
		Spec:           row.Spec,
		Scope:          row.Scope,
		ScopeIDs:       row.ScopeIds,
		Grid:           widgetGrid(row),
		RefreshSeconds: row.RefreshSeconds,
		Enabled:        row.Enabled,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func widgetGrid(row sqlc.DashboardWidget) WidgetGrid {
	return WidgetGrid{X: row.GridX, Y: row.GridY, W: row.GridW, H: row.GridH}
}

func datasourceToResponse(row sqlc.PrometheusDatasource) DatasourceResponse {
	return DatasourceResponse{
		ID:            row.ID,
		Name:          row.Name,
		URL:           row.Url,
		HasAuth:       row.AuthEncrypted != "",
		TLSSkipVerify: row.TlsSkipVerify,
		Enabled:       row.Enabled,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// defaultInt32 lives in monitoring.go — reuse it.

func defaultJSONObject(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}

// Compile-time guard: a *sqlc.Queries satisfies DashboardQuerier.
var _ DashboardQuerier = (*sqlc.Queries)(nil)
