package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type MonitoringHandler struct {
	requester K8sRequester
	queries   MonitoringQuerier
	helm      HelmRequester
	log       *slog.Logger
	authz     authorizationSupport
	mu        sync.Mutex
	triggerCh chan struct{}
	// folderTriggerCh wakes folder-per-cluster Grafana provisioning.
	// Distinct from triggerCh so a cluster create/delete does not also
	// drain the Helm operations queue.
	folderTriggerCh chan struct{}
	// helmConcurrency caps the number of executeMonitoringOperation
	// goroutines dispatched per reconciler tick.
	helmConcurrency int
	// encryptor seals/unseals monitoring_backends.auth_config (migration 146).
	// Optional only in development: config.ValidateProductionSecurity refuses
	// to start a production server without one. When nil, the credential is
	// written to the plaintext JSONB column exactly as it was before 146,
	// which is the row shape the resolver's legacy branch already handles.
	encryptor *auth.Encryptor
	// grafanaTickets is the dedicated mint/redeem store (prefix grafana-ticket:).
	// Not StreamTicketStore. Nil disables the bounce endpoints.
	grafanaTickets *auth.GrafanaTicketStore
	users          UserByIDQuerier
	serverURL      string
	proxyImage     string
	grafanaExpose  GrafanaExpose
	sessionTTL     func(context.Context) time.Duration
	systemOutputs  systemLoggingOutputDisabler
}

// systemLoggingOutputDisabler turns off per-cluster Astronomer Loki destinations
// when the shared Loki family is uninstalled. LoggingHandler implements it.
type systemLoggingOutputDisabler interface {
	DisableSystemOutputsOnLokiUninstall(ctx context.Context) error
}

func (h *MonitoringHandler) SetSystemLoggingOutputDisabler(d systemLoggingOutputDisabler) {
	if h == nil {
		return
	}
	h.systemOutputs = d
}

// GrafanaExpose describes how grafana-proxy is published. Gateway (platform
// HTTPRoute) is preferred when GatewayClass is set; otherwise Ingress.
type GrafanaExpose struct {
	GatewayClass      string
	IngressClass      string
	GatewayName       string
	PlatformNamespace string
	TLSIssuerName     string
	TLSIssuerKind     string
}

// SetEncryptor wires the Fernet encryptor used for the monitoring-backend
// credential at rest (migration 146).
func (h *MonitoringHandler) SetEncryptor(encryptor *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = encryptor
}

// monitoringDecryptor / monitoringSealer return h.encryptor narrowed to one
// direction, or a genuinely nil interface when none is wired. Returning
// h.encryptor directly would hand back a non-nil interface holding a nil
// *auth.Encryptor, and every nil guard downstream would pass straight into a
// nil-receiver Decrypt.
func (h *MonitoringHandler) monitoringDecryptor() imonitoring.Decryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

func (h *MonitoringHandler) monitoringSealer() imonitoring.Encryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

type MonitoringQuerier interface {
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	GetClusterMonitoringConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterMonitoringConfig, error)
	GetClusterMonitoringContext(ctx context.Context, clusterID uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
	UpsertClusterMonitoringConfig(ctx context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error)
	CreateMonitoringOperation(ctx context.Context, arg sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error)
	GetLatestMonitoringOperationForTarget(ctx context.Context, arg sqlc.GetLatestMonitoringOperationForTargetParams) (sqlc.MonitoringOperation, error)
	GetMonitoringOperation(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	ListMonitoringOperations(ctx context.Context, arg sqlc.ListMonitoringOperationsParams) ([]sqlc.MonitoringOperation, error)
	ListMonitoringOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.MonitoringOperationEvent, error)
	ListPendingMonitoringOperations(ctx context.Context, limit int32) ([]sqlc.MonitoringOperation, error)
	MarkMonitoringOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationFailed(ctx context.Context, arg sqlc.MarkMonitoringOperationFailedParams) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationSuperseded(ctx context.Context, arg sqlc.MarkMonitoringOperationSupersededParams) (sqlc.MonitoringOperation, error)
	RequeueMonitoringOperation(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	CreateMonitoringOperationEvent(ctx context.Context, arg sqlc.CreateMonitoringOperationEventParams) (sqlc.MonitoringOperationEvent, error)
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	ListNotificationChannels(ctx context.Context, arg sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error)
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	ListAlertRuleChannelsByRules(ctx context.Context, ruleIds []uuid.UUID) ([]sqlc.AlertRuleChannel, error)
}

func NewMonitoringHandler() *MonitoringHandler {
	return &MonitoringHandler{log: slog.Default(), triggerCh: make(chan struct{}, 1), folderTriggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithRequester(requester K8sRequester) *MonitoringHandler {
	return &MonitoringHandler{requester: requester, log: slog.Default(), triggerCh: make(chan struct{}, 1), folderTriggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithQueries(queries MonitoringQuerier, requester K8sRequester) *MonitoringHandler {
	return &MonitoringHandler{queries: queries, requester: requester, log: slog.Default(), triggerCh: make(chan struct{}, 1), folderTriggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithDeps(queries MonitoringQuerier, requester K8sRequester, helm HelmRequester) *MonitoringHandler {
	return &MonitoringHandler{queries: queries, requester: requester, helm: helm, log: slog.Default(), triggerCh: make(chan struct{}, 1), folderTriggerCh: make(chan struct{}, 1)}
}

type UpdateMonitoringBackendRequest struct {
	BackendType                  string          `json:"backendType"`
	QueryURL                     string          `json:"queryUrl"`
	AlertmanagerURL              string          `json:"alertmanagerUrl"`
	TenantID                     string          `json:"tenantId"`
	AuthType                     string          `json:"authType"`
	AuthConfig                   json.RawMessage `json:"authConfig"`
	DefaultStepSeconds           int32           `json:"defaultStepSeconds"`
	TimeoutSeconds               int32           `json:"timeoutSeconds"`
	DefaultAutoRollbackOnFailure *bool           `json:"defaultAutoRollbackOnFailure"`
	MaxRetryAttempts             int32           `json:"maxRetryAttempts"`
}

type UpdateClusterMonitoringConfigRequest struct {
	BackendID               *uuid.UUID `json:"backendId"`
	ClusterLabel            string     `json:"clusterLabel"`
	ClusterLabelValue       string     `json:"clusterLabelValue"`
	ScrapeIntervalSeconds   int32      `json:"scrapeIntervalSeconds"`
	Retention               string     `json:"retention"`
	StackNamespace          string     `json:"stackNamespace"`
	PrometheusReleaseName   string     `json:"prometheusReleaseName"`
	ThanosSidecarEnabled    bool       `json:"thanosSidecarEnabled"`
	StorageConfigID         string     `json:"storageConfigId"`
	ObjectStorageSecretName string     `json:"objectStorageSecretName"`
	StorageClass            string     `json:"storageClass"`
	StorageSize             string     `json:"storageSize"`
	Status                  string     `json:"status"`
}

type MonitoringStackRequest struct {
	ReleaseName             string `json:"releaseName"`
	Namespace               string `json:"namespace"`
	Retention               string `json:"retention"`
	StorageClass            string `json:"storageClass"`
	StorageSize             string `json:"storageSize"`
	ScrapeInterval          string `json:"scrapeInterval"`
	ClusterLabel            string `json:"clusterLabel"`
	ClusterLabelValue       string `json:"clusterLabelValue"`
	PrometheusVersion       string `json:"prometheusVersion"`
	ChartVersion            string `json:"chartVersion"`
	StorageConfigID         string `json:"storageConfigId"`
	ObjectStorageSecretName string `json:"objectStorageSecretName"`
	// EnableGrafana omitted: true, except false when sharedGrafana is
	// healthy and the cluster stack is not_configured (changelog'd).
	EnableGrafana         *bool `json:"enableGrafana"`
	EnableAlertmanager    *bool `json:"enableAlertmanager"`
	ThanosSidecarEnabled  *bool `json:"thanosSidecarEnabled"`
	AutoRollbackOnFailure *bool `json:"autoRollbackOnFailure"`
}

type SharedThanosStackRequest struct {
	ManagementClusterID     string `json:"managementClusterId"`
	Namespace               string `json:"namespace"`
	ReleaseName             string `json:"releaseName"`
	ChartVersion            string `json:"chartVersion"`
	StorageConfigID         string `json:"storageConfigId"`
	ObjectStorageSecretName string `json:"objectStorageSecretName"`
	QueryReplicas           int32  `json:"queryReplicas"`
	StoreGatewayReplicas    int32  `json:"storeGatewayReplicas"`
	CompactorReplicas       int32  `json:"compactorReplicas"`
	AutoRollbackOnFailure   *bool  `json:"autoRollbackOnFailure"`
}

type SharedAlertmanagerRequest struct {
	ManagementClusterID   string `json:"managementClusterId"`
	Namespace             string `json:"namespace"`
	ReleaseName           string `json:"releaseName"`
	ChartVersion          string `json:"chartVersion"`
	Replicas              int32  `json:"replicas"`
	StorageClass          string `json:"storageClass"`
	StorageSize           string `json:"storageSize"`
	AutoRollbackOnFailure *bool  `json:"autoRollbackOnFailure"`
}

// SharedGrafanaRequest is the camelCase body for the shared Grafana family.
// ingressHost overrides grafana.<ServerURL host>; never values.ingress.host.
type SharedGrafanaRequest struct {
	ManagementClusterID   string `json:"managementClusterId"`
	Namespace             string `json:"namespace"`
	ReleaseName           string `json:"releaseName"`
	ChartVersion          string `json:"chartVersion"`
	Replicas              int32  `json:"replicas"`
	StorageClass          string `json:"storageClass"`
	StorageSize           string `json:"storageSize"`
	IngressHost           string `json:"ingressHost"`
	LogDatasourceURL      string `json:"logDatasourceUrl"`
	AutoRollbackOnFailure *bool  `json:"autoRollbackOnFailure"`
}

// SharedLokiRequest is the camelCase body for the shared Loki family.
// ingestHostname is required and never derived from the Astronomer ingress host.
type SharedLokiRequest struct {
	ManagementClusterID     string `json:"managementClusterId"`
	Namespace               string `json:"namespace"`
	ReleaseName             string `json:"releaseName"`
	ChartVersion            string `json:"chartVersion"`
	StorageConfigID         string `json:"storageConfigId"`
	ObjectStorageSecretName string `json:"objectStorageSecretName"`
	IngestHostname          string `json:"ingestHostname"`
	StorageClass            string `json:"storageClass"`
	WalStorageSize          string `json:"walStorageSize"`
	Mode                    string `json:"mode"`
	Retention               string `json:"retention"`
	SkipDiskCheck           *bool  `json:"skipDiskCheck"`
	AutoRollbackOnFailure   *bool  `json:"autoRollbackOnFailure"`
}

type objectStoreSecretSpec struct {
	Name            string
	Key             string
	Content         string
	StorageConfigID string
}

type releaseRef struct {
	Namespace   string
	ReleaseName string
}

type monitoringOperationEnvelope struct {
	ClusterID                string                 `json:"clusterId,omitempty"`
	Request                  json.RawMessage        `json:"request,omitempty"`
	Values                   map[string]any         `json:"values,omitempty"`
	SecretSpec               *objectStoreSecretSpec `json:"secretSpec,omitempty"`
	ResolvedAutoRollback     bool                   `json:"resolvedAutoRollback"`
	ResolvedMaxRetryAttempts int32                  `json:"resolvedMaxRetryAttempts"`
}

func (h *MonitoringHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *MonitoringHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *MonitoringHandler) GetBackendConfig(w http.ResponseWriter, r *http.Request) {
	// The response embeds the backend's decoded authConfig (operator-supplied
	// backend auth material), so this read carries the same monitoring gate as
	// its mutating sibling — it was previously reachable unauthenticated.
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{})
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondJSON(w, http.StatusOK, map[string]any{})
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring backend")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
}

// readAuthConfig resolves a backend's auth_config for a RESPONSE.
//
// Unlike the write paths, a decrypt failure here is not fatal: the response
// carries no credential either way, and the operationPolicies block and the
// URLs are exactly what an operator needs while diagnosing a key problem. It
// falls back to the stored projection run through the at-rest split, so the
// answer is non-secret by construction even if a mid-rollout write by an old
// binary left something in the JSONB. The credential key names go missing,
// which is honest — we genuinely cannot tell what the envelope holds.
func (h *MonitoringHandler) readAuthConfig(backend sqlc.MonitoringBackend) map[string]any {
	authConfig, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		h.log.Error("decrypt monitoring backend credential for read", "error", err)
		return imonitoring.StripAuthConfigSecrets(imonitoring.DecodeAuthConfig(backend.AuthConfig))
	}
	return authConfig
}

func (h *MonitoringHandler) UpdateBackendConfig(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	var req UpdateMonitoringBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// RMW site (migration 146). This used to be a pure writer: whatever the
	// client sent as authConfig became the whole stored document. That stopped
	// being viable the moment reads started answering with the non-secret
	// projection — the UI's own GET → edit → PUT round-trip would post an
	// authConfig with no credential in it, and the credential would be gone.
	// The same replace-everything behaviour also silently discarded the
	// shared-Thanos / shared-Alertmanager deployment metadata that lives in
	// this column, on every backend edit.
	//
	// So: the stored document is the base. An ABSENT authConfig means "leave
	// the credential alone"; a PRESENT one is authoritative for the credential
	// and merges over the config-bag keys, which the client never sends.
	existing, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		// First save. There is nothing to preserve.
		existing = sqlc.MonitoringBackend{}
	default:
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring backend")
		return
	}
	storedCfg, err := resolveMonitoringBackendAuthConfig(existing, h.monitoringDecryptor())
	if err != nil {
		// Fail the write rather than merge against a document we could not
		// read: an operator editing a timeout would otherwise have their
		// credential deleted as a side effect.
		h.log.Error("decrypt monitoring backend credential for update", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError,
			"Failed to read the existing monitoring credential; check the platform encryption key")
		return
	}
	authConfigMap := storedCfg
	if req.AuthConfig != nil {
		// The client is authoritative about the credential portion; the
		// config-bag keys it does not know about are carried forward.
		merged := imonitoring.StripAuthConfigSecrets(storedCfg)
		for key, value := range decodeJSONMap(req.AuthConfig) {
			merged[key] = value
		}
		authConfigMap = merged
	}
	policies := mapFromMapValue(authConfigMap["operationPolicies"])
	if req.DefaultAutoRollbackOnFailure != nil {
		policies["defaultAutoRollbackOnFailure"] = *req.DefaultAutoRollbackOnFailure
	}
	if req.MaxRetryAttempts > 0 {
		policies["maxRetryAttempts"] = req.MaxRetryAttempts
	} else if _, ok := policies["maxRetryAttempts"]; !ok {
		policies["maxRetryAttempts"] = int32(1)
	}
	authConfigMap["operationPolicies"] = policies
	if req.BackendType == "" {
		req.BackendType = "thanos"
	}
	if req.QueryURL == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "queryUrl is required")
		return
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        req.BackendType,
		QueryUrl:           req.QueryURL,
		AlertmanagerUrl:    req.AlertmanagerURL,
		TenantID:           req.TenantID,
		AuthType:           req.AuthType,
		DefaultStepSeconds: req.DefaultStepSeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		CreatedByID:        currentUserUUID(r),
	}
	if err := imonitoring.SealInto(&params, authConfigMap, h.monitoringSealer()); err != nil {
		h.log.Error("encrypt monitoring backend credential", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to secure the monitoring credential")
		return
	}
	backend, err := h.queries.UpsertDefaultMonitoringBackend(r.Context(), params)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to save monitoring backend")
		return
	}
	// UpdateBackendConfig is the upsert behind both CreateEndpoint and
	// UpdateEndpoint; we record the action as "update" — when the row didn't
	// exist before this is effectively a "create", but distinguishing the two
	// would require a pre-read and isn't worth the extra round-trip.
	recordAudit(r, h.queries, "monitoring.endpoint.update", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"query_url":        backend.QueryUrl,
		"alertmanager_url": backend.AlertmanagerUrl,
		"tenant_id":        backend.TenantID,
		"auth_type":        backend.AuthType,
	})
	// The response renders the document this request just produced rather than
	// a re-read of the row — the stored JSONB is now the stripped projection,
	// so a re-read would report `authConfigKeys: []` and make a successful save
	// look like a dropped credential — but it is REDACTED exactly like the
	// read. It has to be: since this handler became a read-modify-write, the
	// document it renders is the merge of the caller's input over the STORED
	// one, so echoing it unredacted would hand a caller who omitted authConfig
	// (or who supplied only some of its keys) the credential they never held.
	// monitoring:update must not become a way to read the credential.
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, authConfigMap))
}

// monitoringBackendResponse renders a backend row from its RESOLVED
// auth_config document. authConfig is operator-supplied backend auth material
// (bearer tokens, basic-auth passwords, custom headers) and monitoring:read is
// a low-privilege verb — the shipped troubleshooter/viewer templates all carry
// it — so responses get the non-secret projection only: the operationPolicies
// block, plus the key names so an operator can still see that credentials are
// configured.
//
// There is deliberately no write-response variant. There used to be one, on
// the argument that the monitoring:update caller had just supplied the
// authConfig in the same request and so was being told nothing it did not
// already hold. Migration 146 falsified that argument: UpdateBackendConfig is
// now a read-modify-write, so the document it renders is the caller's input
// MERGED OVER THE STORED ONE, and a request that omitted authConfig renders
// the stored credential. One redaction, applied to every response, is the only
// version of this that cannot drift back into a disclosure.
func monitoringBackendResponse(backend sqlc.MonitoringBackend, authConfig map[string]any) map[string]any {
	return monitoringBackendPayload(backend, authConfig)
}

// resolveMonitoringBackendAuthConfig returns the COMPLETE stored auth_config
// document for a backend row (migration 146).
//
// Every read-modify-write on this column starts here and every one of them
// must treat the error as fatal TO THE WRITE — and, in the unattended worker,
// to the write only: internal/worker/tasks/monitoring_reconcile.go logs and
// skips its status stamp rather than failing the tick, because per-cluster
// reconciliation needs no monitoring credential and must not be frozen by one.
// The column is a mixed credential/config bag: four separate paths mutate a
// non-secret key in it
// (shared-Thanos metadata, shared-Alertmanager metadata, shared-alerting asset
// hashes, and the reconcile status stamp in the worker), and any one of them
// that re-marshalled the stored JSONB projection instead of the resolved
// document would persist "this backend has no credential". The operator would
// see monitoring stop authenticating after an unrelated policy edit, with
// nothing in the audit trail pointing at the cause.
func resolveMonitoringBackendAuthConfig(backend sqlc.MonitoringBackend, dec imonitoring.Decryptor) (map[string]any, error) {
	full, err := imonitoring.ResolveAuthConfig(backend.AuthConfigEncrypted, backend.AuthConfig, dec)
	if err != nil {
		return nil, err
	}
	return imonitoring.DecodeAuthConfig(full), nil
}

// monitoringBackendPayload renders a backend row from its RESOLVED auth_config
// document.
//
// It takes the resolved document rather than the row so the ciphertext column
// has no path into a response, and so `authConfigKeys` keeps meaning "which
// credential keys are configured" after migration 146 sealed them out of the
// JSONB. Deriving that list from the stored projection instead would report an
// empty list for every sealed backend — an operator with monitoring:read would
// be told no credential is configured when one is.
func monitoringBackendPayload(backend sqlc.MonitoringBackend, authConfig map[string]any) map[string]any {
	return map[string]any{
		"id":                 backend.ID.String(),
		"name":               backend.Name,
		"backendType":        backend.BackendType,
		"queryUrl":           backend.QueryUrl,
		"alertmanagerUrl":    backend.AlertmanagerUrl,
		"tenantId":           backend.TenantID,
		"authType":           backend.AuthType,
		"authConfig":         redactedMonitoringAuthConfig(authConfig),
		"authConfigKeys":     monitoringAuthConfigKeys(authConfig),
		"operationPolicies":  mapFromMapValue(authConfig["operationPolicies"]),
		"defaultStepSeconds": backend.DefaultStepSeconds,
		"timeoutSeconds":     backend.TimeoutSeconds,
		"isDefault":          backend.IsDefault,
	}
}

// redactedMonitoringAuthConfig keeps only the keys that are definitionally not
// credentials. operationPolicies is retry/rollback policy the UI reads back;
// everything else in authConfig is treated as secret, because the shape is
// operator-authored and an allow-list is the only safe direction.
//
// This list is deliberately NARROWER than imonitoring.NonSecretAuthConfigKeys
// (the at-rest split). Being narrower is always safe — a key that stays in the
// clear on disk but is withheld from a monitoring:read response leaks nothing
// — whereas being wider would render something the envelope had sealed. The
// shared-stack metadata is served by its own status endpoints rather than
// smuggled through here. TestRedactedMonitoringAuthConfigIsSubsetOfNonSecret
// pins the direction.
func redactedMonitoringAuthConfig(authConfig map[string]any) map[string]any {
	out := map[string]any{}
	if _, ok := authConfig["operationPolicies"]; ok {
		out["operationPolicies"] = mapFromMapValue(authConfig["operationPolicies"])
	}
	return out
}

// monitoringAuthConfigKeys lists the authConfig key names (never values) so a
// read-only operator can tell whether auth material is configured without
// receiving it — the same "configured, not disclosed" shape the SIEM forwarder
// surface uses.
//
// Since migration 146 it means exactly "the keys the envelope holds": the
// caller passes the RESOLVED document and the non-secret config-bag keys are
// filtered out by the same allow-list the at-rest split uses, so an operator
// is not told that `sharedThanos` is auth material.
func monitoringAuthConfigKeys(authConfig map[string]any) []string {
	return imonitoring.AuthConfigSecretKeyNames(authConfig)
}

func (h *MonitoringHandler) applySharedThanosStack(ctx context.Context, msgType protocol.MessageType, req SharedThanosStackRequest, secretSpec objectStoreSecretSpec, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if err := h.ensureObjectStoreSecret(ctx, req.ManagementClusterID, req.Namespace, secretSpec); err != nil {
		return nil, err
	}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "thanos",
		RepoURL:     "https://stevehipwell.github.io/helm-charts/",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedAlertmanager(ctx context.Context, msgType protocol.MessageType, req SharedAlertmanagerRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "alertmanager",
		RepoURL:     "https://prometheus-community.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedLokiStack(ctx context.Context, msgType protocol.MessageType, req SharedLokiRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if values == nil {
		values = map[string]any{}
	}
	values["extraObjects"] = lokiFamilyExtraObjects(req, h.proxyImage)
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   sharedLokiChartName,
		RepoURL:     sharedLokiChartRepo,
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedGrafanaStack(ctx context.Context, msgType protocol.MessageType, req SharedGrafanaRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	// Re-render sidecar ConfigMaps from live backend metadata so a Thanos
	// family that became healthy after enqueue is included without a second
	// form submit.
	if values == nil {
		values = map[string]any{}
	}
	if h.queries != nil {
		if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
			values["extraObjects"] = grafanaFamilyExtraObjects(req, backend, h.proxyImage, h.serverURL, h.grafanaExpose)
		}
	}
	values["extraConfigmapMounts"] = []any{grafanaClusterFolderProvidersMount()}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "grafana",
		RepoURL:     "https://grafana.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func sanitizeMonitoringValues(values map[string]any) map[string]any {
	raw, err := json.Marshal(values)
	if err != nil {
		return map[string]any{}
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return map[string]any{}
	}
	redactSensitiveMap(cloned)
	return cloned
}

func redactSensitiveMap(data map[string]any) {
	for key, value := range data {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "access_key") || strings.Contains(lower, "accesskey") || strings.Contains(lower, "secret_key") || strings.Contains(lower, "objstoreconfig") {
			data[key] = "***redacted***"
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			redactSensitiveMap(typed)
		case []any:
			for _, item := range typed {
				if m, ok := item.(map[string]any); ok {
					redactSensitiveMap(m)
				}
			}
		}
	}
}

// storageConfigAuthorizer approves — or refuses — a caller's reference to one
// backup_storage_configs row. objectStoreSecretSpec calls it after loading the
// row and BEFORE reading anything out of it.
//
// It exists because storageConfigId arrives in the REQUEST BODY and names a
// global object: GetBackupStorageConfigByID takes an id and nothing else, and
// the row it returns carries live S3 access/secret keys that this file writes
// verbatim into a Secret in a namespace of the caller's cluster. The backups
// API gates the same rows on rbac.ResourceBackups and never surfaces their raw
// keys at all (backups.go storageResponse). While the per-cluster monitoring
// routes were evaluated as a GLOBAL check, "the caller reached this code" was
// itself proof of a fleet-wide grant; now that a single-cluster tenant can
// reach them, the reference needs its own authorization.
type storageConfigAuthorizer func(sqlc.BackupStorageConfig) error

// errStorageConfigForbidden / errStorageConfigAuthzFailed are the two outcomes
// a storageConfigAuthorizer can produce that must NOT be reported as a bad
// request body. respondStackPayloadError maps them to 403 / 500.
var (
	errStorageConfigForbidden   = errors.New("you do not have permission to use this object storage configuration")
	errStorageConfigAuthzFailed = errors.New("failed to retrieve user permissions")
)

// clusterStorageConfigAuthorizer is the guard for a PER-CLUSTER stack install
// on clusterID. A reference is allowed when any of the following holds:
//
//  1. the config belongs to the routed cluster (cluster_id == clusterID) — it
//     is that cluster's own object and the caller already holds a monitoring
//     write on it to be here at all;
//  2. the caller holds backups:read at the config's OWN scope — the same rule
//     authorizeBackup applies to every other read of that row;
//  3. the caller holds a fleet-wide monitoring grant at routeVerb — i.e. they
//     could have reached this exact handler back when its gate was a global
//     check. This clause is pre-fix parity and nothing more, which is why it
//     takes the ROUTE's verb rather than any monitoring verb: a Monitoring
//     Admin installing against the fleet's default (unscoped) storage config
//     must keep working, but a global monitoring:read holder must not gain the
//     install path they never had.
//
// Anything else — most importantly a single-cluster tenant naming a global or
// a neighbouring tenant's config — is refused.
func (h *MonitoringHandler) clusterStorageConfigAuthorizer(ctx context.Context, clusterID uuid.UUID, routeVerb rbac.Verb) storageConfigAuthorizer {
	return func(cfg sqlc.BackupStorageConfig) error {
		bindings, restricted, err := h.authz.bindingsForContext(ctx)
		if err != nil {
			return errStorageConfigAuthzFailed
		}
		if !restricted {
			return nil
		}
		if cfg.ClusterID.Valid {
			if uuid.UUID(cfg.ClusterID.Bytes) == clusterID {
				return nil
			}
			if h.authz.allowsCluster(bindings, uuid.UUID(cfg.ClusterID.Bytes), rbac.ResourceBackups, rbac.VerbRead) {
				return nil
			}
		} else if h.authz.allowsGlobal(bindings, rbac.ResourceBackups, rbac.VerbRead) {
			return nil
		}
		if h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, routeVerb) {
			return nil
		}
		return errStorageConfigForbidden
	}
}

// respondStackPayloadError maps a monitoringStackPayload failure to a status.
// Everything it returns is a complaint about the request body EXCEPT the two
// storage-config authorization outcomes: answering those 400 would tell the
// caller their body was malformed and invite them to retry it.
func respondStackPayloadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errStorageConfigForbidden):
		// The canonical wording every other permission denial uses, not the
		// sentinel's text: the response must not say which of the authorizer's
		// clauses the caller failed, or whether the id they guessed resolved to
		// a row at all.
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
	case errors.Is(err, errStorageConfigAuthzFailed):
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
	default:
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
	}
}

func (h *MonitoringHandler) objectStoreSecretSpec(ctx context.Context, storageConfigID, overrideName, defaultName string, authorize storageConfigAuthorizer) (objectStoreSecretSpec, error) {
	if h.queries == nil {
		return objectStoreSecretSpec{}, fmt.Errorf("monitoring store not configured")
	}
	storageID, err := uuid.Parse(storageConfigID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("invalid storageConfigId")
	}
	storageCfg, err := h.queries.GetBackupStorageConfigByID(ctx, storageID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("backup storage config not found")
	}
	// Before any field of the row is read, including into an error message.
	if authorize != nil {
		if err := authorize(storageCfg); err != nil {
			return objectStoreSecretSpec{}, err
		}
	}
	if storageCfg.Bucket == "" {
		return objectStoreSecretSpec{}, fmt.Errorf("storage config bucket is required")
	}
	content, err := h.buildObjstoreConfigYAML(storageCfg)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("failed to build object storage config: %w", err)
	}
	name := defaultString(overrideName, defaultName)
	return objectStoreSecretSpec{
		Name:            name,
		Key:             "objstore.yml",
		Content:         content,
		StorageConfigID: storageConfigID,
	}, nil
}

// storageCredentials returns the S3 access/secret pair for a backup storage
// config.
//
// It is not simply cfg.AccessKey/cfg.SecretKey: since the credential-at-rest
// change those columns are deliberately BLANKED whenever the encrypted column
// is populated (legacyBackupCredentialColumns in backups.go), so reading them
// directly yields "" on every deployment that has an encryptor — and the stack
// was then installed with an empty-credential objstore secret and no error
// anywhere. Fail loudly instead when the row is sealed and we cannot open it.
func (h *MonitoringHandler) storageCredentials(cfg sqlc.BackupStorageConfig) (string, string, error) {
	if cfg.EncryptedCredentials == "" {
		return cfg.AccessKey, cfg.SecretKey, nil
	}
	if h == nil || h.encryptor == nil {
		return "", "", fmt.Errorf("object storage credentials are encrypted but no encryption key is configured")
	}
	plaintext, err := h.encryptor.Decrypt(cfg.EncryptedCredentials)
	if err != nil {
		return "", "", fmt.Errorf("failed to decrypt object storage credentials")
	}
	var creds struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal([]byte(plaintext), &creds); err != nil {
		return "", "", fmt.Errorf("object storage credentials are malformed")
	}
	return creds.AccessKey, creds.SecretKey, nil
}

func (h *MonitoringHandler) buildObjstoreConfigYAML(storageCfg sqlc.BackupStorageConfig) (string, error) {
	accessKey, secretKey, err := h.storageCredentials(storageCfg)
	if err != nil {
		return "", err
	}
	objstoreConfig := map[string]any{
		"type": "S3",
		"config": map[string]any{
			"bucket":     storageCfg.Bucket,
			"endpoint":   storageCfg.EndpointUrl,
			"region":     storageCfg.Region,
			"access_key": accessKey,
			"secret_key": secretKey,
		},
	}
	if storageCfg.Prefix != "" {
		objstoreConfig["prefix"] = storageCfg.Prefix
	}
	if storageCfg.EndpointUrl == "" {
		objstoreConfig["config"].(map[string]any)["endpoint"] = "s3.amazonaws.com"
	}
	raw, err := yaml.Marshal(objstoreConfig)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *MonitoringHandler) ensureObjectStoreSecret(ctx context.Context, clusterID, namespace string, spec objectStoreSecretSpec) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	if err := h.ensureNamespace(ctx, clusterID, namespace); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "monitoring",
			},
		},
		"type": "Opaque",
		"stringData": map[string]string{
			spec.Key: spec.Content,
		},
	})
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, spec.Name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) ensureNamespace(ctx context.Context, clusterID, namespace string) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s", namespace)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
	})
	if err != nil {
		return err
	}
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) applyMonitoringStack(ctx context.Context, clusterID string, msgType protocol.MessageType, req MonitoringStackRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if req.StorageConfigID != "" {
		// nil authorizer, deliberately: this runs in the operation executor,
		// which has no HTTP caller and no bindings to check. The reference was
		// authorized when the operation was ENQUEUED — monitoringStackPayload
		// runs clusterStorageConfigAuthorizer against the same
		// req.StorageConfigID before enqueueClusterStackOperation persists it
		// into the envelope this re-reads. Adding a check here would evaluate
		// an empty binding set and fail every install.
		secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-thanos-objstore", nil)
		if err != nil {
			return nil, err
		}
		if err := h.ensureObjectStoreSecret(ctx, clusterID, req.Namespace, secretSpec); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "kube-prometheus-stack",
		RepoURL:     "https://prometheus-community.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     900,
	})
}

func scrapeIntervalSeconds(raw string) int32 {
	if raw == "" {
		return 30
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 30
	}
	return int32(d.Seconds())
}

func defaultInt32(v, fallback int32) int32 {
	if v <= 0 {
		return fallback
	}
	return v
}

func nullableNow(ok bool) pgtype.Timestamptz {
	if !ok {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
}

func specHash(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mapFromMapValue(v any) map[string]any {
	out, _ := v.(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func (h *MonitoringHandler) observeRelease(ctx context.Context, clusterID string, ref releaseRef) (map[string]any, bool, []string) {
	if h.helm == nil || clusterID == "" || ref.ReleaseName == "" || ref.Namespace == "" {
		return nil, false, nil
	}
	result, err := h.helm.Status(ctx, clusterID, ref.ReleaseName, ref.Namespace)
	observed := map[string]any{
		"clusterId":   clusterID,
		"namespace":   ref.Namespace,
		"releaseName": ref.ReleaseName,
		"observedAt":  time.Now().UTC().Format(time.RFC3339),
	}
	if err != nil {
		observed["status"] = "missing"
		observed["error"] = err.Error()
		return observed, true, []string{"helm release not found or not healthy"}
	}
	observed["status"] = result.Status
	observed["revision"] = result.Revision
	return observed, false, nil
}

func boolPtrValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func nullablePgTime(ts pgtype.Timestamptz) any {
	if !ts.Valid {
		return nil
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func (h *MonitoringHandler) UnmarshalBody(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// --- Monitoring endpoints CRUD (Python: /api/v1/monitoring/endpoints/) ---
//
// We back this on the existing `monitoring_backends` table since the Python
// `PrometheusEndpoint` model maps to the same configuration concept.

// ListEndpoints handles GET /api/v1/monitoring/endpoints/.
func (h *MonitoringHandler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil && err != pgx.ErrNoRows {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load monitoring endpoints")
		return
	}
	items := []map[string]any{}
	if err == nil && backend.ID != uuid.Nil {
		items = append(items, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
	}
	RespondPaginated(w, r, items, int64(len(items)))
}

// GetEndpoint handles GET /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) GetEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	idStr := chi.URLParam(r, "id")
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring endpoint not found")
		return
	}
	if backend.ID.String() != idStr {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring endpoint not found")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
}

// CreateEndpoint handles POST /api/v1/monitoring/endpoints/.
// Currently maps to UpsertDefaultMonitoringBackend.
func (h *MonitoringHandler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// UpdateEndpoint handles PUT /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// DeleteEndpoint handles DELETE /api/v1/monitoring/endpoints/{id}/.
// We do not currently support deleting the default backend; return 501.
func (h *MonitoringHandler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	RespondRequestError(w, r, http.StatusNotImplemented, apierror.NotImplemented, "Deleting the default monitoring backend is not yet supported")
}
