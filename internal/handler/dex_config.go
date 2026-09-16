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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
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
	StageCreateDexConnector(ctx context.Context, arg sqlc.StageCreateDexConnectorParams) (sqlc.StageCreateDexConnectorRow, error)
	StageUpdateDexConnector(ctx context.Context, arg sqlc.StageUpdateDexConnectorParams) (sqlc.StageUpdateDexConnectorRow, error)
	StageDeleteDexConnector(ctx context.Context, connectorID uuid.UUID) (int64, error)

	GetDexSettings(ctx context.Context, id uuid.UUID) (sqlc.DexSetting, error)
	GetDexSettingsForGeneration(ctx context.Context, arg sqlc.GetDexSettingsForGenerationParams) (sqlc.DexSetting, error)
	StageDexSettingsAndDisableSSO(ctx context.Context, arg sqlc.StageDexSettingsAndDisableSSOParams) (int64, error)
	RestoreDexSSOForGeneration(ctx context.Context, arg sqlc.RestoreDexSSOForGenerationParams) (sqlc.RestoreDexSSOForGenerationRow, error)
	MarkDexRuntimeStaged(ctx context.Context, arg sqlc.MarkDexRuntimeStagedParams) (sqlc.DexSetting, error)
	MarkDexRuntimeApplied(ctx context.Context, arg sqlc.MarkDexRuntimeAppliedParams) (sqlc.DexSetting, error)
	BackfillDexPublicClientsEnvelope(ctx context.Context, arg sqlc.BackfillDexPublicClientsEnvelopeParams) (sqlc.DexSetting, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)

	// SSO bridge — register-as-sso writes here.
	GetSSOConfigurationByProvider(ctx context.Context, provider string) (sqlc.SsoConfiguration, error)
	CreateSSOConfiguration(ctx context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	UpdateSSOConfiguration(ctx context.Context, arg sqlc.UpdateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	EnableDexSSOForGeneration(ctx context.Context, arg sqlc.EnableDexSSOForGenerationParams) (sqlc.EnableDexSSOForGenerationRow, error)
}

// DexMutationTx is the transaction-bound identity-provider configuration
// surface. Connector/settings stages may also disable live Dex SSO; that
// security-relevant state change and its audit evidence must share one commit.
type DexMutationTx interface {
	audit.OutboxQuerier
	StageCreateDexConnector(context.Context, sqlc.StageCreateDexConnectorParams) (sqlc.StageCreateDexConnectorRow, error)
	StageUpdateDexConnector(context.Context, sqlc.StageUpdateDexConnectorParams) (sqlc.StageUpdateDexConnectorRow, error)
	StageDeleteDexConnector(context.Context, uuid.UUID) (int64, error)
	StageDexSettingsAndDisableSSO(context.Context, sqlc.StageDexSettingsAndDisableSSOParams) (int64, error)
	GetDexSettingsForGeneration(context.Context, sqlc.GetDexSettingsForGenerationParams) (sqlc.DexSetting, error)
	RestoreDexSSOForGeneration(context.Context, sqlc.RestoreDexSSOForGenerationParams) (sqlc.RestoreDexSSOForGenerationRow, error)
	EnableDexSSOForGeneration(context.Context, sqlc.EnableDexSSOForGenerationParams) (sqlc.EnableDexSSOForGenerationRow, error)
}

type dexRunTxFunc func(context.Context, func(DexMutationTx) error) error

// DexHandler exposes /api/v1/auth/dex/* endpoints.
type DexHandler struct {
	queries             DexQuerier
	runTx               dexRunTxFunc
	encryptor           *auth.Encryptor
	k8s                 K8sRequester
	log                 *slog.Logger
	rolloutPollInterval time.Duration
	rolloutTimeout      time.Duration
	bundledIdentity     *DexRuntimeIdentity
}

// DexRuntimeIdentity identifies chart-owned in-cluster Dex resources. It is
// resolved by internal/config and injected during server composition.
type DexRuntimeIdentity struct {
	Namespace, ChartReleaseName, DeploymentName, ServiceName, RuntimeSecretName string
	MigrationPhase                                                              string
}

// NewDexHandler constructs a Dex handler. queries is required; encryptor and
// k8s are optional at construction time. Secret-bearing writes and renders
// fail closed until an encryptor is configured; /apply returns 503 without a
// Kubernetes requester.
func NewDexHandler(queries DexQuerier) *DexHandler {
	return &DexHandler{
		queries:             queries,
		log:                 slog.Default(),
		rolloutPollInterval: 500 * time.Millisecond,
		rolloutTimeout:      60 * time.Second,
	}
}

// SetBundledRuntimeIdentity enables chart-owned Dex resource management.
func (h *DexHandler) SetBundledRuntimeIdentity(identity DexRuntimeIdentity) {
	if h != nil {
		h.bundledIdentity = &identity
	}
}

func (h *DexHandler) SetRunTx(runTx dexRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *DexHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
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

func validateDexRuntimeIdentity(identity DexRuntimeIdentity) error {
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
	if identity.MigrationPhase != "fresh" && identity.MigrationPhase != "prepare" && identity.MigrationPhase != "cutover" {
		return fmt.Errorf("invalid bundled Dex migration phase")
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
			row.RuntimeSecretName != identity.RuntimeSecretName || row.RuntimePhase != identity.MigrationPhase {
			return row, fmt.Errorf("stored Dex runtime identity does not match the bundled chart")
		}
		row.ReleaseName = identity.DeploymentName // compatibility field
		return row, nil
	}
	row.Namespace = cmp.Or(row.Namespace, "dex")
	row.DeploymentName = cmp.Or(row.DeploymentName, row.ReleaseName, "dex")
	row.ServiceName = cmp.Or(row.ServiceName, row.ReleaseName, row.DeploymentName)
	row.ReleaseName = row.DeploymentName
	row.RuntimeSecretName = cmp.Or(row.RuntimeSecretName, "astronomer-dex-runtime")
	row.RuntimePhase = cmp.Or(row.RuntimePhase, "fresh")
	identity := DexRuntimeIdentity{Namespace: row.Namespace, ChartReleaseName: cmp.Or(row.ChartReleaseName, "custom"), DeploymentName: row.DeploymentName, ServiceName: row.ServiceName, RuntimeSecretName: row.RuntimeSecretName, MigrationPhase: row.RuntimePhase}
	if err := validateDexRuntimeIdentity(identity); err != nil {
		return row, err
	}
	return row, nil
}

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
	canonical, err := dexconfig.CanonicalConnectorType(connectorType)
	if err != nil {
		return err
	}
	spec := dexConnectorRegistry[canonical]
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
	canonical, err := dexconfig.CanonicalConnectorType(connectorType)
	if err != nil {
		return err
	}
	spec := dexConnectorRegistry[canonical]
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
	canonical, err := dexconfig.CanonicalConnectorType(connectorType)
	if err != nil {
		return sanitizeDexMap(out)
	}
	spec := dexConnectorRegistry[canonical]
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
// openapi:request DexConnectorRequest
type connectorRequest struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Config      map[string]any `json:"config"`
	Enabled     *bool          `json:"enabled,omitempty"`
}

// settingsRequest is the JSON shape PUT /settings accepts.
// openapi:request DexSettingsRequest
type settingsRequest struct {
	IssuerURL         string           `json:"issuer_url"`
	ClusterID         string           `json:"cluster_id"`
	Namespace         string           `json:"namespace"`
	ReleaseName       string           `json:"release_name"`
	ChartReleaseName  string           `json:"chart_release_name"`
	DeploymentName    string           `json:"deployment_name"`
	ServiceName       string           `json:"service_name"`
	RuntimeSecretName string           `json:"runtime_secret_name"`
	PublicClients     []map[string]any `json:"public_clients"`
	Expiry            map[string]any   `json:"expiry"`
	Extra             map[string]any   `json:"extra"`
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
