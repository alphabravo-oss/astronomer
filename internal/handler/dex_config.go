package handler

// Phase B4 — Dex shim for enterprise auth.
//
// Astronomer-go itself only ever speaks generic OIDC (see internal/auth/oauth.go's
// RegisterOIDCProvider path that landed in Phase A1). Dex brokers the messy IdPs
// — Azure AD, LDAP, SAML, Okta, GitLab, etc. — and exposes a single OIDC issuer
// our SSO manager can register against.
//
// This file owns:
//   * CRUD for `dex_connectors` (one row per upstream IdP connector).
//   * Singleton settings for the running Dex deployment (issuer URL, namespace,
//     ConfigMap name, public clients, expiry).
//   * A connector-type registry mapping each connector kind to its required +
//     optional + secret config fields. Validation runs against this registry on
//     every write.
//   * Rendering settings + connectors into a Dex-shaped YAML configmap and
//     PATCH/POST'ing it to the management cluster via K8sRequester so Dex's
//     in-process file watcher hot-reloads.
//   * `register-as-sso` ergonomics: one-click row in `sso_configurations` so the
//     A1 OIDC discovery path can register Dex as a normal OIDC provider.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// dexSettingsSingletonID is the fixed UUID we use for the singleton settings
// row. Callers never have to know this — the handler always reads/writes by
// this id.
var dexSettingsSingletonID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// DexQuerier abstracts the database queries the Dex handler needs.
type DexQuerier interface {
	GetDexConnectorByID(ctx context.Context, id uuid.UUID) (sqlc.DexConnector, error)
	GetDexConnectorByName(ctx context.Context, name string) (sqlc.DexConnector, error)
	ListDexConnectors(ctx context.Context) ([]sqlc.DexConnector, error)
	ListEnabledDexConnectors(ctx context.Context) ([]sqlc.DexConnector, error)
	CreateDexConnector(ctx context.Context, arg sqlc.CreateDexConnectorParams) (sqlc.DexConnector, error)
	UpdateDexConnector(ctx context.Context, arg sqlc.UpdateDexConnectorParams) (sqlc.DexConnector, error)
	DeleteDexConnector(ctx context.Context, id uuid.UUID) error

	GetDexSettings(ctx context.Context, id uuid.UUID) (sqlc.DexSetting, error)
	UpsertDexSettings(ctx context.Context, arg sqlc.UpsertDexSettingsParams) (sqlc.DexSetting, error)

	// SSO bridge — register-as-sso writes here.
	GetSSOConfigurationByProvider(ctx context.Context, provider string) (sqlc.SsoConfiguration, error)
	CreateSSOConfiguration(ctx context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	UpdateSSOConfiguration(ctx context.Context, arg sqlc.UpdateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
}

// DexHandler exposes /api/v1/auth/dex/* endpoints.
type DexHandler struct {
	queries   DexQuerier
	encryptor *auth.Encryptor
	k8s       K8sRequester
	log       *slog.Logger
}

// NewDexHandler constructs a Dex handler. queries is required; encryptor and
// k8s are optional (encryptor: secret round-trip is skipped when nil; k8s: the
// /apply endpoint short-circuits with a 503 when nil so unit tests can run
// without a tunnel).
func NewDexHandler(queries DexQuerier) *DexHandler {
	return &DexHandler{
		queries: queries,
		log:     slog.Default(),
	}
}

// SetEncryptor wires the Fernet encryptor used to encrypt secret connector
// fields (clientSecret, bindPW, ...) before they hit the database.
func (h *DexHandler) SetEncryptor(enc *auth.Encryptor) {
	if h != nil {
		h.encryptor = enc
	}
}

// SetK8sRequester wires the tunnel-backed Kubernetes API client used by /apply.
func (h *DexHandler) SetK8sRequester(req K8sRequester) {
	if h != nil {
		h.k8s = req
	}
}

// SetLogger overrides the default logger.
func (h *DexHandler) SetLogger(log *slog.Logger) {
	if h != nil && log != nil {
		h.log = log
	}
}

// dexConnectorSpec describes a connector type's configuration surface. The
// registry below maps each `type` to one of these. The handler validates
// incoming POST/PATCH requests against the matching spec and returns 400 with
// a list of missing fields.
type dexConnectorSpec struct {
	Type        string
	DisplayHint string
	// Required is the list of top-level config keys that MUST be present and
	// non-empty in the connector's `config` JSON.
	Required []string
	// Optional documents the keys we know about but don't require.
	Optional []string
	// Secret is the list of top-level config keys whose values are sensitive
	// and must be encrypted at rest. The handler round-trips these through the
	// Fernet encryptor on read/write/render.
	Secret []string
	// Nested describes any "must contain field X.Y" relationships. Top-level
	// missing parents are reported by Required; this catches deeper required
	// fields like userSearch.baseDN for ldap.
	Nested []nestedRequirement
}

type nestedRequirement struct {
	Parent string
	Keys   []string
}

// dexConnectorRegistry is the source of truth for which fields each connector
// type expects. Keep this list in sync with the wizard in the frontend.
var dexConnectorRegistry = map[string]dexConnectorSpec{
	"oidc": {
		Type:        "oidc",
		DisplayHint: "Generic OpenID Connect (Keycloak, Authentik, Auth0, ...)",
		Required:    []string{"issuer", "clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "scopes", "userNameKey", "insecureSkipVerify"},
		Secret:      []string{"clientSecret"},
	},
	"okta": {
		Type:        "okta",
		DisplayHint: "Okta (treated as OIDC with Okta defaults)",
		Required:    []string{"issuer", "clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "scopes", "groups"},
		Secret:      []string{"clientSecret"},
	},
	"microsoft": {
		Type:        "microsoft",
		DisplayHint: "Azure AD / Microsoft Entra ID",
		Required:    []string{"tenant", "clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "groups", "onlySecurityGroups", "useGroupsAsWhitelist"},
		Secret:      []string{"clientSecret"},
	},
	"github": {
		Type:        "github",
		DisplayHint: "GitHub OAuth (orgs / teams)",
		Required:    []string{"clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "orgs", "teams", "loadAllGroups"},
		Secret:      []string{"clientSecret"},
	},
	"gitlab": {
		Type:        "gitlab",
		DisplayHint: "GitLab (self-hosted or gitlab.com)",
		Required:    []string{"baseURL", "clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "groups"},
		Secret:      []string{"clientSecret"},
	},
	"bitbucket": {
		Type:        "bitbucket",
		DisplayHint: "Bitbucket Cloud",
		Required:    []string{"clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "teams"},
		Secret:      []string{"clientSecret"},
	},
	"google": {
		Type:        "google",
		DisplayHint: "Google Workspace",
		Required:    []string{"clientID", "clientSecret"},
		Optional:    []string{"redirectURI", "scopes", "hostedDomains"},
		Secret:      []string{"clientSecret"},
	},
	"saml": {
		Type:        "saml",
		DisplayHint: "SAML 2.0 (ADFS, Shibboleth, Okta-SAML, ...)",
		Required:    []string{"ssoURL", "entityIssuer"},
		Optional: []string{
			"ca", "caData", "redirectURI", "usernameAttr", "emailAttr",
			"groupsAttr", "groupsDelim", "filterGroups", "allowedGroups",
			"insecureSkipSignatureValidation", "nameIDPolicyFormat",
		},
	},
	"ldap": {
		Type:        "ldap",
		DisplayHint: "LDAP / Active Directory",
		Required:    []string{"host", "bindDN", "bindPW"},
		Optional:    []string{"insecureNoSSL", "insecureSkipVerify", "rootCAData", "startTLS", "usernamePrompt"},
		Secret:      []string{"bindPW"},
		Nested: []nestedRequirement{
			{Parent: "userSearch", Keys: []string{"baseDN", "username", "idAttr", "emailAttr"}},
		},
	},
	"oauth": {
		Type:        "oauth",
		DisplayHint: "Generic OAuth 2.0",
		Required:    []string{"clientID", "clientSecret", "tokenURL", "authorizationURL", "userInfoURL"},
		Optional:    []string{"redirectURI", "scopes", "userIDKey"},
		Secret:      []string{"clientSecret"},
	},
}

// dexConnectorTypes returns the registered connector types in deterministic
// order. Used by the handler's metadata endpoint and by the test that asserts
// the registry stays in sync with the migration's catalog.
func dexConnectorTypes() []string {
	out := make([]string, 0, len(dexConnectorRegistry))
	for k := range dexConnectorRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validateConnectorConfig returns nil when the supplied raw config satisfies
// the spec for connectorType, or an error listing the missing fields.
func validateConnectorConfig(connectorType string, raw map[string]any) error {
	spec, ok := dexConnectorRegistry[strings.ToLower(connectorType)]
	if !ok {
		return fmt.Errorf("unknown connector type %q", connectorType)
	}
	missing := make([]string, 0)
	for _, key := range spec.Required {
		v, ok := raw[key]
		if !ok || isEmptyValue(v) {
			missing = append(missing, key)
		}
	}
	for _, n := range spec.Nested {
		parent, ok := raw[n.Parent].(map[string]any)
		if !ok || len(parent) == 0 {
			missing = append(missing, n.Parent)
			continue
		}
		for _, key := range n.Keys {
			if v, ok := parent[key]; !ok || isEmptyValue(v) {
				missing = append(missing, n.Parent+"."+key)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func isEmptyValue(v any) bool {
	switch vv := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(vv) == ""
	case []any:
		return len(vv) == 0
	case map[string]any:
		return len(vv) == 0
	}
	return false
}

// encryptSecretFields walks raw and replaces every spec.Secret key with its
// Fernet-encrypted value. No-op when the encryptor is nil. Callers should
// pass a freshly-decoded map so we don't mutate shared state.
func (h *DexHandler) encryptSecretFields(connectorType string, raw map[string]any) error {
	if h == nil || h.encryptor == nil || raw == nil {
		return nil
	}
	spec, ok := dexConnectorRegistry[strings.ToLower(connectorType)]
	if !ok {
		return nil
	}
	for _, key := range spec.Secret {
		v, ok := raw[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		// Best-effort idempotency: if the value already decrypts cleanly we
		// leave it alone. This lets PATCHes that don't touch the secret round
		// trip without re-encrypting (and double-encrypting) the value.
		if _, err := h.encryptor.Decrypt(s); err == nil {
			continue
		}
		ct, err := h.encryptor.Encrypt(s)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", key, err)
		}
		raw[key] = ct
	}
	return nil
}

// decryptSecretFields walks raw and replaces every spec.Secret key with its
// plaintext value. Used at render time before the config goes into the Dex
// ConfigMap. Failures decrypting a single field are tolerated (the field is
// left as-is) so a misconfigured encryption key surface a runtime error in
// Dex rather than a 500 from /apply.
func (h *DexHandler) decryptSecretFields(connectorType string, raw map[string]any) {
	if h == nil || h.encryptor == nil || raw == nil {
		return
	}
	spec, ok := dexConnectorRegistry[strings.ToLower(connectorType)]
	if !ok {
		return
	}
	for _, key := range spec.Secret {
		v, ok := raw[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if pt, err := h.encryptor.Decrypt(s); err == nil {
			raw[key] = pt
		}
	}
}

// redactSecretFields returns a shallow clone of raw with every spec.Secret
// value replaced by an empty string. Used in API responses so the UI can show
// "(set)" without exposing ciphertext.
func redactSecretFields(connectorType string, raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	spec, ok := dexConnectorRegistry[strings.ToLower(connectorType)]
	if !ok {
		return out
	}
	for _, key := range spec.Secret {
		if v, ok := out[key]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				out[key] = ""
				out["__"+key+"_set"] = true
			}
		}
	}
	return out
}

// connectorRequest is the JSON shape POST/PATCH accepts.
type connectorRequest struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Config      map[string]any `json:"config"`
	Enabled     *bool          `json:"enabled,omitempty"`
}

// settingsRequest is the JSON shape PUT /settings accepts.
type settingsRequest struct {
	IssuerURL      string         `json:"issuer_url"`
	ClusterID      string         `json:"cluster_id"`
	Namespace      string         `json:"namespace"`
	ReleaseName    string         `json:"release_name"`
	ConfigmapName  string         `json:"configmap_name"`
	PublicClients  []map[string]any `json:"public_clients"`
	Expiry         map[string]any `json:"expiry"`
	Extra          map[string]any `json:"extra"`
}

// ListConnectorTypes exposes the registry so the UI can render its wizard.
// GET /api/v1/auth/dex/connector-types/
func (h *DexHandler) ListConnectorTypes(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(dexConnectorRegistry))
	for _, t := range dexConnectorTypes() {
		spec := dexConnectorRegistry[t]
		out = append(out, map[string]any{
			"type":         spec.Type,
			"display_hint": spec.DisplayHint,
			"required":     spec.Required,
			"optional":     spec.Optional,
			"secret":       spec.Secret,
			"nested":       nestedRequirementsToJSON(spec.Nested),
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

func nestedRequirementsToJSON(in []nestedRequirement) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, n := range in {
		out = append(out, map[string]any{"parent": n.Parent, "keys": n.Keys})
	}
	return out
}

// ListConnectors returns every persisted connector. GET /api/v1/auth/dex/connectors/
func (h *DexHandler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListDexConnectors(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list Dex connectors")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, h.connectorResponse(row))
	}
	RespondJSON(w, http.StatusOK, items)
}

// GetConnector returns a single connector by ID. GET /api/v1/auth/dex/connectors/{id}/
func (h *DexHandler) GetConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid connector ID")
		return
	}
	row, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Connector not found")
		return
	}
	RespondJSON(w, http.StatusOK, h.connectorResponse(row))
}

// CreateConnector validates + persists a new connector. POST /api/v1/auth/dex/connectors/
func (h *DexHandler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "invalid_name", "Connector name is required")
		return
	}
	if _, ok := dexConnectorRegistry[req.Type]; !ok {
		RespondError(w, http.StatusBadRequest, "invalid_type", fmt.Sprintf("Unknown connector type %q", req.Type))
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	if err := validateConnectorConfig(req.Type, req.Config); err != nil {
		RespondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.encryptSecretFields(req.Type, req.Config); err != nil {
		RespondError(w, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt secret fields")
		return
	}
	cfgBytes, err := json.Marshal(req.Config)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "marshal_error", "Failed to encode connector config")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.CreateDexConnector(r.Context(), sqlc.CreateDexConnectorParams{
		Name:        req.Name,
		Type:        req.Type,
		DisplayName: req.DisplayName,
		Config:      cfgBytes,
		Enabled:     enabled,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			RespondError(w, http.StatusConflict, "duplicate", "A connector with that name already exists")
			return
		}
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create connector")
		return
	}
	recordAudit(r, h.queries, "dex.connector.create", "dex_connector", row.ID.String(), row.Name, map[string]any{
		"type":    row.Type,
		"enabled": row.Enabled,
	})
	RespondJSON(w, http.StatusCreated, h.connectorResponse(row))
}

// UpdateConnector PATCHes an existing connector. PATCH /api/v1/auth/dex/connectors/{id}/
//
// We accept partial bodies: omitted fields keep their current value. The
// `config` map is treated as a full replacement when present (partial config
// merges would be ambiguous for things like `groups`).
func (h *DexHandler) UpdateConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Connector not found")
		return
	}
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	connectorType := existing.Type
	if t := strings.ToLower(strings.TrimSpace(req.Type)); t != "" {
		if _, ok := dexConnectorRegistry[t]; !ok {
			RespondError(w, http.StatusBadRequest, "invalid_type", fmt.Sprintf("Unknown connector type %q", t))
			return
		}
		connectorType = t
	}
	displayName := existing.DisplayName
	if req.DisplayName != "" {
		displayName = req.DisplayName
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cfgBytes := existing.Config
	if req.Config != nil {
		// Merge: any secret field left empty in the request is preserved from
		// the existing row (so the UI can PATCH without resending the secret).
		merged := mergeSecretFromExisting(connectorType, existing.Config, req.Config)
		if err := validateConnectorConfig(connectorType, merged); err != nil {
			RespondError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		if err := h.encryptSecretFields(connectorType, merged); err != nil {
			RespondError(w, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt secret fields")
			return
		}
		raw, err := json.Marshal(merged)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "marshal_error", "Failed to encode connector config")
			return
		}
		cfgBytes = raw
	}
	row, err := h.queries.UpdateDexConnector(r.Context(), sqlc.UpdateDexConnectorParams{
		ID:          id,
		Type:        connectorType,
		DisplayName: displayName,
		Config:      cfgBytes,
		Enabled:     enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update connector")
		return
	}
	recordAudit(r, h.queries, "dex.connector.update", "dex_connector", row.ID.String(), row.Name, map[string]any{
		"type":    row.Type,
		"enabled": row.Enabled,
	})
	RespondJSON(w, http.StatusOK, h.connectorResponse(row))
}

// DeleteConnector removes a connector. DELETE /api/v1/auth/dex/connectors/{id}/
func (h *DexHandler) DeleteConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Connector not found")
		return
	}
	if err := h.queries.DeleteDexConnector(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete connector")
		return
	}
	recordAudit(r, h.queries, "dex.connector.delete", "dex_connector", id.String(), existing.Name, map[string]any{
		"type": existing.Type,
	})
	RespondJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// GetSettings returns the singleton Dex settings row. GET /api/v1/auth/dex/settings/
//
// When the row hasn't been created yet (fresh install) we return zero values
// so the UI's first-time setup wizard can pre-fill defaults.
func (h *DexHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	row, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil {
		RespondJSON(w, http.StatusOK, defaultSettingsResponse())
		return
	}
	RespondJSON(w, http.StatusOK, settingsResponse(row))
}

// UpdateSettings upserts the singleton settings row. PUT /api/v1/auth/dex/settings/
func (h *DexHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		RespondError(w, http.StatusBadRequest, "missing_issuer", "issuer_url is required")
		return
	}
	if req.Namespace == "" {
		req.Namespace = "dex"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "dex"
	}
	if req.ConfigmapName == "" {
		req.ConfigmapName = "astronomer-dex-config"
	}
	clusterUUID := pgtype.UUID{}
	if req.ClusterID != "" {
		id, err := uuid.Parse(req.ClusterID)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
			return
		}
		clusterUUID = pgtype.UUID{Bytes: id, Valid: true}
	}
	publicBytes, _ := json.Marshal(req.PublicClients)
	if len(publicBytes) == 0 {
		publicBytes = []byte("[]")
	}
	expiryBytes, _ := json.Marshal(req.Expiry)
	if len(expiryBytes) == 0 || string(expiryBytes) == "null" {
		expiryBytes = []byte("{}")
	}
	extraBytes, _ := json.Marshal(req.Extra)
	if len(extraBytes) == 0 || string(extraBytes) == "null" {
		extraBytes = []byte("{}")
	}
	row, err := h.queries.UpsertDexSettings(r.Context(), sqlc.UpsertDexSettingsParams{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     strings.TrimRight(req.IssuerURL, "/"),
		ClusterID:     clusterUUID,
		Namespace:     req.Namespace,
		ReleaseName:   req.ReleaseName,
		ConfigmapName: req.ConfigmapName,
		PublicClients: publicBytes,
		Expiry:        expiryBytes,
		Extra:         extraBytes,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "save_error", "Failed to save Dex settings")
		return
	}
	recordAudit(r, h.queries, "dex.settings.update", "dex_settings", row.ID.String(), row.ReleaseName, map[string]any{
		"issuer_url":     row.IssuerUrl,
		"namespace":      row.Namespace,
		"configmap_name": row.ConfigmapName,
	})
	RespondJSON(w, http.StatusOK, settingsResponse(row))
}

// Apply renders the full Dex config from settings + connectors and writes it
// to the management cluster's ConfigMap so Dex hot-reloads. Returns 503 when
// the K8s requester is not configured (e.g. before the tunnel is up).
//
// POST /api/v1/auth/dex/apply/
func (h *DexHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if h.k8s == nil {
		RespondError(w, http.StatusServiceUnavailable, "tunnel_unavailable", "Kubernetes requester is not configured")
		return
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "no_settings", "Dex settings have not been configured yet; PUT /settings first")
		return
	}
	if settings.IssuerUrl == "" {
		RespondError(w, http.StatusBadRequest, "missing_issuer", "Dex settings have no issuer_url; PUT /settings first")
		return
	}
	if !settings.ClusterID.Valid {
		RespondError(w, http.StatusBadRequest, "missing_cluster", "Dex settings have no cluster_id; PUT /settings first")
		return
	}
	connectors, err := h.queries.ListEnabledDexConnectors(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list Dex connectors")
		return
	}
	configYAML, err := h.renderDexConfig(settings, connectors)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "render_error", err.Error())
		return
	}
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	if err := h.applyConfigMap(r.Context(), clusterID, settings.Namespace, settings.ConfigmapName, configYAML); err != nil {
		RespondError(w, http.StatusBadGateway, "apply_error", err.Error())
		return
	}
	recordAudit(r, h.queries, "dex.config.apply", "dex_settings", settings.ID.String(), settings.ReleaseName, map[string]any{
		"cluster_id":      clusterID,
		"namespace":       settings.Namespace,
		"configmap_name":  settings.ConfigmapName,
		"connector_count": len(connectors),
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"applied":         true,
		"cluster_id":      clusterID,
		"namespace":       settings.Namespace,
		"configmap_name":  settings.ConfigmapName,
		"connector_count": len(connectors),
		"applied_at":      time.Now().UTC().Format(time.RFC3339),
	})
}

// RegisterAsSSO is the one-click ergonomic helper that creates (or updates)
// the SSO row pointing at our installed Dex. The frontend can offer this
// once the operator has filled in dex_settings + at least one connector and
// applied them to the cluster.
//
// POST /api/v1/auth/dex/register-as-sso/
//
// Body (optional fields):
//
//	{
//	  "client_id":     "astronomer",
//	  "client_secret": "...plaintext... (will be encrypted)",
//	  "display_name":  "Sign in with Dex"
//	}
func (h *DexHandler) RegisterAsSSO(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		DisplayName  string `json:"display_name"`
	}
	if r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
			RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil || settings.IssuerUrl == "" {
		RespondError(w, http.StatusBadRequest, "no_settings", "Dex settings have not been configured yet")
		return
	}
	if req.ClientID == "" {
		req.ClientID = "astronomer"
	}
	if req.DisplayName == "" {
		req.DisplayName = "Sign in with Dex"
	}
	encryptedSecret := ""
	if req.ClientSecret != "" {
		if h.encryptor == nil {
			RespondError(w, http.StatusServiceUnavailable, "encrypt_unavailable", "Encryptor is not configured; cannot store client_secret")
			return
		}
		ct, err := h.encryptor.Encrypt(req.ClientSecret)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt client secret")
			return
		}
		encryptedSecret = ct
	}
	cfgBytes, _ := json.Marshal(map[string]any{"issuer_url": settings.IssuerUrl})
	// If the dex SSO row already exists, update it; otherwise create.
	existing, getErr := h.queries.GetSSOConfigurationByProvider(r.Context(), "dex")
	if getErr == nil {
		secretValue := existing.ClientSecretEncrypted
		if encryptedSecret != "" {
			secretValue = encryptedSecret
		}
		updated, err := h.queries.UpdateSSOConfiguration(r.Context(), sqlc.UpdateSSOConfigurationParams{
			ID:                    existing.ID,
			IsEnabled:             true,
			DisplayName:           req.DisplayName,
			Config:                cfgBytes,
			ClientID:              req.ClientID,
			ClientSecretEncrypted: secretValue,
			AllowedOrganizations:  existing.AllowedOrganizations,
			AllowedDomains:        existing.AllowedDomains,
			AutoCreateUsers:       existing.AutoCreateUsers,
			DefaultGlobalRoleID:   existing.DefaultGlobalRoleID,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "save_error", "Failed to update SSO row")
			return
		}
		recordAudit(r, h.queries, "dex.register_sso", "sso_configuration", updated.ID.String(), updated.Provider, map[string]any{
			"client_id":  updated.ClientID,
			"issuer_url": settings.IssuerUrl,
			"updated":    true,
		})
		RespondJSON(w, http.StatusOK, map[string]any{
			"provider":    updated.Provider,
			"id":          updated.ID.String(),
			"is_enabled":  updated.IsEnabled,
			"client_id":   updated.ClientID,
			"issuer_url":  settings.IssuerUrl,
			"display_name": updated.DisplayName,
			"updated":     true,
		})
		return
	}
	created, err := h.queries.CreateSSOConfiguration(r.Context(), sqlc.CreateSSOConfigurationParams{
		Provider:              "dex",
		IsEnabled:             true,
		DisplayName:           req.DisplayName,
		Config:                cfgBytes,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: encryptedSecret,
		AllowedOrganizations:  json.RawMessage(`[]`),
		AllowedDomains:        json.RawMessage(`[]`),
		AutoCreateUsers:       true,
		DefaultGlobalRoleID:   pgtype.UUID{},
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "save_error", "Failed to create SSO row")
		return
	}
	recordAudit(r, h.queries, "dex.register_sso", "sso_configuration", created.ID.String(), created.Provider, map[string]any{
		"client_id":  created.ClientID,
		"issuer_url": settings.IssuerUrl,
		"created":    true,
	})
	RespondJSON(w, http.StatusCreated, map[string]any{
		"provider":     created.Provider,
		"id":           created.ID.String(),
		"is_enabled":   created.IsEnabled,
		"client_id":    created.ClientID,
		"issuer_url":   settings.IssuerUrl,
		"display_name": created.DisplayName,
		"created":      true,
	})
}

// connectorResponse builds the JSON shape we return on every connector read.
// Sensitive fields are redacted.
func (h *DexHandler) connectorResponse(row sqlc.DexConnector) map[string]any {
	cfg := decodeJSONMap(row.Config)
	out := map[string]any{
		"id":           row.ID.String(),
		"name":         row.Name,
		"type":         row.Type,
		"display_name": row.DisplayName,
		"enabled":      row.Enabled,
		"config":       redactSecretFields(row.Type, cfg),
		"created_at":   row.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":   row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return out
}

func defaultSettingsResponse() map[string]any {
	return map[string]any{
		"issuer_url":      "",
		"cluster_id":      "",
		"namespace":       "dex",
		"release_name":    "dex",
		"configmap_name":  "astronomer-dex-config",
		"public_clients":  []any{},
		"expiry":          map[string]any{},
		"extra":           map[string]any{},
		"configured":      false,
	}
}

func settingsResponse(row sqlc.DexSetting) map[string]any {
	clusterID := ""
	if row.ClusterID.Valid {
		clusterID = uuid.UUID(row.ClusterID.Bytes).String()
	}
	return map[string]any{
		"issuer_url":      row.IssuerUrl,
		"cluster_id":      clusterID,
		"namespace":       row.Namespace,
		"release_name":    row.ReleaseName,
		"configmap_name":  row.ConfigmapName,
		"public_clients":  json.RawMessage(row.PublicClients),
		"expiry":          json.RawMessage(row.Expiry),
		"extra":           json.RawMessage(row.Extra),
		"configured":      true,
		"updated_at":      row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// renderDexConfig builds a Dex-shaped config document and serializes it to
// YAML. The output goes verbatim into the ConfigMap's `config.yaml` key. We
// keep this in handler-package code (rather than a sub-package) so tests can
// exercise it without exporting helpers.
func (h *DexHandler) renderDexConfig(settings sqlc.DexSetting, connectors []sqlc.DexConnector) ([]byte, error) {
	doc := map[string]any{
		"issuer": settings.IssuerUrl,
		"storage": map[string]any{
			"type": "kubernetes",
			"config": map[string]any{
				"inCluster": true,
			},
		},
		"web": map[string]any{
			"http": "0.0.0.0:5556",
		},
		"oauth2": map[string]any{
			"skipApprovalScreen": true,
		},
	}
	// Public + static clients: the operator can supply both from settings
	// (public_clients is a list of {id, name, redirectURIs, secret?, public?}).
	clients := make([]any, 0)
	if len(settings.PublicClients) > 0 {
		var raw []map[string]any
		if err := json.Unmarshal(settings.PublicClients, &raw); err == nil {
			for _, c := range raw {
				clients = append(clients, c)
			}
		}
	}
	if len(clients) > 0 {
		doc["staticClients"] = clients
	}
	// Expiry settings (idTokens, refreshTokens, ...). Forward whatever shape
	// the operator stored so we don't have to grow this code per Dex release.
	if len(settings.Expiry) > 0 {
		var ex map[string]any
		if err := json.Unmarshal(settings.Expiry, &ex); err == nil && len(ex) > 0 {
			doc["expiry"] = ex
		}
	}
	if len(settings.Extra) > 0 {
		var ex map[string]any
		if err := json.Unmarshal(settings.Extra, &ex); err == nil {
			for k, v := range ex {
				doc[k] = v
			}
		}
	}
	out := make([]map[string]any, 0, len(connectors))
	for _, c := range connectors {
		raw := decodeJSONMap(c.Config)
		h.decryptSecretFields(c.Type, raw)
		out = append(out, map[string]any{
			"type":   c.Type,
			"id":     c.Name,
			"name":   firstNonEmpty(c.DisplayName, c.Name),
			"config": raw,
		})
	}
	doc["connectors"] = out
	buf := &bytes.Buffer{}
	yamlBytes, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal dex config: %w", err)
	}
	buf.Write(yamlBytes)
	return buf.Bytes(), nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// applyConfigMap PATCHes (or POSTs, on 404) the ConfigMap holding Dex's config.
// The `config.yaml` key matches the volumeMount path the chart's defaults set
// up; Dex's in-process file watcher hot-reloads when the file changes.
func (h *DexHandler) applyConfigMap(ctx context.Context, clusterID, namespace, name string, configYAML []byte) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"app.kubernetes.io/component":  "dex-config",
			},
		},
		"data": map[string]any{
			"config.yaml": string(configYAML),
		},
	})
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
	resp, err = h.k8s.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		// Lost the race with another writer; PATCH should now succeed.
		resp, err = h.k8s.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
		if err != nil {
			return err
		}
	}
	return ensureSuccess(resp)
}

// mergeSecretFromExisting is the partial-update helper for PATCH /connectors/{id}.
// When the request omits a secret field (or sends it empty), keep the existing
// value so the UI can re-save form data without forcing the user to retype the
// secret. Non-secret fields are taken verbatim from req.
func mergeSecretFromExisting(connectorType string, existingRaw json.RawMessage, req map[string]any) map[string]any {
	merged := make(map[string]any, len(req))
	for k, v := range req {
		merged[k] = v
	}
	spec, ok := dexConnectorRegistry[strings.ToLower(connectorType)]
	if !ok {
		return merged
	}
	existing := decodeJSONMap(existingRaw)
	for _, key := range spec.Secret {
		v, ok := req[key]
		s, isStr := v.(string)
		if !ok || (isStr && strings.TrimSpace(s) == "") {
			if prev, ok := existing[key]; ok {
				merged[key] = prev
			}
		}
	}
	return merged
}
