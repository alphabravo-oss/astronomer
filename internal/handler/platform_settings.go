// Package handler — Rancher-style global settings hub.
//
// Migration 046 introduced the `platform_settings` key/value table; this
// file is the API surface in front of it. The motivation is the same as
// Rancher's /v3/settings: operators need a single place to tune branding,
// banners (compliance MOTD), feature flags, token TTL, and the
// telemetry-opt-in — all without redeploying the chart.
//
// Endpoints (all under /api/v1):
//
//   GET    /admin/settings/             — full list, merged with defaults
//   GET    /admin/settings/{key}/       — one setting
//   PUT    /admin/settings/{key}/       — update; body { "value": <any> }
//   DELETE /admin/settings/{key}/       — reset to default
//   GET    /settings/branding/          — PUBLIC: the branding.* subset
//   GET    /settings/banner/            — PUBLIC: the banner.* subset
//
// Admin endpoints are superuser-only (gated inside the handler so the
// failure mode is a clean 403 instead of a generic permission middleware
// rejection — same pattern admin_queues.go uses). Branding + banner read
// endpoints are PRE-AUTH because the login page renders them before the
// user has a session.
//
// Registry: the in-handler `settingsRegistry` is the source of truth for
// the set of legal keys + their type spec + default value. Unknown keys
// on PUT are rejected; type mismatches return 400 validation_error.
// Missing rows on GET fall through to the registry default so a fresh DB
// returns sane values without any handler-side write.
//
// Cache: PUT / DELETE invalidate the in-memory FeatureGate cache (see
// settings_cache.go). The cache TTL is 30s — settings change rarely so
// we don't want every request to hit Postgres for the catalog tab gate.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// PlatformSettingsQuerier is the narrow DB surface this handler needs.
// Production wires *sqlc.Queries; the test fakes implement just these
// four methods (+ optionally CreateAuditLogV1 to cover the audit path).
type PlatformSettingsQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	ListPlatformSettings(ctx context.Context) ([]sqlc.PlatformSetting, error)
	ListPlatformSettingsByPrefix(ctx context.Context, prefix string) ([]sqlc.PlatformSetting, error)
	UpsertPlatformSetting(ctx context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error)
	DeletePlatformSetting(ctx context.Context, key string) error
}

// settingType enumerates the JSON shapes the handler accepts. Anything
// else PUT-ed returns 400 validation_error.
type settingType string

const (
	typeString settingType = "string"
	typeBool   settingType = "bool"
	typeInt    settingType = "int"
	typeEnum   settingType = "enum"
)

// settingSpec describes one canonical setting. Defaults live here (not
// in the DB) so the handler can answer a GET even on a fresh database
// with zero rows. When a row IS present, its value overrides the
// default.
type settingSpec struct {
	Type        settingType
	Default     any
	Description string
	// For typeInt: min/max bounds (inclusive). Zero means unset.
	MinInt int
	MaxInt int
	// For typeEnum: allowed string values.
	Enum []string
}

// Namespaces — used by the pre-auth subset endpoints and the
// FeatureGate middleware. Keep these as the source of truth; the
// registry below uses dotted-prefix keys so "what's a branding key?"
// is answered by `strings.HasPrefix(k, NamespaceBranding+".")` and
// nothing else.
const (
	NamespaceBranding  = "branding"
	NamespaceBanner    = "banner"
	NamespaceFeature   = "feature"
	NamespaceToken     = "token"
	NamespaceTelemetry = "telemetry"
)

// settingsRegistry enumerates every legal key. Adding a new key to
// platform_settings means adding it here too — otherwise PUT will
// reject it as `unknown_key` and reads will skip it.
var settingsRegistry = map[string]settingSpec{
	"branding.product_name":  {Type: typeString, Default: "Astronomer", Description: "Product display name shown in the header and tab title"},
	"branding.logo_url":      {Type: typeString, Default: "", Description: "URL of the logo PNG/SVG; empty string falls back to the built-in mark"},
	"branding.primary_color": {Type: typeString, Default: "#0066CC", Description: "Primary brand color (hex); applied as a CSS variable across the SPA"},
	"branding.support_url":   {Type: typeString, Default: "", Description: "Link rendered in the in-app help menu; empty = hide the menu entry"},
	"branding.copyright":     {Type: typeString, Default: "", Description: "Footer copyright text; empty = hide the footer line"},
	"banner.login_text":      {Type: typeString, Default: "", Description: "Pre-login banner text; markdown supported. Empty = no banner"},
	"banner.global_text":     {Type: typeString, Default: "", Description: "Persistent in-app banner text; markdown supported. Empty = no banner"},
	"banner.global_color":    {Type: typeEnum, Default: "info", Description: "Banner severity: info | warning | critical", Enum: []string{"info", "warning", "critical"}},
	"feature.catalog":        {Type: typeBool, Default: true, Description: "Helm chart catalog tab"},
	"feature.projects":       {Type: typeBool, Default: true, Description: "Projects (multi-tenancy) tab"},
	"feature.monitoring":     {Type: typeBool, Default: true, Description: "Cluster monitoring tab"},
	"feature.argocd":         {Type: typeBool, Default: true, Description: "ArgoCD GitOps integration tab"},
	"feature.security":       {Type: typeBool, Default: true, Description: "Security / CIS scans tab"},
	"feature.backups":        {Type: typeBool, Default: true, Description: "Backup and restore tab"},
	"token.default_ttl_min":  {Type: typeInt, Default: 60, Description: "API token default expiry in minutes; 0 = no expiry", MinInt: 0, MaxInt: 525600 * 10},
	"token.max_ttl_min":      {Type: typeInt, Default: 525600, Description: "Maximum allowed API token expiry in minutes (1 year default)", MinInt: 1, MaxInt: 525600 * 10},
	"telemetry.enabled":      {Type: typeBool, Default: false, Description: "Opt-in: send anonymized aggregate telemetry nightly"},
	"telemetry.endpoint":     {Type: typeString, Default: "https://telemetry.alphabravo.io/astronomer", Description: "HTTPS endpoint that anonymized telemetry POSTs land at"},
	// Migration 058 — dashboard widget iframe allow-list. Comma-
	// separated list of hosts grafana_panel + url_iframe widget specs
	// may point at. Empty (the default) blocks every iframe widget;
	// the operator opts-in by populating the list.
	"dashboard.allowed_iframe_hosts": {Type: typeString, Default: "", Description: "Comma-separated allow-list of hosts dashboard widgets may iframe (e.g. grafana.example.com,billing.example.com)"},
}

// preAuthAllowedNamespaces is the explicit allowlist for the public
// /settings/branding/ + /settings/banner/ endpoints. We do NOT use
// the registry's full namespace set here — telemetry.* and feature.*
// must NEVER leak pre-auth (telemetry.endpoint is operator config,
// feature.* tells an attacker which surfaces to probe).
var preAuthAllowedNamespaces = map[string]string{
	"branding": NamespaceBranding,
	"banner":   NamespaceBanner,
}

// PlatformSettingsHandler owns /api/v1/admin/settings/* + the two
// pre-auth /api/v1/settings/branding/, /banner/ endpoints.
type PlatformSettingsHandler struct {
	queries PlatformSettingsQuerier
	// cache is the FeatureGate middleware's cache, shared so that PUT /
	// DELETE invalidate it. Optional — the handler works without one.
	cache *SettingsCache
}

// NewPlatformSettingsHandler wires the handler. queries may be nil for
// degenerate test installs; the handler then 503s on every endpoint.
func NewPlatformSettingsHandler(queries PlatformSettingsQuerier) *PlatformSettingsHandler {
	return &PlatformSettingsHandler{queries: queries}
}

// SetCache attaches the shared FeatureGate cache so mutations
// invalidate it. Optional.
func (h *PlatformSettingsHandler) SetCache(c *SettingsCache) { h.cache = c }

// settingResponse is the wire shape for a single setting. UpdatedBy is
// a string pointer because the JSONB column can be NULL (and is, before
// any operator has touched the row).
type settingResponse struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Default     any             `json:"default"`
	UpdatedBy   *string         `json:"updated_by,omitempty"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
	IsDefault   bool            `json:"is_default"`
}

// List handles GET /api/v1/admin/settings/.
//
// Returns the full merged view: every registry key, with its current
// value (from DB if present, else default). Superuser-gated.
func (h *PlatformSettingsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Settings store not configured")
		return
	}
	rows, err := h.queries.ListPlatformSettings(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	rowByKey := make(map[string]sqlc.PlatformSetting, len(rows))
	for _, r := range rows {
		rowByKey[r.Key] = r
	}
	out := make([]settingResponse, 0, len(settingsRegistry))
	for key, spec := range settingsRegistry {
		out = append(out, buildResponse(key, spec, rowByKey[key]))
	}
	// Stable order — registry map iteration is random, but the wire
	// contract should be predictable for the operator UI.
	sortSettings(out)
	RespondJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/admin/settings/{key}/.
func (h *PlatformSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	spec, known := settingsRegistry[key]
	if !known {
		RespondError(w, http.StatusNotFound, "unknown_key", "Unknown setting key")
		return
	}
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Settings store not configured")
		return
	}
	row, err := h.queries.GetPlatformSetting(r.Context(), key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, buildResponse(key, spec, row))
}

// updateRequest is the body shape for PUT.
type updateRequest struct {
	Value json.RawMessage `json:"value"`
}

// Update handles PUT /api/v1/admin/settings/{key}/.
//
// Body: { "value": <JSON> }. The value is validated against the
// registry spec's type before persisting. On success, the in-memory
// FeatureGate cache is invalidated so the next request sees the new
// value.
func (h *PlatformSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	spec, known := settingsRegistry[key]
	if !known {
		RespondError(w, http.StatusNotFound, "unknown_key", "Unknown setting key")
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if len(req.Value) == 0 {
		RespondError(w, http.StatusBadRequest, "validation_error", "value is required")
		return
	}
	if err := validateValue(spec, req.Value); err != nil {
		RespondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Settings store not configured")
		return
	}

	// Capture the previous value for the audit trail. ErrNoRows is the
	// normal "first write" case — record an empty old_value.
	var oldValueJSON json.RawMessage
	if prev, err := h.queries.GetPlatformSetting(r.Context(), key); err == nil {
		oldValueJSON = prev.Value
	} else if !errors.Is(err, pgx.ErrNoRows) {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	row, err := h.queries.UpsertPlatformSetting(r.Context(), sqlc.UpsertPlatformSettingParams{
		Key:         key,
		Value:       req.Value,
		Description: spec.Description,
		UpdatedBy:   currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	if h.cache != nil {
		h.cache.Invalidate(key)
	}

	recordAudit(r, h.queries, "admin.platform_settings.updated", "platform_setting", key, key, map[string]any{
		"key":       key,
		"old_value": rawOrNull(oldValueJSON),
		"new_value": rawOrNull(req.Value),
	})

	RespondJSON(w, http.StatusOK, buildResponse(key, spec, row))
}

// Delete handles DELETE /api/v1/admin/settings/{key}/.
//
// Resets the key to the registry default by removing the row. The next
// GET returns the registry default.
func (h *PlatformSettingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	spec, known := settingsRegistry[key]
	if !known {
		RespondError(w, http.StatusNotFound, "unknown_key", "Unknown setting key")
		return
	}
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Settings store not configured")
		return
	}
	var oldValueJSON json.RawMessage
	if prev, err := h.queries.GetPlatformSetting(r.Context(), key); err == nil {
		oldValueJSON = prev.Value
	}
	if err := h.queries.DeletePlatformSetting(r.Context(), key); err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if h.cache != nil {
		h.cache.Invalidate(key)
	}
	recordAudit(r, h.queries, "admin.platform_settings.reset", "platform_setting", key, key, map[string]any{
		"key":       key,
		"old_value": rawOrNull(oldValueJSON),
	})
	// Echo back the default so the SPA doesn't have to refetch.
	RespondJSON(w, http.StatusOK, buildResponse(key, spec, sqlc.PlatformSetting{}))
}

// PublicBranding handles GET /api/v1/settings/branding/. PRE-AUTH —
// the login page renders product name + logo + primary color BEFORE
// the user has a session.
func (h *PlatformSettingsHandler) PublicBranding(w http.ResponseWriter, r *http.Request) {
	h.servePublicNamespace(w, r, NamespaceBranding)
}

// PublicBanner handles GET /api/v1/settings/banner/. PRE-AUTH — the
// login banner is part of the same first-paint render as branding.
func (h *PlatformSettingsHandler) PublicBanner(w http.ResponseWriter, r *http.Request) {
	h.servePublicNamespace(w, r, NamespaceBanner)
}

// servePublicNamespace is the shared implementation for the pre-auth
// readers. The `canonical` argument is hardcoded by the caller (not
// derived from a URL param) so an attacker can never pivot a public
// route to dump telemetry.endpoint or the feature flag map.
func (h *PlatformSettingsHandler) servePublicNamespace(w http.ResponseWriter, r *http.Request, canonical string) {
	// Belt-and-suspenders: even though the caller is hardcoded, double
	// check against the explicit allowlist so accidental additions to
	// servePublicNamespace's callsites can't widen the exposure.
	if _, ok := preAuthAllowedNamespaces[canonical]; !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown public settings namespace")
		return
	}
	if h.queries == nil {
		// Degraded mode: still return the registry defaults so the
		// login page renders with the built-in branding.
		RespondJSON(w, http.StatusOK, publicSubsetResponse(canonical, nil))
		return
	}
	rows, err := h.queries.ListPlatformSettingsByPrefix(r.Context(), canonical+".")
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, publicSubsetResponse(canonical, rows))
}

// publicSubsetResponse projects DB rows + registry defaults onto a
// flat map { key: value } for the public branding/banner readers. We
// strip metadata (description, updated_by) because pre-auth callers
// don't need it and we shouldn't volunteer who-edited-what before
// authentication.
func publicSubsetResponse(canonical string, rows []sqlc.PlatformSetting) map[string]any {
	rowByKey := make(map[string]sqlc.PlatformSetting, len(rows))
	for _, r := range rows {
		rowByKey[r.Key] = r
	}
	out := make(map[string]any)
	prefix := canonical + "."
	for key, spec := range settingsRegistry {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		row, present := rowByKey[key]
		if present && len(row.Value) > 0 && string(row.Value) != "null" {
			var v any
			if err := json.Unmarshal(row.Value, &v); err == nil {
				out[key] = v
				continue
			}
		}
		out[key] = spec.Default
	}
	return out
}

// gate enforces superuser-only access. Matches the admin_queues.go
// pattern. Audits on success so a read attempt is logged.
func (h *PlatformSettingsHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return false
	}
	callerID, err := uuid.Parse(caller.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return false
	}
	if h.queries == nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "User store not configured")
		return false
	}
	user, err := h.queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
		return false
	}
	if !user.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden", "Settings administration requires superuser privileges")
		return false
	}
	return true
}

// --- helpers ---

// buildResponse merges a registry spec with an optional DB row into the
// wire shape. row.Key == "" signals "no DB row" (the default zero value).
func buildResponse(key string, spec settingSpec, row sqlc.PlatformSetting) settingResponse {
	out := settingResponse{
		Key:         key,
		Description: spec.Description,
		Type:        string(spec.Type),
		Default:     spec.Default,
	}
	if row.Key != "" && len(row.Value) > 0 {
		out.Value = row.Value
		// Description override from DB takes priority — operators may
		// re-document a setting for their internal users.
		if row.Description != "" {
			out.Description = row.Description
		}
		updatedAt := row.UpdatedAt
		out.UpdatedAt = &updatedAt
		if row.UpdatedBy.Valid {
			id := uuid.UUID(row.UpdatedBy.Bytes).String()
			out.UpdatedBy = &id
		}
		out.IsDefault = false
	} else {
		// Fall through to default. Encode the default to JSON so the
		// `value` field is always RawMessage-shaped.
		defaultJSON, _ := json.Marshal(spec.Default)
		out.Value = defaultJSON
		out.IsDefault = true
	}
	return out
}

// sortSettings sorts by key alphabetically — namespaces cluster
// (branding.*, banner.*, ...) which matches how the UI groups them.
func sortSettings(out []settingResponse) {
	// Small N (~18); a single-pass insertion is fine and avoids the
	// sort package dependency.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].Key > out[j].Key {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
}

// validateValue checks that the raw JSON matches the registry spec.
// Returns nil on success; a user-facing error message on failure.
func validateValue(spec settingSpec, raw json.RawMessage) error {
	switch spec.Type {
	case typeString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("value must be a string")
		}
	case typeBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("value must be a boolean (true/false)")
		}
	case typeInt:
		var n json.Number
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return fmt.Errorf("value must be an integer")
		}
		i, err := n.Int64()
		if err != nil {
			return fmt.Errorf("value must be an integer")
		}
		if spec.MinInt != 0 || spec.MaxInt != 0 {
			if i < int64(spec.MinInt) || i > int64(spec.MaxInt) {
				return fmt.Errorf("value must be between %d and %d", spec.MinInt, spec.MaxInt)
			}
		}
	case typeEnum:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("value must be a string")
		}
		ok := false
		for _, e := range spec.Enum {
			if e == s {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("value must be one of: %s", strings.Join(spec.Enum, ", "))
		}
	default:
		return fmt.Errorf("internal: unknown setting type %q", spec.Type)
	}
	return nil
}

// rawOrNull turns a JSONB RawMessage into something json.Marshal can
// embed in the audit detail map. Empty input → nil so the audit row
// shows `null` rather than `""`.
func rawOrNull(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	return v
}

// updatedByPGType is unused here directly but kept for symmetry with
// the rest of the package — see auth_context.currentUserUUID. The line
// below silences "imported and not used" if all callers vanish.
var _ = pgtype.UUID{}
