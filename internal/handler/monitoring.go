package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type MonitoringHandler struct {
	requester K8sRequester
	queries   MonitoringQuerier
	runTx     monitoringRunTxFunc
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

type MonitoringMutationTx interface {
	audit.OutboxQuerier
	GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(context.Context, sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
	UpsertClusterMonitoringConfig(context.Context, sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error)
	CreateMonitoringOperation(context.Context, sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error)
	CreateMonitoringOperationIdempotent(context.Context, sqlc.CreateMonitoringOperationIdempotentParams) (sqlc.MonitoringOperation, error)
	RequeueMonitoringOperation(context.Context, uuid.UUID) (sqlc.MonitoringOperation, error)
}

type monitoringRunTxFunc func(context.Context, func(MonitoringMutationTx) error) error

func (h *MonitoringHandler) SetRunTx(runTx monitoringRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *MonitoringHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func respondMonitoringMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackStatus int, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errMonitoringOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a different monitoring operation")
		return
	}
	respondTransactionalMutationError(w, r, err, fallbackStatus, fallbackCode, fallbackMessage)
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

type monitoringBackendDeleter interface {
	DeleteDefaultMonitoringBackendIfUnused(context.Context, uuid.UUID) (sqlc.MonitoringBackend, error)
}

var errMonitoringBackendDeleteUnsupported = errors.New("monitoring backend deletion is not configured")

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

// openapi:request UpdateMonitoringBackendRequest
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

// openapi:request UpdateClusterMonitoringConfigRequest
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

// openapi:request MonitoringStackRequest
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

// openapi:request SharedThanosStackRequest
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

// openapi:request SharedAlertmanagerStackRequest
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

// openapi:request SharedGrafanaStackRequest
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

// openapi:request SharedLokiStackRequest
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

func (h *MonitoringHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, querier)
}
