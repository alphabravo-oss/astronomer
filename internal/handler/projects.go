package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/projectquota"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// projectScopeQuerier is the OPTIONAL scope-filtered list capability, the
// project twin of clusterScopeQuerier. See that type for why it is not folded
// into ProjectQuerier.
type projectScopeQuerier interface {
	ListProjectsForScopes(ctx context.Context, arg sqlc.ListProjectsForScopesParams) ([]sqlc.Project, error)
	CountProjectsForScopes(ctx context.Context, arg sqlc.CountProjectsForScopesParams) (int64, error)
}

// ProjectQuerier abstracts project-related database queries. Phase B3 added
// the project_namespaces sidecar (per-namespace reconcile state); the
// methods below stay backward-compatible — the handler still works with the
// pre-B3 query set in tests that don't exercise enforcement.
type ProjectQuerier interface {
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	ListProjects(ctx context.Context, arg sqlc.ListProjectsParams) ([]sqlc.Project, error)
	ListProjectsByCluster(ctx context.Context, arg sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error)
	CreateProject(ctx context.Context, arg sqlc.CreateProjectParams) (sqlc.Project, error)
	UpdateProject(ctx context.Context, arg sqlc.UpdateProjectParams) (sqlc.Project, error)
	UpdateProjectPolicy(ctx context.Context, arg sqlc.UpdateProjectPolicyParams) (sqlc.Project, error)
	DeleteProject(ctx context.Context, id uuid.UUID) error
	CountProjects(ctx context.Context) (int64, error)
	CountProjectsFiltered(ctx context.Context, filterSearch string) (int64, error)
	CountProjectsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CountProjectsByClusterFiltered(ctx context.Context, arg sqlc.CountProjectsByClusterFilteredParams) (int64, error)

	// Phase B3 additions: per-namespace reconcile state. Kept as the same
	// interface so a single sqlc.*Queries instance can satisfy everything,
	// and so test fakes only need to implement the methods they exercise.
	GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	GetDefaultPodSecurityTemplate(ctx context.Context) (sqlc.PodSecurityTemplate, error)
	UpsertProjectNamespace(ctx context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(ctx context.Context, arg sqlc.DeleteProjectNamespaceParams) error
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
	UpsertProjectResourceQuotaAllocation(ctx context.Context, arg sqlc.UpsertProjectResourceQuotaAllocationParams) (sqlc.ProjectResourceQuotaAllocation, error)
	DeleteProjectResourceQuotaAllocation(ctx context.Context, arg sqlc.DeleteProjectResourceQuotaAllocationParams) error
	ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error)
	ClaimProjectNamespaceReconcile(ctx context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error)
	MarkProjectNamespaceReconciled(ctx context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) (int64, error)

	// Quota usage endpoint needs the cluster display name for the response
	// shape (one row per (cluster, namespace)). The full Cluster row is
	// already loaded so we don't have to wedge a name-only query.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	// RBAC matrix: per-project bindings + role lookups for the
	// /projects/{id}/rbac/ endpoint. Rancher's flagship "Project" UX
	// is the multi-namespace abstraction + project-scoped RBAC; the
	// matrix is what makes this concrete for operators.
	ListProjectRoleBindingsByProject(ctx context.Context, arg sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error)
	GetProjectRoleByID(ctx context.Context, id uuid.UUID) (sqlc.ProjectRole, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// ProjectHandler handles project endpoints.
//
// Namespace membership writes use a mandatory transaction runner so project
// state, the sidecar, every affected reconcile intent, and audit evidence share
// one commit decision. The tunnel requester is consumed only by workers and the
// periodic recovery sweep, never by request-spawned goroutines.
type ProjectHandler struct {
	queries    ProjectQuerier
	service    *projectdomain.Service
	taskOutbox tasks.TaskOutboxWriter
	requester  K8sRequester
	log        *slog.Logger
	encryptor  *auth.Encryptor

	reconcileOnce sync.Once
	runSweep      func(context.Context, *asynq.Task) error

	// maintenanceGate is the migration-057 hook on project.delete.
	// Optional + nil-safe; see clusters.SetMaintenanceGate.
	maintenanceGate *MaintenanceGate

	// enforcer gates Create against the per-project cluster-density cap
	// (migration 051, max_clusters_per_project). Optional + nil-safe.
	enforcer *quota.Enforcer

	// runTx wraps namespace state, sidecar, task intents, and audit evidence in a
	// single pgx transaction. A nil runner fails membership mutations closed.
	runTx projectRunTxFunc

	// rbacBindings is the cache-fronted RBAC binding querier. project_namespaces
	// membership is a NEW input to the namespace-scoped synthetic bindings that
	// GetUserBindings caches, so AddNamespace / RemoveNamespace flush the cache
	// after a membership change — otherwise a removed namespace keeps granting
	// access for up to the cache TTL. Optional + nil-safe.
	rbacBindings rbac.BindingQuerier

	// authz scope-filters the list page to the projects the caller may see.
	// See ClusterHandler.authz — mandatory wherever the list route is served.
	authz authorizationSupport
}

// SetAuthorization wires the RBAC engine + binding querier used to scope-filter
// GET /projects/. See ClusterHandler.SetAuthorization.
func (h *ProjectHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// ProjectNamespaceTx is the tx-scoped query surface AddNamespace /
// RemoveNamespace use to mutate the projects.namespaces JSONB and the
// project_namespaces sidecar atomically. GetProjectByIDForUpdate takes a
// SELECT ... FOR UPDATE row lock so concurrent add/remove calls serialize on
// the read-modify-write of the JSONB list instead of last-writer-wins.
type ProjectNamespaceTx interface {
	audit.OutboxQuerier
	projectdomain.NamespaceTx
}

// projectRunTxFunc runs fn inside a single pgx transaction, committing on a nil
// return and rolling back otherwise. Production wiring begins a pgx tx and hands
// sqlc.New(tx); test code models the same atomicity with an in-memory fake.
type projectRunTxFunc func(ctx context.Context, fn func(q ProjectNamespaceTx) error) error

type projectOwnershipQuerier interface {
	GetProjectOwnership(ctx context.Context, id uuid.UUID) (sqlc.FleetOwnership, error)
}

type projectOwnershipTransferQuerier interface {
	projectOwnershipQuerier
	SetProjectOwnership(ctx context.Context, arg sqlc.SetProjectOwnershipParams) (sqlc.FleetOwnership, error)
}

var errProjectOwnershipTransferUnsupported = errors.New("project ownership can only be transferred from crd to api")

func NewProjectHandler(queries ProjectQuerier) *ProjectHandler {
	return &ProjectHandler{queries: queries, service: projectdomain.NewService(queries, nil)}
}

// SetTaskOutbox wires durable project reconcile delivery for project mutation
// paths outside AddNamespace/RemoveNamespace. Namespace membership mutations
// write through their transaction-bound ProjectNamespaceTx instead.
func (h *ProjectHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
	h.service = projectdomain.NewService(h.queries, q)
}

func (h *ProjectHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetK8sRequester wires the tunnel-backed K8sRequester used by the in-process
// project reconcile sweep.
func (h *ProjectHandler) SetK8sRequester(requester K8sRequester) {
	h.requester = requester
}

// SetReconcileSweep accepts the domain-composed periodic reconcile operation.
// Server composition owns this graph so workers never acquire dependencies
// through an HTTP handler.
func (h *ProjectHandler) SetReconcileSweep(run func(context.Context, *asynq.Task) error) {
	if h == nil {
		return
	}
	h.runSweep = run
}

// SetLogger replaces the handler's logger. Optional; defaults to slog.Default.
func (h *ProjectHandler) SetLogger(log *slog.Logger) { h.log = log }

// SetQuotaEnforcer wires the per-tenant quota enforcer that gates Create
// against the per-project cluster-density cap (migration 051,
// max_clusters_per_project). Optional; nil disables the check so tests can
// construct the handler without it.
func (h *ProjectHandler) SetQuotaEnforcer(e *quota.Enforcer) {
	if h == nil {
		return
	}
	h.enforcer = e
}

// SetMaintenanceGate wires the migration-057 maintenance-window gate
// applied to project.delete. Optional + nil-safe.
func (h *ProjectHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

// SetRunTx wires the mandatory pgx-transaction seam used by AddNamespace /
// RemoveNamespace.
func (h *ProjectHandler) SetRunTx(runTx projectRunTxFunc) {
	if h == nil {
		return
	}
	h.runTx = runTx
}

// SetRBACInvalidator wires the cache-fronted RBAC binding querier so
// AddNamespace / RemoveNamespace can flush the namespace-scoped authorization
// cache after a membership change. Nil-safe, but see RBACInvalidatorWired:
// leaving it unset is a revocation hole, not a feature toggle.
func (h *ProjectHandler) SetRBACInvalidator(bindings rbac.BindingQuerier) {
	if h == nil {
		return
	}
	h.rbacBindings = bindings
}

// RBACInvalidatorWired reports whether invalidateRBACCache can actually flush
// anything. Without it the post-add/remove-namespace flush is a silent no-op and
// a namespace removed from a project keeps authorizing reads until the cache
// entry expires on its own — so production startup validation rejects a server
// wired this way rather than shipping stale authorization.
func (h *ProjectHandler) RBACInvalidatorWired() bool {
	if h == nil || h.rbacBindings == nil {
		return false
	}
	inv, ok := h.rbacBindings.(rbac.CacheInvalidator)
	if !ok {
		return false
	}
	// A typed nil satisfies the interface: middleware.NewSQLCRBACQuerierWithCache
	// returns a nil *SQLCRBACQuerier when its queries argument is nil, and
	// boxing that into RBACQuerier makes h.rbacBindings != nil while
	// InvalidateAll() no-ops on the nil receiver. That is exactly the permanent
	// silent no-op this guard exists to reject, so unwrap the interface and
	// reject a nil pointer.
	if v := reflect.ValueOf(inv); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	// A cacheless querier cannot flush either — Invalidate/InvalidateAll both
	// return at their cache guard. The domain-owned status contract avoids
	// coupling this handler to the middleware cache implementation.
	if status, ok := inv.(rbac.CacheStatus); ok && !status.RBACCacheEnabled() {
		return false
	}
	return true
}

// invalidateRBACCache flushes the whole RBAC binding cache. Called after a
// project_namespaces membership change: project_namespaces feeds the
// namespace-scoped synthetic bindings, and there is no reverse project->users
// index for a targeted invalidation, so we mirror rbac.go's invalidateAll.
func (h *ProjectHandler) invalidateRBACCache() {
	if h == nil || h.rbacBindings == nil {
		return
	}
	if inv, ok := h.rbacBindings.(rbac.CacheInvalidator); ok {
		inv.InvalidateAll()
	}
}

// StartReconciler runs an in-process periodic sweep that re-applies every
// project_namespaces row's enforcement objects. The cooperative DB lease in
// project_namespaces.locked_until guards against multiple worker pods
// double-applying the same row.
//
// StartReconciler is a no-op until SetK8sRequester has been called.
func (h *ProjectHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	h.reconcileOnce.Do(func() {
		go h.runReconciler(ctx)
	})
}

func (h *ProjectHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.requester == nil {
				continue
			}
			if h.runSweep == nil {
				continue
			}
			if err := h.runSweep(ctx, nil); err != nil {
				h.logger().Warn("project reconcile sweep failed", "error", err)
			}
		}
	}
}

func (h *ProjectHandler) logger() *slog.Logger {
	if h.log != nil {
		return h.log
	}
	return slog.Default()
}

// ProjectResponse represents a project in API responses. The B3 fields
// (limit_range, network_policy_mode) are surfaced so the UI can show
// enforcement settings; legacy fields stay where they were so existing
// frontends don't break on a partial deploy.
//
// Migration 040 added pod_security_profile + resource_quota_{cpu,memory,pod}
// fields. They're appended to the response shape; clients that ignore unknown
// fields keep working unchanged.
type ProjectResponse struct {
	ID                       string          `json:"id"`
	Name                     string          `json:"name"`
	DisplayName              string          `json:"display_name"`
	Description              string          `json:"description"`
	ClusterID                string          `json:"cluster_id"`
	Namespaces               json.RawMessage `json:"namespaces"`
	ResourceQuota            json.RawMessage `json:"resource_quota"`
	LimitRange               json.RawMessage `json:"limit_range"`
	NetworkPolicyMode        string          `json:"network_policy_mode"`
	PodSecurityProfile       string          `json:"pod_security_profile"`
	ResourceQuotaCpuLimit    string          `json:"resource_quota_cpu_limit"`
	ResourceQuotaMemoryLimit string          `json:"resource_quota_memory_limit"`
	ResourceQuotaPodCount    int32           `json:"resource_quota_pod_count"`
	CreatedByID              *string         `json:"created_by_id"`
	CreatedAt                string          `json:"created_at"`
	UpdatedAt                string          `json:"updated_at"`
}

// ProjectResourceCapResponse is the public, typed representation of a
// project-wide Kubernetes resource cap. Empty strings / zero remain unlimited
// for that one dimension.
type ProjectResourceCapResponse struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Pods   int32  `json:"pods"`
}

// ProjectResourceQuotaSummaryResponse distinguishes the configured project
// total from the namespace allocations. Remaining is unallocated capacity,
// not live usage; live used values remain in each quota-usage result.
type ProjectResourceQuotaSummaryResponse struct {
	Total     ProjectResourceCapResponse `json:"total"`
	Allocated ProjectResourceCapResponse `json:"allocated"`
	Remaining ProjectResourceCapResponse `json:"remaining"`
}

func projectResourceCapResponse(cap projectquota.Cap) ProjectResourceCapResponse {
	return ProjectResourceCapResponse{CPU: cap.CPU, Memory: cap.Memory, Pods: cap.Pods}
}

func projectToResponse(p sqlc.Project) ProjectResponse {
	resp := ProjectResponse{
		ID:                       p.ID.String(),
		Name:                     p.Name,
		DisplayName:              p.DisplayName,
		Description:              p.Description,
		ClusterID:                p.ClusterID.String(),
		Namespaces:               p.Namespaces,
		ResourceQuota:            p.ResourceQuota,
		LimitRange:               p.LimitRange,
		NetworkPolicyMode:        p.NetworkPolicyMode,
		PodSecurityProfile:       p.PodSecurityProfile,
		ResourceQuotaCpuLimit:    p.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: p.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    p.ResourceQuotaPodCount,
		CreatedAt:                p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:                p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if resp.LimitRange == nil {
		resp.LimitRange = json.RawMessage(`{}`)
	}
	if resp.NetworkPolicyMode == "" {
		resp.NetworkPolicyMode = "none"
	}
	if p.CreatedByID.Valid {
		s := uuid.UUID(p.CreatedByID.Bytes).String()
		resp.CreatedByID = &s
	}
	return resp
}

// CreateProjectRequest represents the request body for creating a project.
//
// Policy fields are pointers so an older client (which doesn't know about
// them yet) can omit them and pick up the defaults rather than reset existing
// rows to the zero value. Field-by-field semantics:
//
//   - PodSecurityProfile: omitted ⇒ defaultPodSecurityProfile (baseline).
//   - ResourceQuota*:     omitted ⇒ unbounded (empty string / 0).
//
// openapi:request CreateProjectRequest
type CreateProjectRequest struct {
	Name                     string          `json:"name" validate:"required"`
	DisplayName              string          `json:"display_name"`
	Description              string          `json:"description"`
	ClusterID                string          `json:"cluster_id"`
	Namespaces               json.RawMessage `json:"namespaces"`
	ResourceQuota            json.RawMessage `json:"resource_quota"`
	LimitRange               json.RawMessage `json:"limit_range"`
	NetworkPolicyMode        string          `json:"network_policy_mode"`
	PodSecurityProfile       *string         `json:"pod_security_profile,omitempty"`
	ResourceQuotaCpuLimit    *string         `json:"resource_quota_cpu_limit,omitempty"`
	ResourceQuotaMemoryLimit *string         `json:"resource_quota_memory_limit,omitempty"`
	ResourceQuotaPodCount    *int32          `json:"resource_quota_pod_count,omitempty"`
}

// UpdateProjectRequest represents the request body for updating a project.
//
// Policy fields stay pointer-typed so an old client that doesn't know about
// the new columns can still PUT the project without nuking them. If a field
// is omitted from the payload, the existing DB value is preserved (the
// handler loads the row first to copy missing fields through to UpdateProject).
// openapi:request UpdateProjectRequest
type UpdateProjectRequest struct {
	// Name is retained as an ignored v1 compatibility field. Project names are
	// immutable; older generated clients included it in update bodies.
	Name                      string          `json:"name,omitempty"`
	DisplayName               string          `json:"display_name"`
	Description               string          `json:"description"`
	Namespaces                json.RawMessage `json:"namespaces"`
	ResourceQuota             json.RawMessage `json:"resource_quota"`
	LimitRange                json.RawMessage `json:"limit_range"`
	NetworkPolicyMode         string          `json:"network_policy_mode"`
	PodSecurityProfile        *string         `json:"pod_security_profile,omitempty"`
	ResourceQuotaCpuLimit     *string         `json:"resource_quota_cpu_limit,omitempty"`
	ResourceQuotaMemoryLimit  *string         `json:"resource_quota_memory_limit,omitempty"`
	ResourceQuotaPodCount     *int32          `json:"resource_quota_pod_count,omitempty"`
	ResourceQuotaCPULegacy    *string         `json:"resource_quota_cpu,omitempty"`
	ResourceQuotaMemoryLegacy *string         `json:"resource_quota_memory,omitempty"`
	ResourceQuotaPodsLegacy   *int32          `json:"resource_quota_pods,omitempty"`
}

// UpdateProjectPolicyRequest is the body for PATCH /projects/{id}/policy/.
// All fields are optional; missing ones leave the existing value in place.
// openapi:request UpdateProjectPolicyRequest
type UpdateProjectPolicyRequest struct {
	PodSecurityProfile        *string `json:"pod_security_profile,omitempty"`
	NetworkPolicyMode         *string `json:"network_policy_mode,omitempty"`
	ResourceQuotaCpuLimit     *string `json:"resource_quota_cpu_limit,omitempty"`
	ResourceQuotaMemoryLimit  *string `json:"resource_quota_memory_limit,omitempty"`
	ResourceQuotaPodCount     *int32  `json:"resource_quota_pod_count,omitempty"`
	ResourceQuotaCPULegacy    *string `json:"resource_quota_cpu,omitempty"`
	ResourceQuotaMemoryLegacy *string `json:"resource_quota_memory,omitempty"`
	ResourceQuotaPodsLegacy   *int32  `json:"resource_quota_pods,omitempty"`
}

func (r *UpdateProjectRequest) normalizeLegacyQuotaFields() {
	if r.ResourceQuotaCpuLimit == nil {
		r.ResourceQuotaCpuLimit = r.ResourceQuotaCPULegacy
	}
	if r.ResourceQuotaMemoryLimit == nil {
		r.ResourceQuotaMemoryLimit = r.ResourceQuotaMemoryLegacy
	}
	if r.ResourceQuotaPodCount == nil {
		r.ResourceQuotaPodCount = r.ResourceQuotaPodsLegacy
	}
}

func (r *UpdateProjectPolicyRequest) normalizeLegacyQuotaFields() {
	if r.ResourceQuotaCpuLimit == nil {
		r.ResourceQuotaCpuLimit = r.ResourceQuotaCPULegacy
	}
	if r.ResourceQuotaMemoryLimit == nil {
		r.ResourceQuotaMemoryLimit = r.ResourceQuotaMemoryLegacy
	}
	if r.ResourceQuotaPodCount == nil {
		r.ResourceQuotaPodCount = r.ResourceQuotaPodsLegacy
	}
}

// List handles GET /api/v1/projects/.
