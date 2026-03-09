package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ToolQuerier abstracts tool-related database queries.
type ToolQuerier interface {
	GetClusterToolByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTool, error)
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	ListClusterTools(ctx context.Context, arg sqlc.ListClusterToolsParams) ([]sqlc.ClusterTool, error)
	ListEnabledTools(ctx context.Context) ([]sqlc.ClusterTool, error)
	CountClusterTools(ctx context.Context) (int64, error)
}

// ToolHandler handles tool endpoints.
type ToolHandler struct {
	queries ToolQuerier
}

// NewToolHandler creates a new tool handler.
func NewToolHandler(queries ToolQuerier) *ToolHandler {
	return &ToolHandler{queries: queries}
}

// ToolResponse represents a cluster tool in API responses.
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
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
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

// List handles GET /api/v1/tools/.
func (h *ToolHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	tools, err := h.queries.ListClusterTools(r.Context(), sqlc.ListClusterToolsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tools")
		return
	}

	total, err := h.queries.CountClusterTools(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count tools")
		return
	}

	items := make([]ToolResponse, 0, len(tools))
	for _, t := range tools {
		items = append(items, toolToResponse(t))
	}

	RespondPaginated(w, r, items, total)
}

// Get handles GET /api/v1/tools/{id}/.
func (h *ToolHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid tool ID")
		return
	}

	tool, err := h.queries.GetClusterToolByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
		return
	}

	RespondJSON(w, http.StatusOK, toolToResponse(tool))
}

// GetBySlug handles GET /api/v1/tools/slug/{slug}/.
func (h *ToolHandler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		RespondError(w, http.StatusBadRequest, "invalid_slug", "Slug is required")
		return
	}

	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
		return
	}

	RespondJSON(w, http.StatusOK, toolToResponse(tool))
}
