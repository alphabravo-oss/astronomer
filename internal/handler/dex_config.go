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
//     retained runtime Secret name, static clients, expiry).
//   * A connector-type registry mapping each connector kind to its required +
//     optional + secret config fields. Validation runs against this registry on
//     every write.
//   * Rendering settings + connectors into a Dex-shaped YAML document stored
//     only in a retained Kubernetes Secret mounted read-only by Dex.
//   * `register-as-sso` ergonomics: one-click row in `sso_configurations` so the
//     A1 OIDC discovery path can register Dex as a normal OIDC provider.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
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
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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
	UpsertDexSettingsAndSSO(ctx context.Context, arg sqlc.UpsertDexSettingsAndSSOParams) (sqlc.SsoConfiguration, error)
	MigrateLegacyDexPublicClients(ctx context.Context, arg sqlc.MigrateLegacyDexPublicClientsParams) (sqlc.DexSetting, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)

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
// k8s are optional at construction time. Secret-bearing writes and renders
// fail closed until an encryptor is configured; /apply returns 503 without a
// Kubernetes requester.
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
// Fernet-encrypted value. Secret-bearing input fails closed when the
// encryptor is unavailable. Callers should pass a freshly-decoded map.
func (h *DexHandler) encryptSecretFields(connectorType string, raw map[string]any) error {
	if h == nil || raw == nil {
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
		if h.encryptor == nil {
			return fmt.Errorf("encrypt %s: encryptor is not configured", key)
		}
		// Existing ciphertext is decrypted then re-encrypted with the active
		// primary key. This is both double-encryption protection and the online
		// key-rotation path used when a connector is re-saved.
		if plaintext, err := h.encryptor.Decrypt(s); err == nil {
			s = plaintext
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
// plaintext value. Used only while rendering the in-memory document that is
// written to the runtime Secret. Missing keys and decrypt failures fail closed
// so ciphertext can never be handed to Dex as if it were a credential.
func (h *DexHandler) decryptSecretFields(connectorType string, raw map[string]any) error {
	if h == nil || raw == nil {
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
		if h.encryptor == nil {
			return fmt.Errorf("decrypt %s: encryptor is not configured", key)
		}
		pt, err := h.encryptor.Decrypt(s)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", key, err)
		}
		raw[key] = pt
	}
	return nil
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
	IssuerURL         string `json:"issuer_url"`
	ClusterID         string `json:"cluster_id"`
	Namespace         string `json:"namespace"`
	ReleaseName       string `json:"release_name"`
	RuntimeSecretName string `json:"runtime_secret_name"`
	// ConfigmapName is accepted for one compatibility release. It is treated
	// as a resource-name alias only and never causes a ConfigMap write.
	ConfigmapName string           `json:"configmap_name"`
	PublicClients []map[string]any `json:"public_clients"`
	Expiry        map[string]any   `json:"expiry"`
	Extra         map[string]any   `json:"extra"`
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list Dex connectors")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	row, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	RespondJSON(w, http.StatusOK, h.connectorResponse(row))
}

// CreateConnector validates + persists a new connector. POST /api/v1/auth/dex/connectors/
func (h *DexHandler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Connector name is required")
		return
	}
	if _, ok := dexConnectorRegistry[req.Type]; !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, fmt.Sprintf("Unknown connector type %q", req.Type))
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	if err := validateConnectorConfig(req.Type, req.Config); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	if err := h.encryptSecretFields(req.Type, req.Config); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt secret fields")
		return
	}
	cfgBytes, err := json.Marshal(req.Config)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MarshalError, "Failed to encode connector config")
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
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A connector with that name already exists")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create connector")
		return
	}
	recordAudit(r, h.queries, "dex.connector.create", "dex_connector", row.ID.String(), row.Name, map[string]any{
		"type":    row.Type,
		"enabled": row.Enabled,
	})
	w.Header().Set("Location", "/api/v1/auth/dex/connectors/"+row.ID.String()+"/")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	connectorType := existing.Type
	if t := strings.ToLower(strings.TrimSpace(req.Type)); t != "" {
		if _, ok := dexConnectorRegistry[t]; !ok {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, fmt.Sprintf("Unknown connector type %q", t))
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
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
		if err := h.encryptSecretFields(connectorType, merged); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt secret fields")
			return
		}
		raw, err := json.Marshal(merged)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.MarshalError, "Failed to encode connector config")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update connector")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	if err := h.queries.DeleteDexConnector(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete connector")
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
	clients, row, err := h.loadPublicClients(r.Context(), row)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Dex static-client secrets are unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, settingsResponse(row, clients))
}

// UpdateSettings upserts the singleton settings row. PUT /api/v1/auth/dex/settings/
func (h *DexHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingIssuer, "issuer_url is required")
		return
	}
	if req.Namespace == "" {
		req.Namespace = "dex"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "dex"
	}
	if req.RuntimeSecretName == "" {
		req.RuntimeSecretName = req.ConfigmapName
	}
	if req.RuntimeSecretName == "" {
		req.RuntimeSecretName = "astronomer-dex-runtime"
	}
	clusterUUID := pgtype.UUID{}
	if req.ClusterID != "" {
		id, err := uuid.Parse(req.ClusterID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster_id")
			return
		}
		clusterUUID = pgtype.UUID{Bytes: id, Valid: true}
	}
	existingClients := []map[string]any{}
	if existing, getErr := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID); getErr == nil {
		var loadErr error
		existingClients, _, loadErr = h.loadPublicClients(r.Context(), existing)
		if loadErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Dex static-client secrets are unavailable")
			return
		}
	}
	clients := mergePublicClientSecrets(existingClients, req.PublicClients)
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured; cannot store Dex static clients")
		return
	}
	if err := validateDexExtra(req.Extra); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
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
		ID:                     dexSettingsSingletonID,
		IssuerUrl:              strings.TrimRight(req.IssuerURL, "/"),
		ClusterID:              clusterUUID,
		Namespace:              req.Namespace,
		ReleaseName:            req.ReleaseName,
		ConfigmapName:          req.RuntimeSecretName,
		RuntimeSecretName:      req.RuntimeSecretName,
		PublicClientsEncrypted: encryptedClients,
		Expiry:                 expiryBytes,
		Extra:                  extraBytes,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to save Dex settings")
		return
	}
	recordAudit(r, h.queries, "dex.settings.update", "dex_settings", row.ID.String(), row.ReleaseName, map[string]any{
		"issuer_url":          row.IssuerUrl,
		"namespace":           row.Namespace,
		"runtime_secret_name": row.RuntimeSecretName,
	})
	RespondJSON(w, http.StatusOK, settingsResponse(row, clients))
}

// Apply renders the full Dex config from settings + connectors, patches the
// management cluster's retained runtime Secret, then rolls the Deployment only
// when content changed. Returns 503 when the K8s
// requester is not configured (e.g. before the tunnel is up).
//
// POST /api/v1/auth/dex/apply/
func (h *DexHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if h.k8s == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnavailable, "Kubernetes requester is not configured")
		return
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoSettings, "Dex settings have not been configured yet; PUT /settings first")
		return
	}
	if settings.IssuerUrl == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingIssuer, "Dex settings have no issuer_url; PUT /settings first")
		return
	}
	if !settings.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingCluster, "Dex settings have no cluster_id; PUT /settings first")
		return
	}
	connectors, err := h.queries.ListEnabledDexConnectors(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list Dex connectors")
		return
	}
	clients, settings, err := h.loadPublicClients(r.Context(), settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Dex static-client secrets are unavailable")
		return
	}
	configYAML, err := h.renderDexConfig(settings, clients, connectors)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RenderError, err.Error())
		return
	}
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	changed, secretVersion, err := h.applyRuntimeSecret(r.Context(), clusterID, settings.Namespace, settings.RuntimeSecretName, configYAML)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ApplyError, err.Error())
		return
	}
	if changed {
		if err := h.restartDeployment(r.Context(), clusterID, settings.Namespace, settings.ReleaseName, secretVersion); err != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.RestartError, err.Error())
			return
		}
	}
	recordAudit(r, h.queries, "dex.config.apply", "dex_settings", settings.ID.String(), settings.ReleaseName, map[string]any{
		"cluster_id":          clusterID,
		"namespace":           settings.Namespace,
		"runtime_secret_name": settings.RuntimeSecretName,
		"deployment_name":     settings.ReleaseName,
		"connector_count":     len(connectors),
		"changed":             changed,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"applied":             true,
		"cluster_id":          clusterID,
		"namespace":           settings.Namespace,
		"runtime_secret_name": settings.RuntimeSecretName,
		"deployment_name":     settings.ReleaseName,
		"connector_count":     len(connectors),
		"changed":             changed,
		"applied_at":          time.Now().UTC().Format(time.RFC3339),
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
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil || settings.IssuerUrl == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoSettings, "Dex settings have not been configured yet")
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
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured; cannot store client_secret")
			return
		}
		ct, err := h.encryptor.Encrypt(req.ClientSecret)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt client secret")
			return
		}
		encryptedSecret = ct
	}
	existing, getErr := h.queries.GetSSOConfigurationByProvider(r.Context(), "dex")
	created := getErr != nil
	staticSecret := req.ClientSecret
	secretValue := encryptedSecret
	allowedOrganizations := json.RawMessage(`[]`)
	allowedDomains := json.RawMessage(`[]`)
	autoCreateUsers := true
	defaultGlobalRoleID := pgtype.UUID{}
	if !created {
		allowedOrganizations = existing.AllowedOrganizations
		allowedDomains = existing.AllowedDomains
		autoCreateUsers = existing.AutoCreateUsers
		defaultGlobalRoleID = existing.DefaultGlobalRoleID
		if secretValue == "" {
			secretValue = existing.ClientSecretEncrypted
			if secretValue != "" {
				if h.encryptor == nil {
					RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured; cannot synchronize client_secret")
					return
				}
				staticSecret, err = h.encryptor.Decrypt(secretValue)
				if err != nil {
					RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Existing Dex client secret is unavailable")
					return
				}
			}
		}
	} else if staticSecret == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "client_secret is required when registering Dex SSO")
		return
	}
	clients, settings, err := h.astronomerPublicClients(r.Context(), settings, req.ClientID, staticSecret)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Failed to build Dex public client settings")
		return
	}
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Failed to encrypt Dex public clients")
		return
	}
	cfgBytes, _ := json.Marshal(map[string]any{"issuer_url": settings.IssuerUrl})
	row, err := h.queries.UpsertDexSettingsAndSSO(r.Context(), sqlc.UpsertDexSettingsAndSSOParams{
		DisplayName: req.DisplayName, SsoConfig: cfgBytes, ClientID: req.ClientID,
		ClientSecretEncrypted: secretValue, AllowedOrganizations: allowedOrganizations,
		AllowedDomains: allowedDomains, AutoCreateUsers: autoCreateUsers, DefaultGlobalRoleID: defaultGlobalRoleID,
		SettingsID: settings.ID, IssuerUrl: settings.IssuerUrl, ClusterID: settings.ClusterID,
		Namespace: settings.Namespace, ReleaseName: settings.ReleaseName, RuntimeSecretName: settings.RuntimeSecretName,
		PublicClientsEncrypted: encryptedClients, Expiry: settings.Expiry, Extra: settings.Extra,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to atomically synchronize Dex and server SSO credentials")
		return
	}
	action := "updated"
	status := http.StatusOK
	if created {
		action = "created"
		status = http.StatusCreated
	}
	recordAudit(r, h.queries, "dex.register_sso", "sso_configuration", row.ID.String(), row.Provider, map[string]any{
		"client_id":  row.ClientID,
		"issuer_url": settings.IssuerUrl,
		action:       true,
	})
	RespondJSON(w, status, map[string]any{
		"provider":     row.Provider,
		"id":           row.ID.String(),
		"is_enabled":   row.IsEnabled,
		"client_id":    row.ClientID,
		"issuer_url":   settings.IssuerUrl,
		"display_name": row.DisplayName,
		action:         true,
	})
}

func (h *DexHandler) astronomerPublicClients(ctx context.Context, settings sqlc.DexSetting, clientID, clientSecret string) ([]map[string]any, sqlc.DexSetting, error) {
	if h == nil || h.queries == nil {
		return nil, settings, nil
	}
	cfg, err := h.queries.GetPlatformConfig(ctx)
	if err != nil || strings.TrimSpace(cfg.ServerUrl) == "" {
		return nil, settings, nil
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.ServerUrl), "/")
	callback := base + "/api/v1/auth/callback/dex"
	callbackSlash := callback + "/"

	clients, settings, err := h.loadPublicClients(ctx, settings)
	if err != nil {
		return nil, settings, err
	}
	found := false
	for i := range clients {
		id, _ := clients[i]["id"].(string)
		if id != clientID {
			continue
		}
		found = true
		clients[i]["name"] = cmp.Or(asString(clients[i]["name"]), "Astronomer")
		if clientSecret != "" {
			clients[i]["secret"] = clientSecret
		}
		clients[i]["redirectURIs"] = mergeStringList(clients[i]["redirectURIs"], []string{callback, callbackSlash})
	}
	if !found {
		client := map[string]any{
			"id":           clientID,
			"name":         "Astronomer",
			"redirectURIs": []string{callback, callbackSlash},
		}
		if clientSecret != "" {
			client["secret"] = clientSecret
		}
		clients = append(clients, client)
	}

	return clients, settings, nil
}

func (h *DexHandler) encryptPublicClients(clients []map[string]any) (string, error) {
	if len(clients) == 0 {
		return "", nil
	}
	if h == nil || h.encryptor == nil {
		return "", fmt.Errorf("encrypt Dex static clients: encryptor is not configured")
	}
	raw, err := json.Marshal(clients)
	if err != nil {
		return "", fmt.Errorf("encode Dex static clients: %w", err)
	}
	value, err := h.encryptor.Encrypt(string(raw))
	if err != nil {
		return "", fmt.Errorf("encrypt Dex static clients: %w", err)
	}
	return value, nil
}

// loadPublicClients decrypts the canonical envelope. For rows created before
// DEX-01 it performs a deterministic, optimistic migration: encrypt the exact
// parsed client array, conditionally scrub the matching plaintext JSONB, and
// re-read if another replica won the race.
func (h *DexHandler) loadPublicClients(ctx context.Context, row sqlc.DexSetting) ([]map[string]any, sqlc.DexSetting, error) {
	if row.RuntimeSecretName == "" {
		row.RuntimeSecretName = cmp.Or(row.ConfigmapName, "astronomer-dex-runtime")
	}
	decode := func(raw []byte) ([]map[string]any, error) {
		clients := make([]map[string]any, 0)
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return clients, nil
		}
		if err := json.Unmarshal(raw, &clients); err != nil {
			return nil, fmt.Errorf("decode Dex static clients: %w", err)
		}
		return clients, nil
	}
	if row.PublicClientsEncrypted != "" {
		if h == nil || h.encryptor == nil {
			return nil, row, fmt.Errorf("decrypt Dex static clients: encryptor is not configured")
		}
		plain, err := h.encryptor.Decrypt(row.PublicClientsEncrypted)
		if err != nil {
			return nil, row, fmt.Errorf("decrypt Dex static clients: %w", err)
		}
		clients, err := decode([]byte(plain))
		return clients, row, err
	}
	clients, err := decode(row.PublicClients)
	if err != nil || len(clients) == 0 {
		return clients, row, err
	}
	encrypted, err := h.encryptPublicClients(clients)
	if err != nil {
		return nil, row, err
	}
	migrated, err := h.queries.MigrateLegacyDexPublicClients(ctx, sqlc.MigrateLegacyDexPublicClientsParams{
		ID:                     row.ID,
		PublicClientsEncrypted: encrypted,
		LegacyPublicClients:    row.PublicClients,
	})
	if err == nil {
		if migrated.RuntimeSecretName == "" {
			migrated.RuntimeSecretName = row.RuntimeSecretName
		}
		return clients, migrated, nil
	}
	latest, readErr := h.queries.GetDexSettings(ctx, row.ID)
	if readErr != nil || latest.PublicClientsEncrypted == "" {
		return nil, row, fmt.Errorf("migrate legacy Dex static clients: %w", err)
	}
	if h.encryptor == nil {
		return nil, latest, fmt.Errorf("decrypt migrated Dex static clients: encryptor is not configured")
	}
	plain, decryptErr := h.encryptor.Decrypt(latest.PublicClientsEncrypted)
	if decryptErr != nil {
		return nil, latest, fmt.Errorf("decrypt migrated Dex static clients: %w", decryptErr)
	}
	latestClients, decodeErr := decode([]byte(plain))
	return latestClients, latest, decodeErr
}

func mergePublicClientSecrets(existing, requested []map[string]any) []map[string]any {
	secrets := make(map[string]string, len(existing))
	for _, client := range existing {
		if id := asString(client["id"]); id != "" {
			secrets[id] = asString(client["secret"])
		}
	}
	out := make([]map[string]any, 0, len(requested))
	for _, client := range requested {
		copyClient := make(map[string]any, len(client))
		for key, value := range client {
			if key != "secret_configured" && key != "secretConfigured" && key != "__secret_set" {
				copyClient[key] = value
			}
		}
		if asString(copyClient["secret"]) == "" {
			if secret := secrets[asString(copyClient["id"])]; secret != "" {
				copyClient["secret"] = secret
			}
		}
		out = append(out, copyClient)
	}
	return out
}

func redactPublicClients(clients []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(clients))
	for _, client := range clients {
		copyClient := make(map[string]any, len(client)+1)
		for key, value := range client {
			copyClient[key] = value
		}
		if secret := asString(copyClient["secret"]); secret != "" {
			copyClient["secret"] = ""
			copyClient["secret_configured"] = true
		} else {
			delete(copyClient, "secret")
			copyClient["secret_configured"] = false
		}
		out = append(out, copyClient)
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func mergeStringList(existing any, add []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	switch vals := existing.(type) {
	case []string:
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	case []any:
		for _, raw := range vals {
			v, _ := raw.(string)
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	for _, v := range add {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
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
		"issuer_url":          "",
		"cluster_id":          "",
		"namespace":           "dex",
		"release_name":        "dex",
		"runtime_secret_name": "astronomer-dex-runtime",
		"public_clients":      []any{},
		"expiry":              map[string]any{},
		"extra":               map[string]any{},
		"configured":          false,
	}
}

func settingsResponse(row sqlc.DexSetting, clients []map[string]any) map[string]any {
	clusterID := ""
	if row.ClusterID.Valid {
		clusterID = uuid.UUID(row.ClusterID.Bytes).String()
	}
	return map[string]any{
		"issuer_url":          row.IssuerUrl,
		"cluster_id":          clusterID,
		"namespace":           row.Namespace,
		"release_name":        row.ReleaseName,
		"runtime_secret_name": row.RuntimeSecretName,
		"public_clients":      redactPublicClients(clients),
		"expiry":              json.RawMessage(row.Expiry),
		"extra":               json.RawMessage(row.Extra),
		"configured":          true,
		"updated_at":          row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// renderDexConfig builds a Dex-shaped config document and serializes it to
// YAML. The output goes verbatim into the runtime Secret's `config.yaml` key. We
// keep this in handler-package code (rather than a sub-package) so tests can
// exercise it without exporting helpers.
func (h *DexHandler) renderDexConfig(settings sqlc.DexSetting, publicClients []map[string]any, connectors []sqlc.DexConnector) ([]byte, error) {
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
	clients := make([]any, 0, len(publicClients))
	for _, client := range publicClients {
		clients = append(clients, client)
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
			if err := validateDexExtra(ex); err != nil {
				return nil, err
			}
			for k, v := range ex {
				doc[k] = v
			}
		}
	}
	out := make([]map[string]any, 0, len(connectors))
	for _, c := range connectors {
		raw := decodeJSONMap(c.Config)
		if err := h.decryptSecretFields(c.Type, raw); err != nil {
			return nil, fmt.Errorf("connector %q: %w", c.Name, err)
		}
		if spec, ok := dexConnectorRegistry[strings.ToLower(c.Type)]; ok && containsField(spec.Optional, "redirectURI") {
			if isEmptyValue(raw["redirectURI"]) {
				raw["redirectURI"] = strings.TrimRight(settings.IssuerUrl, "/") + "/callback"
			}
		}
		out = append(out, map[string]any{
			"type":   c.Type,
			"id":     c.Name,
			"name":   cmp.Or(c.DisplayName, c.Name),
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

func validateDexExtra(extra map[string]any) error {
	for _, key := range []string{"issuer", "storage", "web", "oauth2", "staticClients", "connectors", "expiry"} {
		if _, exists := extra[key]; exists {
			return fmt.Errorf("extra.%s is reserved and must be configured through its dedicated settings surface", key)
		}
	}
	return nil
}

func containsField(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

const (
	dexRuntimeManagedByLabel = "astronomer.io/runtime-writer"
	dexRuntimePurposeLabel   = "astronomer.io/secret-purpose"
)

type dexRuntimeSecret struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		ResourceVersion string            `json:"resourceVersion,omitempty"`
		Labels          map[string]string `json:"labels,omitempty"`
		Annotations     map[string]string `json:"annotations,omitempty"`
	} `json:"metadata"`
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

// applyRuntimeSecret creates or resourceVersion-replaces the stable Secret
// mounted by Dex. Existing data is compared in memory for a fixed point;
// no content-derived hash is persisted because static-client secrets may have
// low entropy. Identical repeat applies do not mutate or roll the Deployment.
// A pre-existing Secret must carry our ownership labels; this prevents a name
// collision from silently overwriting an operator-owned credential.
func (h *DexHandler) applyRuntimeSecret(ctx context.Context, clusterID, namespace, name string, configYAML []byte) (bool, string, error) {
	if strings.TrimSpace(name) == "" {
		return false, "", fmt.Errorf("Dex runtime_secret_name is empty")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return false, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, "", fmt.Errorf("Dex runtime Secret %s/%s does not exist; install or upgrade the chart to create the retained metadata-only Secret, then retry apply", namespace, name)
	}
	if err := ensureSuccess(resp); err != nil {
		return false, "", err
	}
	var existing dexRuntimeSecret
	if err := parseJSONResponse(resp, &existing); err != nil {
		return false, "", fmt.Errorf("decode existing Dex runtime Secret: %w", err)
	}
	if existing.Metadata.Labels[dexRuntimeManagedByLabel] != "dex-handler" ||
		existing.Metadata.Labels[dexRuntimePurposeLabel] != "dex-runtime" {
		return false, "", fmt.Errorf("refusing to overwrite Secret %s/%s without Dex runtime ownership labels", namespace, name)
	}
	desired := newDexRuntimeSecret(namespace, name, configYAML)
	contentChanged := existing.Data["config.yaml"] != desired.Data["config.yaml"]
	metadataCurrent := existing.Metadata.Annotations["helm.sh/resource-policy"] == "keep" &&
		existing.Metadata.Annotations["argocd.argoproj.io/sync-options"] == "Prune=false,Delete=false" &&
		existing.Metadata.Annotations["argocd.argoproj.io/compare-options"] == "IgnoreExtraneous"
	if !contentChanged && metadataCurrent {
		return false, existing.Metadata.ResourceVersion, nil
	}
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": existing.Metadata.ResourceVersion,
			"labels":          desired.Metadata.Labels,
			"annotations":     desired.Metadata.Annotations,
		},
		"type": desired.Type,
		"data": desired.Data,
	})
	if err != nil {
		return false, "", err
	}
	updated, err := h.k8s.Do(ctx, clusterID, http.MethodPatch, path, body, requestHeaders("application/merge-patch+json"))
	if err != nil {
		return false, "", err
	}
	if updated.StatusCode == http.StatusConflict {
		return false, "", fmt.Errorf("Dex runtime Secret changed concurrently; retry apply")
	}
	if err := ensureSuccess(updated); err != nil {
		return false, "", err
	}
	var result dexRuntimeSecret
	if err := parseJSONResponse(updated, &result); err != nil {
		return false, "", fmt.Errorf("decode updated Dex runtime Secret: %w", err)
	}
	return contentChanged, result.Metadata.ResourceVersion, nil
}

func newDexRuntimeSecret(namespace, name string, configYAML []byte) dexRuntimeSecret {
	secret := dexRuntimeSecret{
		APIVersion: "v1",
		Kind:       "Secret",
		Type:       "Opaque",
		Data: map[string]string{
			"config.yaml": base64.StdEncoding.EncodeToString(configYAML),
		},
	}
	secret.Metadata.Name = name
	secret.Metadata.Namespace = namespace
	secret.Metadata.Labels = map[string]string{
		dexRuntimeManagedByLabel:              "dex-handler",
		"app.kubernetes.io/component":         "dex-runtime",
		dexRuntimePurposeLabel:                "dex-runtime",
		"astronomer.io/backup-reconstruction": "encrypted-management-db",
	}
	secret.Metadata.Annotations = map[string]string{
		"helm.sh/resource-policy":            "keep",
		"argocd.argoproj.io/sync-options":    "Prune=false,Delete=false",
		"argocd.argoproj.io/compare-options": "IgnoreExtraneous",
	}
	return secret
}

func (h *DexHandler) restartDeployment(ctx context.Context, clusterID, namespace, name, secretResourceVersion string) error {
	body, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"astronomer.io/dex-runtime-resource-version": secretResourceVersion,
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodPatch, path, body, requestHeaders("application/merge-patch+json"))
	if err != nil {
		return err
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
