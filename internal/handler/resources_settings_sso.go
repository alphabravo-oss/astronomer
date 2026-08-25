package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

var ssoProviderSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func (h *ResourceHandler) GetGeneralSettings(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"platformName":           "Astronomer",
			"agentHeartbeatInterval": 30,
			"defaultSessionTimeout":  60,
			"enableAuditLogging":     true,
			"metricsCollection":      true,
		})
		return
	}
	cfg, err := h.queries.GetPlatformConfig(r.Context())
	if err != nil && err != pgx.ErrNoRows {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Failed to load platform settings")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"platformName":           defaultString(cfg.PlatformName, "Astronomer"),
		"agentHeartbeatInterval": 30,
		"defaultSessionTimeout":  60,
		"enableAuditLogging":     true,
		"metricsCollection":      true,
	})
}

// UpdateGeneralSettings handles PUT /api/v1/settings/general/.
//
// Persists the editable platform-level settings to the singleton
// platform_configuration row. Fields not present in the body keep their
// previous value (read from GetPlatformConfig). The frontend currently
// edits platformName + telemetry/heartbeat-style toggles; only platformName
// has a backing column today, the others are echoed back unchanged.
// openapi:request-operation putSettingsGeneral
type UpdateGeneralSettingsRequest struct {
	PlatformName           *string `json:"platformName"`
	PlatformNameSnake      *string `json:"platform_name"`
	AgentHeartbeatInterval *int    `json:"agentHeartbeatInterval"`
	DefaultSessionTimeout  *int    `json:"defaultSessionTimeout"`
	EnableAuditLogging     *bool   `json:"enableAuditLogging"`
	MetricsCollection      *bool   `json:"metricsCollection"`
}

func (h *ResourceHandler) UpdateGeneralSettings(w http.ResponseWriter, r *http.Request) {
	var req UpdateGeneralSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if h.runTx != nil {
		var cfg sqlc.PlatformConfiguration
		err := h.runTx(r.Context(), func(q ResourceSettingsMutationTx) error {
			current, err := q.GetPlatformConfigForUpdate(r.Context())
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			cfg, err = q.UpsertPlatformConfig(r.Context(), generalSettingsUpdateParams(current, req))
			if err != nil {
				return err
			}
			return recordAuditOutbox(r, q, "settings.general.update", "platform_settings", fmt.Sprintf("%d", cfg.ID), cfg.PlatformName, http.StatusOK, map[string]any{
				"platform_name":     cfg.PlatformName,
				"telemetry_enabled": cfg.TelemetryEnabled,
			})
		})
		if err != nil {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.SettingsError, "Failed to update platform settings")
			return
		}
		// Keep every process-local settings read behind the commit boundary.
		// General settings currently use platform_configuration, but flushing
		// here prevents a future mirrored setting from observing stale state.
		if h.settingsCache != nil {
			h.settingsCache.Flush()
		}
		respondGeneralSettingsUpdate(w, req, cfg)
		return
	}

	var current sqlc.PlatformConfiguration
	if h.queries != nil {
		if cfg, err := h.queries.GetPlatformConfig(r.Context()); err == nil {
			current = cfg
		}
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "settings store not configured")
		return
	}
	cfg, err := h.queries.UpsertPlatformConfig(r.Context(), generalSettingsUpdateParams(current, req))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Failed to update platform settings")
		return
	}
	recordAudit(r, h.queries, "settings.general.update", "platform_settings", fmt.Sprintf("%d", cfg.ID), cfg.PlatformName, map[string]any{
		"platform_name":     cfg.PlatformName,
		"telemetry_enabled": cfg.TelemetryEnabled,
	})
	respondGeneralSettingsUpdate(w, req, cfg)
}

func generalSettingsUpdateParams(current sqlc.PlatformConfiguration, req UpdateGeneralSettingsRequest) sqlc.UpsertPlatformConfigParams {
	platformName := defaultString(current.PlatformName, "Astronomer")
	telemetry := current.TelemetryEnabled
	if current.ID == 0 {
		telemetry = true
	}
	if req.PlatformName != nil && *req.PlatformName != "" {
		platformName = *req.PlatformName
	} else if req.PlatformNameSnake != nil && *req.PlatformNameSnake != "" {
		platformName = *req.PlatformNameSnake
	}
	if req.MetricsCollection != nil {
		telemetry = *req.MetricsCollection
	}
	return sqlc.UpsertPlatformConfigParams{
		ServerUrl:        current.ServerUrl,
		PlatformName:     platformName,
		TelemetryEnabled: telemetry,
		BootstrappedAt:   current.BootstrappedAt,
		InstanceID:       current.InstanceID,
	}
}

func respondGeneralSettingsUpdate(w http.ResponseWriter, req UpdateGeneralSettingsRequest, cfg sqlc.PlatformConfiguration) {
	RespondJSON(w, http.StatusOK, map[string]any{
		"platformName":           defaultString(cfg.PlatformName, "Astronomer"),
		"agentHeartbeatInterval": intOrDefault(req.AgentHeartbeatInterval, 30),
		"defaultSessionTimeout":  intOrDefault(req.DefaultSessionTimeout, 60),
		"enableAuditLogging":     boolOrDefault(req.EnableAuditLogging, true),
		"metricsCollection":      cfg.TelemetryEnabled,
	})
}

func intOrDefault(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func boolOrDefault(p *bool, d bool) bool {
	if p == nil {
		return d
	}
	return *p
}

func ssoConfigurationToResponse(row sqlc.SsoConfiguration) SSOProviderResponse {
	cfg := map[string]string{}
	var parsed auth.SSOProviderConfig
	if len(row.Config) > 0 && json.Unmarshal(row.Config, &parsed) == nil {
		if parsed.IssuerURL != "" {
			cfg["metadataUrl"] = parsed.IssuerURL + "/.well-known/openid-configuration"
		}
		if parsed.RedirectURL != "" {
			cfg["redirectUrl"] = parsed.RedirectURL
		}
		if len(parsed.Scopes) > 0 {
			cfg["scopes"] = strings.Join(parsed.Scopes, ",")
		}
	}
	if len(row.AllowedOrganizations) > 0 {
		var orgs []string
		if json.Unmarshal(row.AllowedOrganizations, &orgs) == nil && len(orgs) > 0 {
			cfg["allowedOrganizations"] = strings.Join(orgs, ",")
		}
	}
	return SSOProviderResponse{
		ID:        row.ID.String(),
		Provider:  row.Provider,
		Type:      ssoProviderType(row),
		Name:      defaultString(row.DisplayName, row.Provider),
		Enabled:   row.IsEnabled,
		Config:    cfg,
		CreatedAt: row.CreatedAt.UTC().Format(timeLayout),
		UpdatedAt: row.UpdatedAt.UTC().Format(timeLayout),
	}
}

func ssoProviderType(row sqlc.SsoConfiguration) string {
	if row.Provider == "github" || row.Provider == "google" {
		return row.Provider
	}
	return "oidc"
}

func ssoProviderKeyFromName(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = ssoProviderSlugPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	return slug
}

func normalizeOIDCIssuerURL(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimRight(value, "/")
	const suffix = "/.well-known/openid-configuration"
	if strings.HasSuffix(value, suffix) {
		return strings.TrimSuffix(value, suffix)
	}
	return value
}

func csvStringToJSON(raw string) json.RawMessage {
	items := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value != "" {
			items = append(items, value)
		}
	}
	if len(items) == 0 {
		return json.RawMessage(`[]`)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}

func (h *ResourceHandler) registerSSOProvider(ctx context.Context, providerKey, providerType string, cfg auth.SSOProviderConfig, clientID, secretEncrypted string) error {
	if h == nil || h.ssoMgr == nil {
		return fmt.Errorf("sso manager is not configured")
	}
	if providerType == "oidc" {
		return h.ssoMgr.RegisterOIDCProvider(ctx, providerKey, cfg.IssuerURL, clientID, secretEncrypted, cfg.RedirectURL, cfg.Scopes)
	}
	return h.ssoMgr.RegisterProvider(providerType, clientID, secretEncrypted, cfg.RedirectURL, cfg.Scopes)
}

func (h *ResourceHandler) ListSSOProviders(w http.ResponseWriter, r *http.Request) {
	if h.sso == nil {
		RespondList(w, []any{}, NewPagination(0, 0, 0, 0))
		return
	}
	rows, err := h.sso.GetEnabledSSOProviders(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SSOError, "Failed to load SSO providers")
		return
	}
	items := make([]SSOProviderResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, ssoConfigurationToResponse(row))
	}
	// GetEnabledSSOProviders returns every enabled provider unpaginated.
	RespondList(w, items, NewPagination(len(items), len(items), 0, len(items)))
}

// openapi:request-operation postSettingsSso
type SSOProviderRequest struct {
	Type    string               `json:"type"`
	Name    string               `json:"name"`
	Enabled *bool                `json:"enabled"`
	Config  SSOProviderConfigDTO `json:"config"`
}

type SSOProviderConfigDTO struct {
	ClientID             string `json:"client_id"`
	ClientSecret         string `json:"client_secret"`
	MetadataURL          string `json:"metadata_url"`
	AllowedOrganizations string `json:"allowed_organizations"`
	AutoCreateUsers      *bool  `json:"auto_create_users"`
}

type SSOProviderResponse struct {
	ID        string            `json:"id"`
	Provider  string            `json:"provider"`
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Config    map[string]string `json:"config"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// CreateSSOProvider handles POST /api/v1/settings/sso/.
func (h *ResourceHandler) CreateSSOProvider(w http.ResponseWriter, r *http.Request) {
	if h.sso == nil || h.encryptor == nil || h.ssoMgr == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SSOUnavailable, "SSO provider management is not configured")
		return
	}
	var req SSOProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Provider name is required")
		return
	}
	switch req.Type {
	case "github", "google", "oidc":
	default:
		RespondRequestError(w, r, http.StatusBadRequest, apierror.UnsupportedProvider, "Supported provider types are github, google, and oidc")
		return
	}
	if strings.TrimSpace(req.Config.ClientID) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Client ID is required")
		return
	}
	if strings.TrimSpace(req.Config.ClientSecret) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Client secret is required")
		return
	}
	if req.Type == "oidc" && strings.TrimSpace(req.Config.MetadataURL) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "metadata_url is required for OIDC providers")
		return
	}

	providerKey := req.Type
	if req.Type == "oidc" {
		providerKey = ssoProviderKeyFromName(req.Name)
		if providerKey == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Provider name must contain letters or numbers")
			return
		}
	}
	if len(providerKey) > 16 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Provider key must be 16 characters or fewer")
		return
	}
	// Production re-checks under the provider-key transaction lock below.
	// Keep this direct check only for narrow fakes that do not wire runTx.
	if h.runTx == nil {
		if _, err := h.sso.GetSSOConfigurationByProvider(r.Context(), providerKey); err == nil {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "An SSO provider with this key already exists")
			return
		} else if !errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.SSOError, "Failed to check SSO provider")
			return
		}
	}

	issuerURL := normalizeOIDCIssuerURL(req.Config.MetadataURL)
	config := auth.SSOProviderConfig{}
	if req.Type == "oidc" {
		config.IssuerURL = issuerURL
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode SSO config")
		return
	}
	secretEncrypted, err := h.encryptor.Encrypt(req.Config.ClientSecret)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt SSO client secret")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	allowedOrgs := csvStringToJSON(req.Config.AllowedOrganizations)
	autoCreateUsers := true
	if req.Config.AutoCreateUsers != nil {
		autoCreateUsers = *req.Config.AutoCreateUsers
	}

	params := sqlc.CreateSSOConfigurationParams{
		Provider:              providerKey,
		IsEnabled:             enabled,
		DisplayName:           req.Name,
		Config:                configJSON,
		ClientID:              strings.TrimSpace(req.Config.ClientID),
		ClientSecretEncrypted: secretEncrypted,
		AllowedOrganizations:  allowedOrgs,
		AllowedDomains:        json.RawMessage(`[]`),
		AutoCreateUsers:       autoCreateUsers,
		DefaultGlobalRoleID:   pgtype.UUID{},
	}
	var created sqlc.SsoConfiguration
	if h.runTx != nil {
		created, err = h.createSSOProviderTx(r, params)
	} else {
		created, err = h.sso.CreateSSOConfiguration(r.Context(), params)
	}
	if err != nil {
		if errors.Is(err, errSSOProviderConflict) || isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "An SSO provider with this key already exists")
			return
		}
		if h.runTx != nil {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create SSO provider")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create SSO provider")
		return
	}
	if h.runTx == nil {
		recordAudit(r, h.queries, "sso.provider.create", "sso_provider", created.ID.String(), created.DisplayName, map[string]any{
			"provider": providerKey,
			"type":     ssoProviderType(created),
			"enabled":  created.IsEnabled,
		})
	}

	// Registration is an in-memory/network side effect and must not run until
	// the durable state + audit transaction has committed.
	if enabled {
		if registerErr := h.registerSSOProvider(r.Context(), providerKey, req.Type, config, params.ClientID, secretEncrypted); registerErr != nil {
			if h.ssoMgr.HasProvider(providerKey) {
				h.ssoMgr.RemoveProvider(providerKey)
			}
			if h.runTx != nil {
				if compensateErr := h.compensateSSOCreate(r, created); compensateErr != nil {
					RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RegistrationError, "SSO provider was saved but runtime activation failed; operator repair is required")
					return
				}
			} else {
				_ = h.sso.DeleteSSOConfiguration(r.Context(), created.ID)
			}
			RespondRequestError(w, r, http.StatusBadGateway, apierror.RegistrationError, "SSO provider runtime activation failed; the saved configuration was removed")
			return
		}
	}
	w.Header().Set("Location", "/api/v1/settings/sso/"+created.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, ssoConfigurationToResponse(created))
}

// DeleteSSOProvider handles DELETE /api/v1/settings/sso/{id}/.
func (h *ResourceHandler) DeleteSSOProvider(w http.ResponseWriter, r *http.Request) {
	if h.sso == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SSOUnavailable, "SSO provider management is not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid SSO provider ID")
		return
	}
	var existing sqlc.SsoConfiguration
	if h.runTx != nil {
		existing, err = h.deleteSSOProviderTx(r, id)
		if errors.Is(err, errSSOProviderNotFound) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SSO provider not found")
			return
		}
		if err != nil {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete SSO provider")
			return
		}
	} else {
		existing, err = h.sso.GetSSOConfigurationByID(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SSO provider not found")
			return
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.SSOError, "Failed to load SSO provider")
			return
		}
		if err := h.sso.DeleteSSOConfiguration(r.Context(), id); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete SSO provider")
			return
		}
		recordAudit(r, h.queries, "sso.provider.delete", "sso_provider", existing.ID.String(), existing.DisplayName, map[string]any{
			"provider": existing.Provider,
			"type":     ssoProviderType(existing),
		})
	}
	// Runtime removal follows the committed delete. RemoveProvider is
	// intentionally idempotent and cannot fail.
	if h.ssoMgr != nil {
		h.ssoMgr.RemoveProvider(existing.Provider)
	}
	w.WriteHeader(http.StatusNoContent)
}
