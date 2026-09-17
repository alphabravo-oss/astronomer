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
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type ToolQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterToolByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTool, error)
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	ListClusterTools(ctx context.Context, arg sqlc.ListClusterToolsParams) ([]sqlc.ClusterTool, error)
	ListEnabledTools(ctx context.Context) ([]sqlc.ClusterTool, error)
	CountClusterTools(ctx context.Context) (int64, error)
	CountInstalledCharts(ctx context.Context) (int64, error)
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	GetInstalledChartByRelease(ctx context.Context, arg sqlc.GetInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	// Indexed duplicate-install guard (cluster_id, tool_slug).
	GetInstalledChartByClusterAndTool(ctx context.Context, arg sqlc.GetInstalledChartByClusterAndToolParams) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	UpdateInstalledChartStatus(ctx context.Context, arg sqlc.UpdateInstalledChartStatusParams) error
	AdoptInstalledChartByRelease(ctx context.Context, arg sqlc.AdoptInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	UpdateInstalledChartValues(ctx context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
	DeleteInstalledChart(ctx context.Context, id uuid.UUID) error
	CreateToolOperation(ctx context.Context, arg sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error)
	GetToolOperation(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	ListToolOperations(ctx context.Context, arg sqlc.ListToolOperationsParams) ([]sqlc.ToolOperation, error)
	ListPendingToolOperations(ctx context.Context, limit int32) ([]sqlc.ToolOperation, error)
	GetLatestToolOperationForTarget(ctx context.Context, arg sqlc.GetLatestToolOperationForTargetParams) (sqlc.ToolOperation, error)
	MarkToolOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	MarkToolOperationSuperseded(ctx context.Context, arg sqlc.MarkToolOperationSupersededParams) (sqlc.ToolOperation, error)
	RequeueToolOperation(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	CreateToolOperationEvent(ctx context.Context, arg sqlc.CreateToolOperationEventParams) (sqlc.ToolOperationEvent, error)
	ListToolOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.ToolOperationEvent, error)
	RenewToolOperationLease(context.Context, sqlc.RenewToolOperationLeaseParams) (sqlc.ToolOperation, error)
	CheckpointToolOperation(context.Context, sqlc.CheckpointToolOperationParams) (sqlc.CheckpointToolOperationRow, error)
	FinishToolOperation(context.Context, sqlc.FinishToolOperationParams) (sqlc.ToolOperation, error)
}

type toolOperationPager interface {
	CountToolOperations(ctx context.Context, arg sqlc.CountToolOperationsParams) (int64, error)
	ListToolOperationsForScopes(ctx context.Context, arg sqlc.ListToolOperationsForScopesParams) ([]sqlc.ToolOperation, error)
	CountToolOperationsForScopes(ctx context.Context, arg sqlc.CountToolOperationsForScopesParams) (int64, error)
}

// ToolMutationTx commits the durable controller operation and mandatory audit
// intent together. Tool desired state is materialized by the reconciler only
// after this transaction commits.
type ToolMutationTx interface {
	audit.OutboxQuerier
	CreateToolOperation(context.Context, sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error)
	CreateToolOperationIdempotent(context.Context, sqlc.CreateToolOperationIdempotentParams) (sqlc.ToolOperation, error)
	MarkToolOperationRunning(context.Context, uuid.UUID) (sqlc.ToolOperation, error)
	RequeueToolOperation(context.Context, uuid.UUID) (sqlc.ToolOperation, error)
}

type toolRunTxFunc func(context.Context, func(ToolMutationTx) error) error

type ToolHandler struct {
	queries ToolQuerier
	runTx   toolRunTxFunc
	helm    HelmRequester
	log     *slog.Logger
	authz   authorizationSupport
	bus     *events.Bus
	mu      sync.Mutex
	trigger chan struct{}
	// helmConcurrency caps the number of executeOperation goroutines
	// dispatched per reconciler tick.
	helmConcurrency int
	// maintenanceGate is the migration-057 hook on tool.{install,
	// upgrade,uninstall}. Optional + nil-safe; see clusters.SetMaintenanceGate.
	maintenanceGate *MaintenanceGate
	// vaultResolver substitutes ${vault://...} markers in the values
	// YAML right before the tool install / upgrade task is enqueued.
	// Migration 067. Nil-safe — see vaultResolveBlob in vault_hook.go.
	vaultResolver *avault.Resolver
}

func (h *ToolHandler) SetRunTx(runTx toolRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ToolHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetVaultResolver wires the Vault resolver used to substitute
// ${vault://...} markers in tool preset values at install time.
func (h *ToolHandler) SetVaultResolver(r *avault.Resolver) {
	if h == nil {
		return
	}
	h.vaultResolver = r
}

func NewToolHandler(queries ToolQuerier) *ToolHandler {
	return &ToolHandler{
		queries: queries,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func NewToolHandlerWithHelm(queries ToolQuerier, helm HelmRequester) *ToolHandler {
	return &ToolHandler{
		queries: queries,
		helm:    helm,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers tool.{install,upgrade,uninstall} during an active maintenance
// window. Optional + nil-safe.
func (h *ToolHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

type ToolResponse struct {
	ID                string          `json:"id"`
	Slug              string          `json:"slug"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Icon              string          `json:"icon"`
	Category          string          `json:"category"`
	Charts            json.RawMessage `json:"charts"`
	VersionConstraint string          `json:"version_constraint"`
	DefaultNamespace  string          `json:"default_namespace"`
	IsBuiltin         bool            `json:"is_builtin"`
	IsEnabled         bool            `json:"is_enabled"`
	HelmChartID       *string         `json:"helm_chart_id"`
	Presets           json.RawMessage `json:"presets"`
	ServiceName       string          `json:"service_name"`
	ServicePort       *int32          `json:"service_port"`
	ServicePath       string          `json:"service_path"`
	SubServices       json.RawMessage `json:"sub_services"`
	// FormSchema drives the install-time settings form (nil → raw-YAML only).
	FormSchema *ToolFormSchema `json:"form_schema,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func toolToResponse(t sqlc.ClusterTool) ToolResponse {
	resp := ToolResponse{
		ID:                t.ID.String(),
		Slug:              t.Slug,
		Name:              t.Name,
		Description:       t.Description,
		Icon:              t.Icon,
		Category:          t.Category,
		Charts:            t.Charts,
		VersionConstraint: t.VersionConstraint,
		DefaultNamespace:  t.DefaultNamespace,
		IsBuiltin:         t.IsBuiltin,
		IsEnabled:         t.IsEnabled,
		Presets:           t.Presets,
		ServiceName:       t.ServiceName,
		ServicePath:       t.ServicePath,
		SubServices:       t.SubServices,
		FormSchema:        toolFormSchemaFor(t.Slug),
		CreatedAt:         t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:         t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if t.HelmChartID.Valid {
		s := uuid.UUID(t.HelmChartID.Bytes).String()
		resp.HelmChartID = &s
	}
	if t.ServicePort.Valid {
		resp.ServicePort = &t.ServicePort.Int32
	}
	return resp
}

type toolChart struct {
	ChartName   string `json:"chart_name"`
	RepoURL     string `json:"repo_url"`
	Namespace   string `json:"namespace"`
	Order       int    `json:"order"`
	ReleaseName string `json:"release_name,omitempty"`
	Version     string `json:"version,omitempty"`
	ValuesKey   string `json:"values_key,omitempty"`
}

// openapi:request ToolActionRequest
type toolActionRequest struct {
	ClusterID      string `json:"cluster_id"`
	Preset         string `json:"preset"`
	ValuesOverride string `json:"values_override"`
	ReleaseName    string `json:"release_name"`
}

// openapi:request ToolUninstallRequest
type toolUninstallRequest struct {
	ClusterID string `json:"cluster_id"`
}

type toolOperationEnvelope struct {
	ClusterID string        `json:"clusterId"`
	ToolSlug  string        `json:"toolSlug"`
	Preset    string        `json:"preset,omitempty"`
	Releases  []toolRelease `json:"releases"`
}

// toolReleaseExecution combines immutable scope with one release for the Helm
// adapter. Durable operations always encode the ordered Releases plan.
type toolReleaseExecution struct {
	Description string
	ClusterID   string
	ToolSlug    string
	ReleaseName string
	Namespace   string
	Preset      string
	ValuesYAML  string
	ChartName   string
	RepoURL     string
	Version     string
}

func (h *ToolHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *ToolHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *ToolHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.RunReconciler(ctx)
}

func (h *ToolHandler) RunReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	h.runReconciler(ctx)
}

func (h *ToolHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

// EnsureInstalled synchronously installs or adopts a tool release on the given
// cluster. It is intended for platform-owned bootstrap flows, where waiting
// for the async operation queue would only add startup lag and complexity.
func (h *ToolHandler) EnsureInstalled(ctx context.Context, clusterID uuid.UUID, slug, releaseName, preset, valuesYAML string) (sqlc.InstalledChart, error) {
	if h == nil || h.queries == nil {
		return sqlc.InstalledChart{}, errors.New("tool handler not configured")
	}
	if slug == DexToolSlug {
		return sqlc.InstalledChart{}, errors.New("Dex is bundled with the Astronomer management chart and cannot be installed from the remote tools catalog")
	}
	tool, err := h.queries.GetToolBySlug(ctx, slug)
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	valuesYAML = mergeValueLayers(presetValuesYAML(tool.Presets, preset), valuesYAML)
	if cluster, err := h.queries.GetClusterByID(ctx, clusterID); err == nil {
		valuesYAML = mergeValueLayers(distributionInstallValues(slug, cluster.Distribution), valuesYAML)
	}
	plan, err := buildToolReleasePlan(tool, releaseName, valuesYAML)
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	env := toolOperationEnvelope{ClusterID: clusterID.String(), ToolSlug: slug, Preset: preset, Releases: plan}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/tools/"+slug+"/install/", nil)
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	// Claim inside the audited creation transaction. A background reconciler
	// cannot steal this synchronous bootstrap operation between enqueue/claim.
	op, err := executeMutation(r, h.runTx, func(q ToolMutationTx) (sqlc.ToolOperation, error) {
		created, err := createToolOperation(ctx, q, "tool_installation", operationTargetKey(clusterID, slug), "install", env, pgtype.UUID{})
		if err != nil {
			return sqlc.ToolOperation{}, err
		}
		return q.MarkToolOperationRunning(ctx, created.ID)
	}, func(op sqlc.ToolOperation) mutationAuditEvent {
		return mutationAuditEvent{action: "tool.install", resourceType: "tool", resourceID: tool.ID.String(), resourceName: slug, status: http.StatusAccepted, detail: map[string]any{"operation_id": op.ID.String(), "cluster_id": clusterID.String(), "source": "platform_bootstrap", "releases": toolPlanAudit(env)}}
	})
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	err = h.executeOperation(ctx, op)
	h.finishToolOperation(ctx, op, err)
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	return h.findInstalledTool(ctx, clusterID, slug)
}

func (h *ToolHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}
