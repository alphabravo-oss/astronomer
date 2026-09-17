package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

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
	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	if u.User != nil {
		return fmt.Errorf("url must not include credentials; use the datasource auth fields")
	}
	return nil
}

// datasourceAuditDetail deliberately records only the endpoint origin. A
// Prometheus base URL may contain tenant selectors or tokens in its path/query;
// those values must never be copied into audit sinks.
func datasourceAuditDetail(row sqlc.PrometheusDatasource) map[string]any {
	origin := ""
	if parsed, err := url.Parse(row.Url); err == nil && parsed.Host != "" {
		origin = parsed.Scheme + "://" + parsed.Host
	}
	return map[string]any{
		"endpoint_origin": origin,
		"has_auth":        row.AuthEncrypted != "",
		"tls_skip_verify": row.TlsSkipVerify,
		"enabled":         row.Enabled,
	}
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
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("url must use https")
	}
	if u.User != nil {
		return "", fmt.Errorf("url must not include credentials")
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

func datasourceListToResponse(row sqlc.ListPrometheusDatasourcesPageRow) DatasourceResponse {
	return DatasourceResponse{
		ID:            row.ID,
		Name:          row.Name,
		URL:           row.Url,
		HasAuth:       row.HasAuth,
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
