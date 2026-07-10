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
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
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
	MarkDexRuntimeApplied(ctx context.Context, arg sqlc.MarkDexRuntimeAppliedParams) (sqlc.DexSetting, error)
	BackfillDexPublicClientsEnvelope(ctx context.Context, arg sqlc.BackfillDexPublicClientsEnvelopeParams) (sqlc.DexSetting, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)

	// SSO bridge — register-as-sso writes here.
	GetSSOConfigurationByProvider(ctx context.Context, provider string) (sqlc.SsoConfiguration, error)
	CreateSSOConfiguration(ctx context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	UpdateSSOConfiguration(ctx context.Context, arg sqlc.UpdateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	EnableDexSSOForGeneration(ctx context.Context, arg sqlc.EnableDexSSOForGenerationParams) (sqlc.SsoConfiguration, error)
}

// DexHandler exposes /api/v1/auth/dex/* endpoints.
type DexHandler struct {
	queries             DexQuerier
	encryptor           *auth.Encryptor
	k8s                 K8sRequester
	log                 *slog.Logger
	rolloutPollInterval time.Duration
	rolloutTimeout      time.Duration
	bundledIdentity     *dexRuntimeIdentity
}

type dexRuntimeIdentity struct {
	Namespace, ChartReleaseName, DeploymentName, ServiceName, RuntimeSecretName string
}

// NewDexHandler constructs a Dex handler. queries is required; encryptor and
// k8s are optional at construction time. Secret-bearing writes and renders
// fail closed until an encryptor is configured; /apply returns 503 without a
// Kubernetes requester.
func NewDexHandler(queries DexQuerier) *DexHandler {
	handler := &DexHandler{
		queries:             queries,
		log:                 slog.Default(),
		rolloutPollInterval: 500 * time.Millisecond,
		rolloutTimeout:      60 * time.Second,
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DEX_BUNDLED_ENABLED")), "true") {
		handler.bundledIdentity = &dexRuntimeIdentity{
			Namespace:         strings.TrimSpace(os.Getenv("DEX_BUNDLED_NAMESPACE")),
			ChartReleaseName:  strings.TrimSpace(os.Getenv("DEX_BUNDLED_RELEASE_NAME")),
			DeploymentName:    strings.TrimSpace(os.Getenv("DEX_BUNDLED_DEPLOYMENT_NAME")),
			ServiceName:       strings.TrimSpace(os.Getenv("DEX_BUNDLED_SERVICE_NAME")),
			RuntimeSecretName: strings.TrimSpace(os.Getenv("DEX_BUNDLED_RUNTIME_SECRET_NAME")),
		}
	}
	return handler
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

func validateDexRuntimeIdentity(identity dexRuntimeIdentity) error {
	if errs := k8svalidation.IsDNS1123Label(identity.Namespace); len(errs) > 0 {
		return fmt.Errorf("invalid bundled Dex namespace")
	}
	if errs := k8svalidation.IsDNS1123Label(identity.ChartReleaseName); len(errs) > 0 {
		return fmt.Errorf("invalid bundled Dex chart release")
	}
	for label, value := range map[string]string{"deployment": identity.DeploymentName, "service": identity.ServiceName, "runtime Secret": identity.RuntimeSecretName} {
		if errs := k8svalidation.IsDNS1123Subdomain(value); len(errs) > 0 {
			return fmt.Errorf("invalid bundled Dex %s name", label)
		}
	}
	return nil
}

func (h *DexHandler) normalizeRuntimeIdentity(row sqlc.DexSetting) (sqlc.DexSetting, error) {
	if h != nil && h.bundledIdentity != nil {
		identity := *h.bundledIdentity
		if err := validateDexRuntimeIdentity(identity); err != nil {
			return row, err
		}
		if row.Namespace != identity.Namespace || row.ChartReleaseName != identity.ChartReleaseName ||
			row.DeploymentName != identity.DeploymentName || row.ServiceName != identity.ServiceName ||
			row.RuntimeSecretName != identity.RuntimeSecretName {
			return row, fmt.Errorf("stored Dex runtime identity does not match the bundled chart")
		}
		row.ReleaseName = identity.DeploymentName // compatibility field
		return row, nil
	}
	row.Namespace = cmp.Or(row.Namespace, "dex")
	row.DeploymentName = cmp.Or(row.DeploymentName, row.ReleaseName, "dex")
	row.ServiceName = cmp.Or(row.ServiceName, row.ReleaseName, row.DeploymentName)
	row.ReleaseName = row.DeploymentName
	row.RuntimeSecretName = cmp.Or(row.RuntimeSecretName, row.ConfigmapName, "astronomer-dex-runtime")
	identity := dexRuntimeIdentity{Namespace: row.Namespace, ChartReleaseName: cmp.Or(row.ChartReleaseName, "custom"), DeploymentName: row.DeploymentName, ServiceName: row.ServiceName, RuntimeSecretName: row.RuntimeSecretName}
	if err := validateDexRuntimeIdentity(identity); err != nil {
		return row, err
	}
	return row, nil
}

type dexConnectorSpec = dexconfig.ConnectorSpec
type nestedRequirement = dexconfig.NestedRequirement

// dexConnectorRegistry is a read-only projection of the shared runtime
// contract used by both this API and dexconfigcheck.
var dexConnectorRegistry = dexconfig.Registry()

// dexConnectorTypes returns the registered connector types in deterministic
// order. Used by the handler's metadata endpoint and by the test that asserts
// the registry stays in sync with the migration's catalog.
func dexConnectorTypes() []string {
	return dexconfig.ConnectorTypes()
}

// validateConnectorConfig returns nil when the supplied raw config satisfies
// the spec for connectorType, or an error listing the missing fields.
func validateConnectorConfig(connectorType string, raw map[string]any) error {
	return dexconfig.ValidateConnector(connectorType, raw)
}

func validateCanonicalDexURL(raw string, requireHTTPS bool) error {
	return dexconfig.ValidateURL(raw, !requireHTTPS)
}

func normalizedDexKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
}

func sensitiveDexKey(key string) bool {
	normalized := normalizedDexKey(key)
	for _, fragment := range []string{"secret", "password", "passwd", "token", "apikey", "privatekey", "bindpw", "credential"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
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
	return sanitizeDexMap(out)
}

func sanitizeDexMap(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		normalized := normalizedDexKey(key)
		if sensitiveDexKey(key) && !strings.HasSuffix(normalized, "set") && !strings.HasSuffix(normalized, "configured") {
			if !isEmptyValue(value) {
				out[key] = redaction.Marker
			} else {
				out[key] = value
			}
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			out[key] = sanitizeDexMap(typed)
		case []any:
			items := make([]any, len(typed))
			for i, item := range typed {
				if object, ok := item.(map[string]any); ok {
					items[i] = sanitizeDexMap(object)
				} else {
					items[i] = item
				}
			}
			out[key] = items
		default:
			out[key] = value
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
	ChartReleaseName  string `json:"chart_release_name"`
	DeploymentName    string `json:"deployment_name"`
	ServiceName       string `json:"service_name"`
	RuntimeSecretName string `json:"runtime_secret_name"`
	// ConfigmapName is accepted for one compatibility release. It is treated
	// as a resource-name alias only and never causes a ConfigMap write.
	ConfigmapName string           `json:"configmap_name"`
	PublicClients []map[string]any `json:"public_clients"`
	Expiry        map[string]any   `json:"expiry"`
	Extra         map[string]any   `json:"extra"`
}

func decodeDexRequest(body io.Reader, target any, allowEmpty bool) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
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
		item, err := h.connectorResponse(row)
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "Stored Dex connector is invalid and must be repaired")
			return
		}
		items = append(items, item)
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
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "Stored Dex connector is invalid and must be repaired")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// CreateConnector validates + persists a new connector. POST /api/v1/auth/dex/connectors/
func (h *DexHandler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
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
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Saved Dex connector failed validation")
		return
	}
	RespondJSON(w, http.StatusCreated, response)
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
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	connectorType := existing.Type
	if t := strings.ToLower(strings.TrimSpace(req.Type)); t != "" {
		if _, ok := dexConnectorRegistry[t]; !ok {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, fmt.Sprintf("Unknown connector type %q", t))
			return
		}
		if t != existing.Type {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "connector type is immutable")
			return
		}
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
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Saved Dex connector failed validation")
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
	response, err := settingsResponse(row, clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "Stored Dex settings are invalid and must be repaired")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// UpdateSettings upserts the singleton settings row. PUT /api/v1/auth/dex/settings/
func (h *DexHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingIssuer, "issuer_url is required")
		return
	}
	if err := validateCanonicalDexURL(req.IssuerURL, true); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "issuer_url "+err.Error())
		return
	}
	existing, existingErr := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if h.bundledIdentity != nil {
		if existingErr != nil {
			RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Bundled Dex bootstrap settings are unavailable; restart the server after the chart is ready")
			return
		}
		if _, err := h.normalizeRuntimeIdentity(existing); err != nil {
			RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Bundled Dex runtime identity does not match the installed chart")
			return
		}
		identity := *h.bundledIdentity
		for field, requested := range map[string]string{
			"namespace": req.Namespace, "release_name": req.ReleaseName, "chart_release_name": req.ChartReleaseName,
			"deployment_name": req.DeploymentName, "service_name": req.ServiceName, "runtime_secret_name": cmp.Or(req.RuntimeSecretName, req.ConfigmapName),
		} {
			var expected string
			switch field {
			case "namespace":
				expected = identity.Namespace
			case "release_name", "deployment_name":
				expected = identity.DeploymentName
			case "chart_release_name":
				expected = identity.ChartReleaseName
			case "service_name":
				expected = identity.ServiceName
			default:
				expected = identity.RuntimeSecretName
			}
			if requested != "" && requested != expected {
				RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, field+" is immutable for bundled Dex")
				return
			}
		}
		req.Namespace, req.ReleaseName = identity.Namespace, identity.DeploymentName
		req.ChartReleaseName, req.DeploymentName, req.ServiceName = identity.ChartReleaseName, identity.DeploymentName, identity.ServiceName
		req.RuntimeSecretName = identity.RuntimeSecretName
	} else {
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
		if req.DeploymentName == "" {
			req.DeploymentName = req.ReleaseName
		}
		if req.ServiceName == "" {
			req.ServiceName = req.ReleaseName
		}
	}
	if errs := k8svalidation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "namespace must be a valid Kubernetes DNS label")
		return
	}
	if errs := k8svalidation.IsDNS1123Label(req.ReleaseName); len(errs) > 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "release_name must be a valid Kubernetes DNS label")
		return
	}
	for label, value := range map[string]string{"deployment_name": req.DeploymentName, "service_name": req.ServiceName} {
		if errs := k8svalidation.IsDNS1123Subdomain(value); len(errs) > 0 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, label+" must be a valid Kubernetes name")
			return
		}
	}
	if errs := k8svalidation.IsDNS1123Subdomain(req.RuntimeSecretName); len(errs) > 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "runtime_secret_name must be a valid Kubernetes Secret name")
		return
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
	if existingErr == nil {
		var loadErr error
		existingClients, _, loadErr = h.loadPublicClients(r.Context(), existing)
		if loadErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Dex static-client secrets are unavailable")
			return
		}
	}
	clients := mergePublicClientSecrets(existingClients, req.PublicClients)
	if h.bundledIdentity != nil {
		clients = existingClients
	}
	if err := validatePublicClients(clients); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured; cannot store Dex static clients")
		return
	}
	if h.bundledIdentity != nil {
		_ = json.Unmarshal(existing.Expiry, &req.Expiry)
		_ = json.Unmarshal(existing.Extra, &req.Extra)
	}
	if err := validateDexExtra(req.Extra); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	if err := validateDexExpiry(req.Expiry); err != nil {
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
	existingSSO, ssoErr := h.queries.GetSSOConfigurationByProvider(r.Context(), "dex")
	disabledForSettings := false
	if ssoErr == nil && existingSSO.IsEnabled {
		if _, err := h.writeDexSSO(r.Context(), existingSSO, false, existingSSO.DisplayName, existingSSO.Config, existingSSO.ClientID, existingSSO.ClientSecretEncrypted); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to disable Dex SSO before staging settings")
			return
		}
		disabledForSettings = true
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
		PublicClients:          mustDexJSON(clients, []byte("[]")),
		Expiry:                 expiryBytes,
		Extra:                  extraBytes,
		ChartReleaseName:       req.ChartReleaseName,
		DeploymentName:         req.DeploymentName,
		ServiceName:            req.ServiceName,
	})
	if err != nil {
		if disabledForSettings {
			_, _ = h.writeDexSSO(r.Context(), existingSSO, true, existingSSO.DisplayName, existingSSO.Config, existingSSO.ClientID, existingSSO.ClientSecretEncrypted)
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to save Dex settings")
		return
	}
	recordAudit(r, h.queries, "dex.settings.update", "dex_settings", row.ID.String(), row.ReleaseName, map[string]any{
		"issuer_url":          row.IssuerUrl,
		"namespace":           row.Namespace,
		"runtime_secret_name": row.RuntimeSecretName,
	})
	response, err := settingsResponse(row, clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Saved Dex settings failed validation")
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
	settings, err = h.normalizeRuntimeIdentity(settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Dex runtime identity does not match the installed chart")
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
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	changed, secretVersion, err := h.reconcileDexRuntime(r.Context(), settings, clients, connectors)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ApplyError, redaction.String(err.Error()))
		return
	}
	recordAudit(r, h.queries, "dex.config.apply", "dex_settings", settings.ID.String(), settings.ReleaseName, map[string]any{
		"cluster_id":          clusterID,
		"namespace":           settings.Namespace,
		"runtime_secret_name": settings.RuntimeSecretName,
		"configmap_name":      settings.RuntimeSecretName,
		"deployment_name":     settings.DeploymentName,
		"connector_count":     len(connectors),
		"changed":             changed,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"applied":                 true,
		"cluster_id":              clusterID,
		"namespace":               settings.Namespace,
		"runtime_secret_name":     settings.RuntimeSecretName,
		"configmap_name":          settings.RuntimeSecretName,
		"deployment_name":         settings.DeploymentName,
		"runtime_generation":      settings.RuntimeGeneration,
		"connector_count":         len(connectors),
		"changed":                 changed,
		"secret_resource_version": secretVersion,
		"applied_at":              time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *DexHandler) reconcileDexRuntime(ctx context.Context, settings sqlc.DexSetting, clients []map[string]any, connectors []sqlc.DexConnector) (bool, string, error) {
	configYAML, err := h.renderDexConfig(settings, clients, connectors)
	if err != nil {
		return false, "", err
	}
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	changed, secretVersion, err := h.applyRuntimeSecret(ctx, clusterID, settings.Namespace, settings.RuntimeSecretName, configYAML, settings.RuntimeGeneration)
	if err != nil {
		return false, "", err
	}
	if secretVersion == "" {
		return false, "", fmt.Errorf("Dex runtime Secret update returned no resourceVersion")
	}
	if err := h.verifyDexRuntimeSecret(ctx, clusterID, settings.Namespace, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration, configYAML); err != nil {
		return false, "", err
	}
	ready := false
	if !changed {
		ready, _ = h.dexDeploymentReadyOnce(ctx, clusterID, settings.Namespace, settings.DeploymentName, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration)
	}
	if !ready {
		if err := h.restartDeployment(ctx, clusterID, settings.Namespace, settings.DeploymentName, secretVersion, settings.RuntimeGeneration); err != nil {
			return false, "", err
		}
		if err := h.waitForDexDeploymentReady(ctx, clusterID, settings.Namespace, settings.DeploymentName, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration); err != nil {
			return false, "", err
		}
	}
	if err := h.verifyDexHealth(ctx, clusterID, settings.Namespace, settings.ServiceName); err != nil {
		return false, "", err
	}
	if _, err := h.queries.MarkDexRuntimeApplied(ctx, sqlc.MarkDexRuntimeAppliedParams{ID: settings.ID, RuntimeGeneration: settings.RuntimeGeneration}); err != nil {
		return false, "", fmt.Errorf("Dex runtime generation became stale before activation")
	}
	return changed, secretVersion, nil
}

func (h *DexHandler) dexDeploymentReadyOnce(ctx context.Context, clusterID, namespace, name, runtimeSecretName, resourceVersion string, generation int64) (bool, error) {
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return false, err
	}
	if err := ensureSuccess(resp); err != nil {
		return false, err
	}
	var deployment dexDeployment
	if err := parseJSONResponse(resp, &deployment); err != nil {
		return false, err
	}
	return dexDeploymentReady(deployment, runtimeSecretName, resourceVersion, generation), nil
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
		if err := decodeDexRequest(r.Body, &req, true); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	if h.k8s == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnavailable, "Kubernetes requester is not configured")
		return
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil || settings.IssuerUrl == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoSettings, "Dex settings have not been configured yet")
		return
	}
	if !settings.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingCluster, "Dex settings require a target cluster before SSO registration")
		return
	}
	settings, err = h.normalizeRuntimeIdentity(settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Dex runtime identity does not match the installed chart")
		return
	}
	connectors, err := h.queries.ListEnabledDexConnectors(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list enabled Dex connectors")
		return
	}
	if len(connectors) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "At least one enabled Dex connector is required before SSO registration")
		return
	}
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	if err := h.verifyDexRuntimeIdentity(r.Context(), clusterID, settings.Namespace, settings.RuntimeSecretName); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ApplyError, "Dex runtime Secret is not prepared and owned by the bundled deployment")
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
	existingPlainSecret := ""
	if !created {
		if existing.ClientSecretEncrypted != "" {
			if h.encryptor == nil {
				RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured; cannot synchronize client_secret")
				return
			}
			existingPlainSecret, err = h.encryptor.Decrypt(existing.ClientSecretEncrypted)
			if err != nil {
				RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Existing Dex client secret is unavailable")
				return
			}
		}
		if secretValue == "" {
			secretValue = existing.ClientSecretEncrypted
			staticSecret = existingPlainSecret
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
	if err := validatePublicClients(clients); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Dex SSO static client is invalid")
		return
	}
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Failed to encrypt Dex public clients")
		return
	}
	candidate := settings
	candidate.PublicClients = mustDexJSON(clients, []byte("[]"))
	candidate.PublicClientsEncrypted = encryptedClients
	if _, err := h.renderDexConfig(candidate, clients, connectors); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.RenderError, "Dex runtime candidate is invalid")
		return
	}
	disabledForRollout := false
	// Any reconciliation can expose a newly staged connector or extension
	// configuration, even when the client credential pair itself is unchanged.
	// Keep login fail-closed until the exact Secret generation, Deployment
	// rollout, and health endpoint have all been verified.
	if !created && existing.IsEnabled {
		if _, err := h.writeDexSSO(r.Context(), existing, false, existing.DisplayName, existing.Config, existing.ClientID, existing.ClientSecretEncrypted); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to safely disable Dex SSO before runtime verification")
			return
		}
		disabledForRollout = true
	}
	staged, err := h.queries.UpsertDexSettings(r.Context(), sqlc.UpsertDexSettingsParams{
		ID: settings.ID, IssuerUrl: settings.IssuerUrl, ClusterID: settings.ClusterID,
		Namespace: settings.Namespace, ReleaseName: settings.ReleaseName,
		ConfigmapName: settings.RuntimeSecretName, RuntimeSecretName: settings.RuntimeSecretName,
		PublicClients: candidate.PublicClients, PublicClientsEncrypted: encryptedClients,
		Expiry: settings.Expiry, Extra: settings.Extra,
		ChartReleaseName: settings.ChartReleaseName, DeploymentName: settings.DeploymentName, ServiceName: settings.ServiceName,
	})
	if err != nil {
		if disabledForRollout {
			_, _ = h.writeDexSSO(r.Context(), existing, true, existing.DisplayName, existing.Config, existing.ClientID, existing.ClientSecretEncrypted)
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Failed to stage Dex static-client settings")
		return
	}
	changed, secretVersion, err := h.reconcileDexRuntime(r.Context(), staged, clients, connectors)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ApplyError, "Dex SSO remains disabled because the verified runtime rollout did not complete")
		return
	}
	cfgBytes, _ := json.Marshal(map[string]any{"issuer_url": settings.IssuerUrl})
	row, err := h.queries.EnableDexSSOForGeneration(r.Context(), sqlc.EnableDexSSOForGenerationParams{
		DisplayName: req.DisplayName, Config: cfgBytes, ClientID: req.ClientID,
		ClientSecretEncrypted: secretValue, RuntimeGeneration: staged.RuntimeGeneration,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SaveError, "Dex runtime is healthy but server SSO remains disabled; retry registration")
		return
	}
	action := "updated"
	status := http.StatusOK
	if created {
		action = "created"
		status = http.StatusCreated
	}
	recordAudit(r, h.queries, "dex.register_sso", "sso_configuration", row.ID.String(), row.Provider, map[string]any{
		"client_id":               row.ClientID,
		"issuer_url":              settings.IssuerUrl,
		"secret_resource_version": secretVersion,
		"runtime_changed":         changed,
		"runtime_generation":      staged.RuntimeGeneration,
		action:                    true,
	})
	RespondJSON(w, status, map[string]any{
		"provider":                row.Provider,
		"id":                      row.ID.String(),
		"is_enabled":              row.IsEnabled,
		"client_id":               row.ClientID,
		"issuer_url":              settings.IssuerUrl,
		"display_name":            row.DisplayName,
		"verified":                true,
		"secret_resource_version": secretVersion,
		"runtime_changed":         changed,
		action:                    true,
	})
}

func (h *DexHandler) writeDexSSO(ctx context.Context, existing sqlc.SsoConfiguration, enabled bool, displayName string, config json.RawMessage, clientID, encryptedSecret string) (sqlc.SsoConfiguration, error) {
	organizations := existing.AllowedOrganizations
	domains := existing.AllowedDomains
	if len(organizations) == 0 {
		organizations = json.RawMessage(`[]`)
	}
	if len(domains) == 0 {
		domains = json.RawMessage(`[]`)
	}
	if existing.ID != uuid.Nil {
		return h.queries.UpdateSSOConfiguration(ctx, sqlc.UpdateSSOConfigurationParams{
			ID: existing.ID, IsEnabled: enabled, DisplayName: displayName, Config: config,
			ClientID: clientID, ClientSecretEncrypted: encryptedSecret,
			AllowedOrganizations: organizations, AllowedDomains: domains,
			AutoCreateUsers: existing.AutoCreateUsers, DefaultGlobalRoleID: existing.DefaultGlobalRoleID,
		})
	}
	created, err := h.queries.CreateSSOConfiguration(ctx, sqlc.CreateSSOConfigurationParams{
		Provider: "dex", IsEnabled: enabled, DisplayName: displayName, Config: config,
		ClientID: clientID, ClientSecretEncrypted: encryptedSecret,
		AllowedOrganizations: organizations, AllowedDomains: domains,
		AutoCreateUsers: true,
	})
	if err == nil {
		return created, nil
	}
	// A concurrent or retried registration may have created the provider after
	// our initial lookup. Re-read and converge through the same idempotent update.
	current, getErr := h.queries.GetSSOConfigurationByProvider(ctx, "dex")
	if getErr != nil {
		return sqlc.SsoConfiguration{}, err
	}
	return h.writeDexSSO(ctx, current, enabled, displayName, config, clientID, encryptedSecret)
}

func (h *DexHandler) astronomerPublicClients(ctx context.Context, settings sqlc.DexSetting, clientID, clientSecret string) ([]map[string]any, sqlc.DexSetting, error) {
	if h == nil || h.queries == nil {
		return nil, settings, nil
	}
	cfg, err := h.queries.GetPlatformConfig(ctx)
	if err != nil {
		return nil, settings, fmt.Errorf("load platform callback configuration")
	}
	if strings.TrimSpace(cfg.ServerUrl) == "" {
		return nil, settings, fmt.Errorf("platform callback URL is not configured")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.ServerUrl), "/")
	if err := validateCanonicalDexURL(base, true); err != nil {
		return nil, settings, fmt.Errorf("platform callback URL is invalid")
	}
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

// loadPublicClients uses the compatibility copy until the durable cutover
// marker is set. Before cutover it opportunistically backfills (but never
// scrubs) the envelope. After cutover only the envelope is authoritative.
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
		if err := validatePublicClients(clients); err != nil {
			return nil, fmt.Errorf("validate Dex static clients")
		}
		return clients, nil
	}
	// Before the explicit cutover, public_clients is the compatibility source of
	// truth because a previous binary may still update it. New binaries dual-write
	// the envelope but never scrub while old replicas can be serving.
	if !row.PublicClientsCutoverAt.Valid {
		clients, err := decode(row.PublicClients)
		if err != nil || len(clients) == 0 || row.PublicClientsEncrypted != "" {
			return clients, row, err
		}
		encrypted, err := h.encryptPublicClients(clients)
		if err != nil {
			return nil, row, err
		}
		backfilled, err := h.queries.BackfillDexPublicClientsEnvelope(ctx, sqlc.BackfillDexPublicClientsEnvelopeParams{
			ID: row.ID, PublicClientsEncrypted: encrypted, LegacyPublicClients: row.PublicClients,
		})
		if err == nil {
			if backfilled.RuntimeSecretName == "" {
				backfilled.RuntimeSecretName = row.RuntimeSecretName
			}
			return clients, backfilled, nil
		}
		latest, readErr := h.queries.GetDexSettings(ctx, row.ID)
		if readErr != nil {
			return nil, row, fmt.Errorf("backfill Dex static-client envelope")
		}
		latestClients, decodeErr := decode(latest.PublicClients)
		return latestClients, latest, decodeErr
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
	// Empty ciphertext is the canonical encoding for an empty client array.
	// The cutover constraint guarantees the compatibility copy is also empty.
	return []map[string]any{}, row, nil
}

func mustDexJSON(value any, fallback []byte) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(fallback)
	}
	return raw
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

func validatePublicClients(clients []map[string]any) error {
	return dexconfig.ValidateStaticClients(clients)
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
		out = append(out, sanitizeDexMap(copyClient))
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
func (h *DexHandler) connectorResponse(row sqlc.DexConnector) (map[string]any, error) {
	cfg := decodeJSONMap(row.Config)
	if err := validateConnectorConfig(row.Type, cfg); err != nil {
		return nil, err
	}
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
	return out, nil
}

func defaultSettingsResponse() map[string]any {
	return map[string]any{
		"issuer_url":          "",
		"cluster_id":          "",
		"namespace":           "dex",
		"release_name":        "dex",
		"chart_release_name":  "",
		"deployment_name":     "dex",
		"service_name":        "dex",
		"runtime_secret_name": "astronomer-dex-runtime",
		"configmap_name":      "astronomer-dex-runtime",
		"public_clients":      []any{},
		"expiry":              map[string]any{},
		"extra":               map[string]any{},
		"configured":          false,
	}
}

func settingsResponse(row sqlc.DexSetting, clients []map[string]any) (map[string]any, error) {
	expiry, extra, err := validatedDexExtensions(row.Expiry, row.Extra)
	if err != nil {
		return nil, err
	}
	clusterID := ""
	if row.ClusterID.Valid {
		clusterID = uuid.UUID(row.ClusterID.Bytes).String()
	}
	return map[string]any{
		"issuer_url":                 row.IssuerUrl,
		"cluster_id":                 clusterID,
		"namespace":                  row.Namespace,
		"release_name":               row.ReleaseName,
		"chart_release_name":         row.ChartReleaseName,
		"deployment_name":            row.DeploymentName,
		"service_name":               row.ServiceName,
		"runtime_secret_name":        row.RuntimeSecretName,
		"configmap_name":             row.RuntimeSecretName,
		"public_clients":             redactPublicClients(clients),
		"expiry":                     expiry,
		"extra":                      extra,
		"configured":                 true,
		"runtime_generation":         row.RuntimeGeneration,
		"runtime_applied_generation": row.RuntimeAppliedGeneration,
		"updated_at":                 row.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// renderDexConfig builds a Dex-shaped config document and serializes it to
// YAML. The output goes verbatim into the runtime Secret's `config.yaml` key. We
// keep this in handler-package code (rather than a sub-package) so tests can
// exercise it without exporting helpers.
func (h *DexHandler) renderDexConfig(settings sqlc.DexSetting, publicClients []map[string]any, connectors []sqlc.DexConnector) ([]byte, error) {
	if err := validateCanonicalDexURL(settings.IssuerUrl, true); err != nil {
		return nil, fmt.Errorf("stored Dex issuer URL is invalid")
	}
	if err := validatePublicClients(publicClients); err != nil && len(publicClients) > 0 {
		return nil, fmt.Errorf("stored Dex static clients are invalid")
	}
	expiry, extra, err := validatedDexExtensions(settings.Expiry, settings.Extra)
	if err != nil {
		return nil, fmt.Errorf("stored Dex extension settings are invalid")
	}
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
	if len(expiry) > 0 {
		doc["expiry"] = expiry
	}
	for k, v := range extra {
		doc[k] = v
	}
	out := make([]map[string]any, 0, len(connectors))
	for _, c := range connectors {
		raw := decodeJSONMap(c.Config)
		if err := validateConnectorConfig(c.Type, raw); err != nil {
			return nil, fmt.Errorf("stored connector %q is invalid", c.Name)
		}
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
	if err := dexconfig.ValidateRuntimeYAML(yamlBytes, 1<<20); err != nil {
		return nil, fmt.Errorf("validate rendered Dex config: %w", err)
	}
	buf.Write(yamlBytes)
	return buf.Bytes(), nil
}

func validateDexExtra(extra map[string]any) error {
	return dexconfig.ValidateExtra(extra)
}

func validateDexExpiry(expiry map[string]any) error {
	return dexconfig.ValidateExpiry(expiry)
}

func validatedDexExtensions(expiryRaw, extraRaw json.RawMessage) (map[string]any, map[string]any, error) {
	decode := func(raw json.RawMessage) (map[string]any, error) {
		out := map[string]any{}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return out, nil
		}
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	expiry, err := decode(expiryRaw)
	if err != nil || validateDexExpiry(expiry) != nil {
		return nil, nil, fmt.Errorf("invalid expiry")
	}
	extra, err := decode(extraRaw)
	if err != nil || validateDexExtra(extra) != nil {
		return nil, nil, fmt.Errorf("invalid extra")
	}
	return expiry, extra, nil
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
	dexRuntimeManagedByLabel       = "astronomer.io/runtime-writer"
	dexRuntimePurposeLabel         = "astronomer.io/secret-purpose"
	dexRuntimeGenerationAnnotation = "astronomer.io/dex-runtime-generation"
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

type dexDeployment struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas int32 `json:"replicas"`
		Template struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Volumes []struct {
					Name   string `json:"name"`
					Secret *struct {
						SecretName string `json:"secretName"`
					} `json:"secret,omitempty"`
				} `json:"volumes"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration  int64 `json:"observedGeneration"`
		UpdatedReplicas     int32 `json:"updatedReplicas"`
		ReadyReplicas       int32 `json:"readyReplicas"`
		AvailableReplicas   int32 `json:"availableReplicas"`
		UnavailableReplicas int32 `json:"unavailableReplicas"`
		Conditions          []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// applyRuntimeSecret creates or resourceVersion-replaces the stable Secret
// mounted by Dex. Existing data is compared in memory for a fixed point;
// no content-derived hash is persisted because static-client secrets may have
// low entropy. Identical repeat applies do not mutate or roll the Deployment.
// A pre-existing Secret must carry our ownership labels; this prevents a name
// collision from silently overwriting an operator-owned credential.
func (h *DexHandler) applyRuntimeSecret(ctx context.Context, clusterID, namespace, name string, configYAML []byte, generations ...int64) (bool, string, error) {
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
	generation := int64(1)
	if len(generations) > 0 {
		generation = generations[0]
	}
	if generation <= 0 {
		return false, "", fmt.Errorf("Dex runtime generation is invalid")
	}
	existingGeneration, _ := strconv.ParseInt(existing.Metadata.Annotations[dexRuntimeGenerationAnnotation], 10, 64)
	if existingGeneration > generation {
		return false, "", fmt.Errorf("Dex runtime generation is stale; retry from current settings")
	}
	desired := newDexRuntimeSecret(namespace, name, configYAML, generation)
	contentChanged := existing.Data["config.yaml"] != desired.Data["config.yaml"]
	metadataCurrent := existing.Type == desired.Type && containsDexMetadata(existing.Metadata.Labels, desired.Metadata.Labels) &&
		containsDexMetadata(existing.Metadata.Annotations, desired.Metadata.Annotations)
	if existingGeneration == generation && contentChanged {
		return false, "", fmt.Errorf("Dex runtime content changed without a new database generation")
	}
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

func newDexRuntimeSecret(namespace, name string, configYAML []byte, generations ...int64) dexRuntimeSecret {
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
	if len(generations) > 0 {
		secret.Metadata.Annotations[dexRuntimeGenerationAnnotation] = strconv.FormatInt(generations[0], 10)
	}
	return secret
}

func containsDexMetadata(existing, desired map[string]string) bool {
	for key, value := range desired {
		if existing[key] != value {
			return false
		}
	}
	return true
}

func (h *DexHandler) restartDeployment(ctx context.Context, clusterID, namespace, name, secretResourceVersion string, generation int64) error {
	body, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"astronomer.io/dex-runtime-resource-version": secretResourceVersion,
						dexRuntimeGenerationAnnotation:               strconv.FormatInt(generation, 10),
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

func (h *DexHandler) verifyDexRuntimeSecret(ctx context.Context, clusterID, namespace, name, resourceVersion string, generation int64, configYAML []byte) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var secret dexRuntimeSecret
	if err := parseJSONResponse(resp, &secret); err != nil {
		return fmt.Errorf("decode verified Dex runtime Secret")
	}
	if secret.Metadata.ResourceVersion != resourceVersion || resourceVersion == "" {
		return fmt.Errorf("Dex runtime Secret resourceVersion changed before rollout")
	}
	if secret.Metadata.Labels[dexRuntimeManagedByLabel] != "dex-handler" || secret.Metadata.Labels[dexRuntimePurposeLabel] != "dex-runtime" {
		return fmt.Errorf("Dex runtime Secret lost required ownership labels")
	}
	if secret.Metadata.Annotations[dexRuntimeGenerationAnnotation] != strconv.FormatInt(generation, 10) {
		return fmt.Errorf("Dex runtime Secret generation changed before rollout")
	}
	if secret.Data["config.yaml"] != base64.StdEncoding.EncodeToString(configYAML) {
		return fmt.Errorf("Dex runtime Secret content changed before rollout")
	}
	return nil
}

func (h *DexHandler) verifyDexRuntimeIdentity(ctx context.Context, clusterID, namespace, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("Dex runtime Secret name is empty")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var secret dexRuntimeSecret
	if err := parseJSONResponse(resp, &secret); err != nil {
		return fmt.Errorf("decode Dex runtime Secret identity")
	}
	if secret.Metadata.Labels[dexRuntimeManagedByLabel] != "dex-handler" || secret.Metadata.Labels[dexRuntimePurposeLabel] != "dex-runtime" || secret.Type != "Opaque" {
		return fmt.Errorf("Dex runtime Secret does not have the required ownership identity")
	}
	return nil
}

func (h *DexHandler) waitForDexDeploymentReady(ctx context.Context, clusterID, namespace, name, runtimeSecretName, resourceVersion string, generation int64) error {
	timeout := h.rolloutTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	poll := h.rolloutPollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	for {
		resp, err := h.k8s.Do(deadlineCtx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
		if err == nil && ensureSuccess(resp) == nil {
			var deployment dexDeployment
			if parseJSONResponse(resp, &deployment) == nil && dexDeploymentReady(deployment, runtimeSecretName, resourceVersion, generation) {
				return nil
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
			return fmt.Errorf("Dex Deployment did not become ready with the verified runtime Secret")
		case <-timer.C:
		}
	}
}

func dexDeploymentReady(deployment dexDeployment, runtimeSecretName, resourceVersion string, generation int64) bool {
	if deployment.Metadata.Generation <= 0 || deployment.Status.ObservedGeneration < deployment.Metadata.Generation ||
		deployment.Spec.Replicas <= 0 || deployment.Status.UpdatedReplicas != deployment.Spec.Replicas ||
		deployment.Status.ReadyReplicas != deployment.Spec.Replicas || deployment.Status.AvailableReplicas != deployment.Spec.Replicas ||
		deployment.Status.UnavailableReplicas != 0 || deployment.Spec.Template.Metadata.Annotations["astronomer.io/dex-runtime-resource-version"] != resourceVersion ||
		deployment.Spec.Template.Metadata.Annotations[dexRuntimeGenerationAnnotation] != strconv.FormatInt(generation, 10) {
		return false
	}
	secretMounted := false
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "config" && volume.Secret != nil && volume.Secret.SecretName == runtimeSecretName {
			secretMounted = true
		}
	}
	available := false
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == "Available" && condition.Status == "True" {
			available = true
		}
	}
	return secretMounted && available
}

func (h *DexHandler) verifyDexHealth(ctx context.Context, clusterID, namespace, serviceName string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:5556/proxy/healthz", namespace, serviceName)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return fmt.Errorf("Dex health endpoint is not ready")
	}
	return nil
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
