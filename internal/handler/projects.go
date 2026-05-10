package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

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
	DeleteProject(ctx context.Context, id uuid.UUID) error
	CountProjects(ctx context.Context) (int64, error)
	CountProjectsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)

	// Phase B3 additions: per-namespace reconcile state. Kept as the same
	// interface so a single sqlc.*Queries instance can satisfy everything,
	// and so test fakes only need to implement the methods they exercise.
	GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	GetDefaultPodSecurityTemplate(ctx context.Context) (sqlc.PodSecurityTemplate, error)
	UpsertProjectNamespace(ctx context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(ctx context.Context, arg sqlc.DeleteProjectNamespaceParams) error
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
	ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error)
	ClaimProjectNamespaceReconcile(ctx context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error)
	MarkProjectNamespaceReconciled(ctx context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) error
}

// ProjectHandler handles project endpoints.
//
// Phase B3 wiring: when both `requester` (a tunnel-backed K8sRequester) and
// `queue` (the asynq client) are non-nil, AddNamespace / RemoveNamespace
// enqueue real project:reconcile tasks and the periodic sweep is driven by
// StartReconciler. The handler is intentionally still functional with both
// set to nil — server boot code wires them in a follow-up; today's existing
// tests that hand the handler only `queries` continue to pass.
type ProjectHandler struct {
	queries   ProjectQuerier
	queue     *asynq.Client
	requester K8sRequester
	log       *slog.Logger

	reconcileOnce sync.Once
	runTask       func(context.Context, *asynq.Task) error
}

// NewProjectHandler creates a new project handler. Phase B3 introduces extra
// dependencies (queue + K8sRequester) but they are intentionally wired via
// setters below so the constructor signature stays compatible with all
// existing call sites (server.go, tests).
func NewProjectHandler(queries ProjectQuerier) *ProjectHandler {
	return &ProjectHandler{
		queries: queries,
		runTask: tasks.HandleProjectReconcile,
	}
}

// SetTaskQueue wires the asynq client used to enqueue project:reconcile and
// project:reconcile:remove tasks. Optional; without it AddNamespace /
// RemoveNamespace still update the DB but no enforcement happens.
func (h *ProjectHandler) SetTaskQueue(queue *asynq.Client) { h.queue = queue }

// SetK8sRequester wires the tunnel-backed K8sRequester used by the in-process
// project reconcile sweep. Calling SetK8sRequester also configures the
// worker/tasks package so it can perform applies against connected clusters
// when running inside the server process.
func (h *ProjectHandler) SetK8sRequester(requester K8sRequester) {
	h.requester = requester
	if requester == nil {
		return
	}
	tasks.ConfigureProjectReconcile(tasks.ProjectReconcileDeps{
		Queries:   projectQuerierAdapter{h.queries},
		Requester: projectRequesterAdapter{requester},
	})
}

// SetLogger replaces the handler's logger. Optional; defaults to slog.Default.
func (h *ProjectHandler) SetLogger(log *slog.Logger) { h.log = log }

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
			if err := tasks.HandleProjectReconcileAll(ctx, nil); err != nil {
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
type ProjectResponse struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	ClusterID         string          `json:"cluster_id"`
	Namespaces        json.RawMessage `json:"namespaces"`
	ResourceQuota     json.RawMessage `json:"resource_quota"`
	LimitRange        json.RawMessage `json:"limit_range"`
	NetworkPolicyMode string          `json:"network_policy_mode"`
	CreatedByID       *string         `json:"created_by_id"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

func projectToResponse(p sqlc.Project) ProjectResponse {
	resp := ProjectResponse{
		ID:                p.ID.String(),
		Name:              p.Name,
		DisplayName:       p.DisplayName,
		Description:       p.Description,
		ClusterID:         p.ClusterID.String(),
		Namespaces:        p.Namespaces,
		ResourceQuota:     p.ResourceQuota,
		LimitRange:        p.LimitRange,
		NetworkPolicyMode: p.NetworkPolicyMode,
		CreatedAt:         p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:         p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
type CreateProjectRequest struct {
	Name              string          `json:"name"`
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	ClusterID         string          `json:"cluster_id"`
	Namespaces        json.RawMessage `json:"namespaces"`
	ResourceQuota     json.RawMessage `json:"resource_quota"`
	LimitRange        json.RawMessage `json:"limit_range"`
	NetworkPolicyMode string          `json:"network_policy_mode"`
}

// UpdateProjectRequest represents the request body for updating a project.
type UpdateProjectRequest struct {
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	Namespaces        json.RawMessage `json:"namespaces"`
	ResourceQuota     json.RawMessage `json:"resource_quota"`
	LimitRange        json.RawMessage `json:"limit_range"`
	NetworkPolicyMode string          `json:"network_policy_mode"`
}

// List handles GET /api/v1/projects/.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	projects, err := h.queries.ListProjects(r.Context(), sqlc.ListProjectsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list projects")
		return
	}

	total, err := h.queries.CountProjects(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count projects")
		return
	}

	items := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		items = append(items, projectToResponse(p))
	}

	RespondPaginated(w, r, items, total)
}

// Create handles POST /api/v1/projects/.
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Project name is required")
		return
	}

	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "validation_error", "Invalid cluster_id")
		return
	}

	var createdByID pgtype.UUID
	if uid, err := uuid.Parse(user.ID); err == nil {
		createdByID = pgtype.UUID{Bytes: uid, Valid: true}
	}

	if req.Namespaces == nil {
		req.Namespaces = json.RawMessage(`[]`)
	}
	if req.ResourceQuota == nil {
		req.ResourceQuota = json.RawMessage(`{}`)
	}
	if req.LimitRange == nil {
		req.LimitRange = json.RawMessage(`{}`)
	}
	if req.NetworkPolicyMode == "" {
		req.NetworkPolicyMode = "none"
	}

	project, err := h.queries.CreateProject(r.Context(), sqlc.CreateProjectParams{
		Name:              req.Name,
		DisplayName:       req.DisplayName,
		Description:       req.Description,
		ClusterID:         clusterID,
		Namespaces:        req.Namespaces,
		ResourceQuota:     req.ResourceQuota,
		LimitRange:        req.LimitRange,
		NetworkPolicyMode: req.NetworkPolicyMode,
		CreatedByID:       createdByID,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create project")
		return
	}
	h.recordProjectAudit(r, "project.create", project, map[string]any{"clusterId": req.ClusterID, "namespaces": decodeJSONArray(req.Namespaces)})

	// Seed the project_namespaces sidecar from any namespaces specified at
	// create time. A reconcile is enqueued for each so enforcement lands
	// without an extra round-trip.
	for _, ns := range decodeNamespaceList(project.Namespaces) {
		h.upsertAndEnqueue(r.Context(), project.ID, project.ClusterID, ns)
	}

	RespondJSON(w, http.StatusCreated, projectToResponse(project))
}

// Get handles GET /api/v1/projects/{id}/.
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	RespondJSON(w, http.StatusOK, projectToResponse(project))
}

// Update handles PUT /api/v1/projects/{id}/.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}

	var req UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Namespaces == nil {
		req.Namespaces = json.RawMessage(`[]`)
	}
	if req.ResourceQuota == nil {
		req.ResourceQuota = json.RawMessage(`{}`)
	}
	if req.LimitRange == nil {
		req.LimitRange = json.RawMessage(`{}`)
	}
	if req.NetworkPolicyMode == "" {
		req.NetworkPolicyMode = "none"
	}

	project, err := h.queries.UpdateProject(r.Context(), sqlc.UpdateProjectParams{
		ID:                id,
		DisplayName:       req.DisplayName,
		Description:       req.Description,
		Namespaces:        req.Namespaces,
		ResourceQuota:     req.ResourceQuota,
		LimitRange:        req.LimitRange,
		NetworkPolicyMode: req.NetworkPolicyMode,
	})
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	h.recordProjectAudit(r, "project.update", project, map[string]any{"namespaces": decodeJSONArray(req.Namespaces)})

	// Re-enqueue every namespace currently on the project so any quota /
	// limit / network-policy changes from this Update propagate. Cheap:
	// asynq dedupes by payload; the periodic sweep also re-converges.
	for _, ns := range decodeNamespaceList(project.Namespaces) {
		h.upsertAndEnqueue(r.Context(), project.ID, project.ClusterID, ns)
	}

	RespondJSON(w, http.StatusOK, projectToResponse(project))
}

// Delete handles DELETE /api/v1/projects/{id}/.
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}

	// Enqueue cleanup for every namespace before removing the project so the
	// managed CRs don't outlive their owner.
	for _, ns := range decodeNamespaceList(project.Namespaces) {
		h.enqueueCleanup(r.Context(), project.ID, project.ClusterID, ns)
	}

	if err := h.queries.DeleteProject(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	h.recordProjectAudit(r, "project.delete", project, map[string]any{"clusterId": project.ClusterID.String()})

	w.WriteHeader(http.StatusNoContent)
}

// ListByCluster handles GET /api/v1/clusters/{cluster_id}/projects/.
func (h *ProjectHandler) ListByCluster(w http.ResponseWriter, r *http.Request) {
	clusterIDStr := chi.URLParam(r, "cluster_id")
	clusterID, err := uuid.Parse(clusterIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	projects, err := h.queries.ListProjectsByCluster(r.Context(), sqlc.ListProjectsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list projects")
		return
	}

	total, err := h.queries.CountProjectsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count projects")
		return
	}

	items := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		items = append(items, projectToResponse(p))
	}

	RespondPaginated(w, r, items, total)
}

// ProjectNamespaceRequest represents the request body for add/remove namespace.
type ProjectNamespaceRequest struct {
	Namespace string `json:"namespace"`
}

// AddNamespace handles POST /api/v1/projects/{id}/add-namespace/.
//
// On success this writes the namespace into both the legacy projects.namespaces
// JSONB column AND the project_namespaces sidecar (used by the reconcile
// task to track per-namespace enforcement state). A project:reconcile task
// is enqueued so the agent applies the ResourceQuota / LimitRange /
// NetworkPolicy without waiting for the periodic sweep.
func (h *ProjectHandler) AddNamespace(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}
	var req ProjectNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "namespace is required")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}

	namespaces := decodeNamespaceList(project.Namespaces)
	for _, ns := range namespaces {
		if ns == req.Namespace {
			RespondError(w, http.StatusConflict, "namespace_exists", "Namespace '"+req.Namespace+"' is already in this project.")
			return
		}
	}

	// Check for conflict with other projects in the same cluster.
	otherProjects, err := h.queries.ListProjectsByCluster(r.Context(), sqlc.ListProjectsByClusterParams{
		ClusterID: project.ClusterID,
		Limit:     1000,
		Offset:    0,
	})
	if err == nil {
		for _, other := range otherProjects {
			if other.ID == project.ID {
				continue
			}
			for _, ns := range decodeNamespaceList(other.Namespaces) {
				if ns == req.Namespace {
					RespondError(w, http.StatusConflict, "namespace_claimed", "Namespace '"+req.Namespace+"' is already assigned to project '"+other.Name+"'.")
					return
				}
			}
		}
	}

	namespaces = append(namespaces, req.Namespace)
	encoded, err := json.Marshal(namespaces)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "marshal_error", "Failed to encode namespaces")
		return
	}

	updated, err := h.queries.UpdateProject(r.Context(), sqlc.UpdateProjectParams{
		ID:                id,
		DisplayName:       project.DisplayName,
		Description:       project.Description,
		Namespaces:        encoded,
		ResourceQuota:     defaultIfEmpty(project.ResourceQuota, `{}`),
		LimitRange:        defaultIfEmpty(project.LimitRange, `{}`),
		NetworkPolicyMode: defaultMode(project.NetworkPolicyMode),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update project")
		return
	}
	h.upsertAndEnqueue(r.Context(), project.ID, project.ClusterID, req.Namespace)
	h.recordProjectAudit(r, "project.add_namespace", updated, map[string]any{"namespace": req.Namespace})
	RespondJSON(w, http.StatusOK, projectToResponse(updated))
}

// RemoveNamespace handles POST /api/v1/projects/{id}/remove-namespace/.
//
// The reconcile cleanup task is enqueued before the namespace is removed
// from the project's JSONB list so it can rely on the project_namespaces
// row still existing while it deletes managed CRs from the cluster.
func (h *ProjectHandler) RemoveNamespace(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}
	var req ProjectNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "namespace is required")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}

	namespaces := decodeNamespaceList(project.Namespaces)
	filtered := make([]string, 0, len(namespaces))
	found := false
	for _, ns := range namespaces {
		if ns == req.Namespace {
			found = true
			continue
		}
		filtered = append(filtered, ns)
	}
	if !found {
		RespondError(w, http.StatusNotFound, "namespace_not_found", "Namespace '"+req.Namespace+"' is not in this project.")
		return
	}

	encoded, err := json.Marshal(filtered)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "marshal_error", "Failed to encode namespaces")
		return
	}

	// Enqueue cleanup BEFORE the DB update so the task still has the row to
	// reference; the cleanup task itself deletes the project_namespaces row.
	h.enqueueCleanup(r.Context(), project.ID, project.ClusterID, req.Namespace)

	updated, err := h.queries.UpdateProject(r.Context(), sqlc.UpdateProjectParams{
		ID:                id,
		DisplayName:       project.DisplayName,
		Description:       project.Description,
		Namespaces:        encoded,
		ResourceQuota:     defaultIfEmpty(project.ResourceQuota, `{}`),
		LimitRange:        defaultIfEmpty(project.LimitRange, `{}`),
		NetworkPolicyMode: defaultMode(project.NetworkPolicyMode),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update project")
		return
	}
	h.recordProjectAudit(r, "project.remove_namespace", updated, map[string]any{"namespace": req.Namespace})
	RespondJSON(w, http.StatusOK, projectToResponse(updated))
}

// upsertAndEnqueue persists the project_namespaces row and schedules an apply
// task. When the server has a live tunnel requester, we execute the task
// in-process so enforcement does not depend on the Redis worker, which has no
// cluster access. Queue enqueue remains as a fallback for contexts that do not
// have a requester wired.
func (h *ProjectHandler) upsertAndEnqueue(ctx context.Context, projectID, clusterID uuid.UUID, namespace string) {
	if h.queries == nil {
		return
	}
	if _, err := h.queries.UpsertProjectNamespace(ctx, sqlc.UpsertProjectNamespaceParams{
		ProjectID: projectID,
		ClusterID: clusterID,
		Namespace: namespace,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.logger().Warn("upsert project_namespace", "project_id", projectID.String(), "namespace", namespace, "error", err)
	}
	task, err := tasks.NewProjectReconcileTask(tasks.ProjectReconcilePayload{
		ProjectID: projectID.String(),
		ClusterID: clusterID.String(),
		Namespace: namespace,
		Op:        "apply",
	})
	if err != nil {
		h.logger().Warn("build project reconcile task", "error", err)
		return
	}
	if h.requester != nil {
		h.dispatchProjectTask(ctx, task)
		return
	}
	if h.queue == nil {
		return
	}
	if _, err := h.queue.Enqueue(task); err != nil {
		h.logger().Warn("enqueue project reconcile task", "error", err)
	}
}

// enqueueCleanup is the RemoveNamespace counterpart. The task itself deletes
// the project_namespaces row and the in-cluster managed CRs. As with apply,
// a live requester means we should execute locally instead of depending on the
// external worker.
func (h *ProjectHandler) enqueueCleanup(ctx context.Context, projectID, clusterID uuid.UUID, namespace string) {
	task, err := tasks.NewProjectReconcileTask(tasks.ProjectReconcilePayload{
		ProjectID: projectID.String(),
		ClusterID: clusterID.String(),
		Namespace: namespace,
		Op:        "remove",
	})
	if err != nil {
		h.logger().Warn("build project cleanup task", "error", err)
		return
	}
	if h.requester != nil {
		h.dispatchProjectTask(ctx, task)
		return
	}
	if h.queue == nil {
		// Without an asynq client wired we still attempt the synchronous DB
		// half so the row goes away and the UI reflects the removal. The
		// in-cluster cleanup will only happen once a worker is wired up.
		_ = h.queries.DeleteProjectNamespace(context.Background(), sqlc.DeleteProjectNamespaceParams{
			ProjectID: projectID,
			ClusterID: clusterID,
			Namespace: namespace,
		})
		return
	}
	if _, err := h.queue.Enqueue(task); err != nil {
		h.logger().Warn("enqueue project cleanup task", "error", err)
	}
}

func (h *ProjectHandler) dispatchProjectTask(ctx context.Context, task *asynq.Task) {
	if h == nil || task == nil {
		return
	}
	runTask := h.runTask
	if runTask == nil {
		runTask = tasks.HandleProjectReconcile
	}
	go func() {
		runCtx := context.Background()
		if ctx != nil {
			runCtx = context.WithoutCancel(ctx)
		}
		if err := runTask(runCtx, task); err != nil {
			h.logger().Warn("run project reconcile task locally", "type", task.Type(), "error", err)
		}
	}()
}

func decodeNamespaceList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var items []string
	if err := json.Unmarshal(raw, &items); err == nil {
		return items
	}
	// Fall back to []any decode.
	var anyItems []any
	if err := json.Unmarshal(raw, &anyItems); err == nil {
		out := make([]string, 0, len(anyItems))
		for _, item := range anyItems {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return []string{}
}

func defaultIfEmpty(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

func defaultMode(mode string) string {
	switch mode {
	case "isolated", "allow-same-project", "none":
		return mode
	default:
		return "none"
	}
}

func (h *ProjectHandler) recordProjectAudit(r *http.Request, action string, project sqlc.Project, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	recordAudit(r, h.queries, action, "project", project.ID.String(), project.Name, detail)
}

func decodeJSONArray(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var items []any
	if json.Unmarshal(raw, &items) != nil {
		return []any{}
	}
	return items
}

// --- adapters: bridge handler-package types into the worker/tasks package ---
//
// The worker/tasks package defines its own minimal interfaces (so it has no
// import dependency on internal/handler). These adapters wrap our concrete
// types into those interfaces for ConfigureProjectReconcile.

type projectQuerierAdapter struct{ q ProjectQuerier }

func (a projectQuerierAdapter) GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error) {
	return a.q.GetProjectByID(ctx, id)
}

func (a projectQuerierAdapter) GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return a.q.GetClusterRegistryConfig(ctx, clusterID)
}

func (a projectQuerierAdapter) GetDefaultPodSecurityTemplate(ctx context.Context) (sqlc.PodSecurityTemplate, error) {
	return a.q.GetDefaultPodSecurityTemplate(ctx)
}

func (a projectQuerierAdapter) ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return a.q.ListProjectNamespaces(ctx, projectID)
}

func (a projectQuerierAdapter) ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error) {
	return a.q.ListAllProjectNamespaces(ctx)
}

func (a projectQuerierAdapter) UpsertProjectNamespace(ctx context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	return a.q.UpsertProjectNamespace(ctx, arg)
}

func (a projectQuerierAdapter) DeleteProjectNamespace(ctx context.Context, arg sqlc.DeleteProjectNamespaceParams) error {
	return a.q.DeleteProjectNamespace(ctx, arg)
}

func (a projectQuerierAdapter) ClaimProjectNamespaceReconcile(ctx context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return a.q.ClaimProjectNamespaceReconcile(ctx, arg)
}

func (a projectQuerierAdapter) MarkProjectNamespaceReconciled(ctx context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) error {
	return a.q.MarkProjectNamespaceReconciled(ctx, arg)
}

type projectRequesterAdapter struct{ r K8sRequester }

func (a projectRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*tasks.ProjectK8sResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	bodyBytes, _ := decodeResponseBody(resp)
	return &tasks.ProjectK8sResponse{StatusCode: resp.StatusCode, Body: bodyBytes}, nil
}
