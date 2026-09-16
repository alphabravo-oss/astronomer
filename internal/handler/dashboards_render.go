package handler

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
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
