package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ProjectQuerier abstracts project-related database queries.
type ProjectQuerier interface {
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	ListProjects(ctx context.Context, arg sqlc.ListProjectsParams) ([]sqlc.Project, error)
	ListProjectsByCluster(ctx context.Context, arg sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error)
	CreateProject(ctx context.Context, arg sqlc.CreateProjectParams) (sqlc.Project, error)
	UpdateProject(ctx context.Context, arg sqlc.UpdateProjectParams) (sqlc.Project, error)
	DeleteProject(ctx context.Context, id uuid.UUID) error
	CountProjects(ctx context.Context) (int64, error)
	CountProjectsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
}

// ProjectHandler handles project endpoints.
type ProjectHandler struct {
	queries ProjectQuerier
}

// NewProjectHandler creates a new project handler.
func NewProjectHandler(queries ProjectQuerier) *ProjectHandler {
	return &ProjectHandler{queries: queries}
}

// ProjectResponse represents a project in API responses.
type ProjectResponse struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	DisplayName   string          `json:"display_name"`
	Description   string          `json:"description"`
	ClusterID     string          `json:"cluster_id"`
	Namespaces    json.RawMessage `json:"namespaces"`
	ResourceQuota json.RawMessage `json:"resource_quota"`
	CreatedByID   *string         `json:"created_by_id"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

func projectToResponse(p sqlc.Project) ProjectResponse {
	resp := ProjectResponse{
		ID:            p.ID.String(),
		Name:          p.Name,
		DisplayName:   p.DisplayName,
		Description:   p.Description,
		ClusterID:     p.ClusterID.String(),
		Namespaces:    p.Namespaces,
		ResourceQuota: p.ResourceQuota,
		CreatedAt:     p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if p.CreatedByID.Valid {
		s := uuid.UUID(p.CreatedByID.Bytes).String()
		resp.CreatedByID = &s
	}
	return resp
}

// CreateProjectRequest represents the request body for creating a project.
type CreateProjectRequest struct {
	Name          string          `json:"name"`
	DisplayName   string          `json:"display_name"`
	Description   string          `json:"description"`
	ClusterID     string          `json:"cluster_id"`
	Namespaces    json.RawMessage `json:"namespaces"`
	ResourceQuota json.RawMessage `json:"resource_quota"`
}

// UpdateProjectRequest represents the request body for updating a project.
type UpdateProjectRequest struct {
	DisplayName   string          `json:"display_name"`
	Description   string          `json:"description"`
	Namespaces    json.RawMessage `json:"namespaces"`
	ResourceQuota json.RawMessage `json:"resource_quota"`
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

	project, err := h.queries.CreateProject(r.Context(), sqlc.CreateProjectParams{
		Name:          req.Name,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		ClusterID:     clusterID,
		Namespaces:    req.Namespaces,
		ResourceQuota: req.ResourceQuota,
		CreatedByID:   createdByID,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create project")
		return
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

	project, err := h.queries.UpdateProject(r.Context(), sqlc.UpdateProjectParams{
		ID:            id,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		Namespaces:    req.Namespaces,
		ResourceQuota: req.ResourceQuota,
	})
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
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

	if err := h.queries.DeleteProject(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}

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
