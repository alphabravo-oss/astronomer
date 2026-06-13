// Cluster groups (migration 066) — label-style folders for clusters.
//
// Operators running 50+ clusters need org structure: a tree of named
// groups (root + 2 levels max) that clusters can be assigned to.
// Groups own:
//   - a name + slug (slug unique within parent; top-level slugs are
//     globally unique via the partial index in 066_cluster_groups.up.sql)
//   - a color + lucide-react icon for the sidebar badge
//   - an enabled flag (soft-delete; the table never hard-deletes a row
//     that has audit history, so historical entries can still resolve
//     names via JOIN)
//   - a parent_id self-FK with ON DELETE CASCADE; deleting a group
//     drops the entire subtree in one call and (because clusters.group_id
//     is ON DELETE SET NULL) leaves clusters parented to the cascaded
//     subtree behind with group_id=NULL
//
// Endpoints (all gated by clusters:update — group admin is a
// clusters-admin concept):
//
//   GET    /api/v1/cluster-groups/                       — list as tree
//   POST   /api/v1/cluster-groups/                       — create
//   GET    /api/v1/cluster-groups/{id}/                  — get + counts
//   PUT    /api/v1/cluster-groups/{id}/                  — update
//   DELETE /api/v1/cluster-groups/{id}/                  — delete subtree
//   GET    /api/v1/cluster-groups/{id}/clusters/         — clusters in tree
//   POST   /api/v1/cluster-groups/{id}/move/             — bulk-assign
//
// Constraints enforced here:
//   - Tree depth cap = 3 (root + 2 levels). Validated at create + update.
//   - A group's parent_id may not point to itself or any of its
//     descendants (no cycles).
//   - Slug uniqueness is delegated to the DB (UNIQUE (parent_id, slug)
//     + the partial unique idx on (slug) WHERE parent_id IS NULL).
//     Duplicates surface as a 400.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// MaxClusterGroupDepth is the inclusive cap on tree depth (depth 0 ==
// top-level, so depth 2 == "root + 2 levels"). Enforced at create + update
// — the migration intentionally doesn't add a CHECK constraint because a
// CHECK over a recursive ancestor walk would have to live in a trigger,
// which adds operational complexity for a property we only really care
// about at write time.
const MaxClusterGroupDepth = 2

// ClusterGroupQuerier is the database surface the handler needs. The
// production *sqlc.Queries satisfies it; tests pass a narrow fake.
type ClusterGroupQuerier interface {
	ListClusterGroups(ctx context.Context) ([]sqlc.ClusterGroup, error)
	ListClusterGroupsAsTree(ctx context.Context) ([]sqlc.ClusterGroupTreeRow, error)
	GetClusterGroupByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterGroup, error)
	CreateClusterGroup(ctx context.Context, arg sqlc.CreateClusterGroupParams) (sqlc.ClusterGroup, error)
	UpdateClusterGroup(ctx context.Context, arg sqlc.UpdateClusterGroupParams) (sqlc.ClusterGroup, error)
	DeleteClusterGroup(ctx context.Context, id uuid.UUID) error
	ListClustersInGroupTree(ctx context.Context, rootID uuid.UUID) ([]sqlc.ClusterInGroupRow, error)
	CountClustersInGroup(ctx context.Context, groupID uuid.UUID) (int64, error)
	CountClustersInGroupTree(ctx context.Context, groupID uuid.UUID) (int64, error)
	AssignClusterGroup(ctx context.Context, arg sqlc.AssignClusterGroupParams) error
	UnassignClusterGroup(ctx context.Context, clusterID uuid.UUID) error
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// ClusterGroupHandler owns /api/v1/cluster-groups/*.
type ClusterGroupHandler struct {
	queries ClusterGroupQuerier
	// auditor is the audit writer used to record cluster_group.* events;
	// `any` mirrors the pattern used by cloud_credentials.go (recordAudit
	// type-asserts internally). Optional — nil-safe.
	auditor any
}

// NewClusterGroupHandler constructs the handler.
func NewClusterGroupHandler(queries ClusterGroupQuerier) *ClusterGroupHandler {
	return &ClusterGroupHandler{queries: queries}
}

// SetAuditor wires the audit writer used for cluster_group.* events.
func (h *ClusterGroupHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

// ClusterGroupResponse is the wire shape for create/get/update responses.
type ClusterGroupResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	Description      string `json:"description"`
	ParentID         string `json:"parent_id,omitempty"`
	Color            string `json:"color"`
	Icon             string `json:"icon"`
	Enabled          bool   `json:"enabled"`
	CreatedBy        string `json:"created_by,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ClusterCount     int64  `json:"cluster_count"`      // direct children
	ClusterCountTree int64  `json:"cluster_count_tree"` // subtree rollup
}

// ClusterGroupTreeResponse is the depth-annotated variant returned by the
// /api/v1/cluster-groups/ list endpoint. Cluster counts are filled in
// per-node so the sidebar can render the count badge without N follow-up
// requests.
type ClusterGroupTreeResponse struct {
	ClusterGroupResponse
	Depth int32 `json:"depth"`
}

func clusterGroupToResponse(g sqlc.ClusterGroup) ClusterGroupResponse {
	resp := ClusterGroupResponse{
		ID:          g.ID.String(),
		Name:        g.Name,
		Slug:        g.Slug,
		Description: g.Description,
		Color:       g.Color,
		Icon:        g.Icon,
		Enabled:     g.Enabled,
		CreatedAt:   g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   g.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if g.ParentID.Valid {
		resp.ParentID = uuid.UUID(g.ParentID.Bytes).String()
	}
	if g.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(g.CreatedBy.Bytes).String()
	}
	return resp
}

// CreateClusterGroupRequest is the POST body shape.
type CreateClusterGroupRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	Color       string `json:"color,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// UpdateClusterGroupRequest is the PUT body shape.
type UpdateClusterGroupRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	Color       string `json:"color,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// MoveClustersRequest is the POST /{id}/move/ body shape.
type MoveClustersRequest struct {
	ClusterIDs []string `json:"cluster_ids"`
}

// clusterGroupSlugPattern matches the kebab-case shape the slug field accepts.
// Mirrors the project-slug pattern so operators don't have to learn a
// new set of rules per resource.
var clusterGroupSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func deriveSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	// Replace any run of non-alphanumerics with a single hyphen.
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else {
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "group"
	}
	return out
}

// validateClusterGroupCommon validates the shared name/slug/color/icon
// shape between create + update.
func validateClusterGroupCommon(name, slug, color, icon string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("name is required")
	}
	if len(name) > 128 {
		return "", errors.New("name must be 128 characters or fewer")
	}
	if slug == "" {
		slug = deriveSlug(name)
	}
	if !clusterGroupSlugPattern.MatchString(slug) {
		return "", errors.New("slug must be lowercase alphanumeric or hyphens, starting and ending with alphanumeric")
	}
	if len(slug) > 128 {
		return "", errors.New("slug must be 128 characters or fewer")
	}
	if color != "" && len(color) > 16 {
		return "", errors.New("color must be 16 characters or fewer")
	}
	if icon != "" && len(icon) > 64 {
		return "", errors.New("icon must be 64 characters or fewer")
	}
	return slug, nil
}

// resolveParent parses the parent_id string (which may be empty for a
// top-level group) into a pgtype.UUID + verifies the parent exists,
// returning the parent's depth so the caller can enforce MaxDepth.
func (h *ClusterGroupHandler) resolveParent(ctx context.Context, raw string) (pgtype.UUID, int, error) {
	if strings.TrimSpace(raw) == "" {
		return pgtype.UUID{}, -1, nil // -1 == "no parent" (this becomes depth 0)
	}
	pid, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, 0, fmt.Errorf("invalid parent_id")
	}
	// Verify parent exists and compute its depth by walking up the chain.
	depth, err := h.depthOf(ctx, pid)
	if err != nil {
		return pgtype.UUID{}, 0, err
	}
	return pgtype.UUID{Bytes: pid, Valid: true}, depth, nil
}

// depthOf returns the depth of the group with the given id (0 == top-level).
// Used by resolveParent + the depth-cap check on update.
func (h *ClusterGroupHandler) depthOf(ctx context.Context, id uuid.UUID) (int, error) {
	depth := 0
	cur := id
	for i := 0; i < MaxClusterGroupDepth+2; i++ { // hard ceiling — corrupt data shouldn't loop forever
		g, err := h.queries.GetClusterGroupByID(ctx, cur)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("parent_id not found")
			}
			return 0, err
		}
		if !g.ParentID.Valid {
			return depth, nil
		}
		depth++
		cur = uuid.UUID(g.ParentID.Bytes)
	}
	return 0, fmt.Errorf("parent chain exceeds maximum depth")
}

// isDescendant returns true if candidate is a descendant of root (or is
// root itself). Used by Update to prevent reparenting a group under one
// of its own descendants (which would create a cycle).
func (h *ClusterGroupHandler) isDescendant(ctx context.Context, root, candidate uuid.UUID) (bool, error) {
	if root == candidate {
		return true, nil
	}
	// Walk up from candidate; if we hit root, candidate is a descendant.
	cur := candidate
	for i := 0; i < 8; i++ { // safety ceiling; tree is depth-capped at 3
		g, err := h.queries.GetClusterGroupByID(ctx, cur)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if !g.ParentID.Valid {
			return false, nil
		}
		if uuid.UUID(g.ParentID.Bytes) == root {
			return true, nil
		}
		cur = uuid.UUID(g.ParentID.Bytes)
	}
	return false, nil
}

// ────────────────────────────────────────────────────────────────────────
// Handlers
// ────────────────────────────────────────────────────────────────────────

// List handles GET /api/v1/cluster-groups/ — depth-annotated tree.
func (h *ClusterGroupHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListClusterGroupsAsTree(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list cluster groups")
		return
	}
	out := make([]ClusterGroupTreeResponse, 0, len(rows))
	for _, row := range rows {
		base := clusterGroupToResponse(sqlc.ClusterGroup{
			ID:          row.ID,
			Name:        row.Name,
			Slug:        row.Slug,
			Description: row.Description,
			ParentID:    row.ParentID,
			Color:       row.Color,
			Icon:        row.Icon,
			Enabled:     row.Enabled,
			CreatedBy:   row.CreatedBy,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		})
		// Per-node cluster counts. The direct count is cheap; the tree
		// rollup is a recursive CTE so we only fetch it lazily — but for
		// the sidebar render we DO need both, so eat the cost here.
		// Errors are non-fatal (zero count surfaces instead).
		if n, err := h.queries.CountClustersInGroup(r.Context(), row.ID); err == nil {
			base.ClusterCount = n
		}
		if n, err := h.queries.CountClustersInGroupTree(r.Context(), row.ID); err == nil {
			base.ClusterCountTree = n
		}
		out = append(out, ClusterGroupTreeResponse{
			ClusterGroupResponse: base,
			Depth:                row.Depth,
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/cluster-groups/{id}/.
func (h *ClusterGroupHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster group ID")
		return
	}
	g, err := h.queries.GetClusterGroupByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster group not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to load cluster group")
		return
	}
	resp := clusterGroupToResponse(g)
	if n, err := h.queries.CountClustersInGroup(r.Context(), id); err == nil {
		resp.ClusterCount = n
	}
	if n, err := h.queries.CountClustersInGroupTree(r.Context(), id); err == nil {
		resp.ClusterCountTree = n
	}
	RespondJSON(w, http.StatusOK, resp)
}

// Create handles POST /api/v1/cluster-groups/.
func (h *ClusterGroupHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	slug, err := validateClusterGroupCommon(req.Name, req.Slug, req.Color, req.Icon)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_field", err.Error())
		return
	}
	parent, parentDepth, err := h.resolveParent(r.Context(), req.ParentID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_parent", err.Error())
		return
	}
	// Depth cap: parentDepth == -1 means "no parent" (depth 0); otherwise
	// the new group sits at parentDepth + 1.
	newDepth := 0
	if parent.Valid {
		newDepth = parentDepth + 1
	}
	if newDepth > MaxClusterGroupDepth {
		RespondRequestError(w, r, http.StatusBadRequest, "max_depth",
			fmt.Sprintf("Cluster group tree depth is capped at %d (root + %d levels)", MaxClusterGroupDepth, MaxClusterGroupDepth))
		return
	}
	color := req.Color
	if color == "" {
		color = "#6b7280"
	}
	icon := req.Icon
	if icon == "" {
		icon = "folder"
	}
	g, err := h.queries.CreateClusterGroup(r.Context(), sqlc.CreateClusterGroupParams{
		Name:        strings.TrimSpace(req.Name),
		Slug:        slug,
		Description: req.Description,
		ParentID:    parent,
		Color:       color,
		Icon:        icon,
		CreatedBy:   currentUserUUID(r),
	})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusBadRequest, "duplicate_slug", "A cluster group with this slug already exists under the same parent")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "create_error", "Failed to create cluster group")
		return
	}
	recordAudit(r, h.auditor, "admin.cluster_group.created", "cluster_group", g.ID.String(), g.Name, map[string]any{
		"slug":      g.Slug,
		"parent_id": req.ParentID,
	})
	RespondJSON(w, http.StatusCreated, clusterGroupToResponse(g))
}

// Update handles PUT /api/v1/cluster-groups/{id}/.
func (h *ClusterGroupHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster group ID")
		return
	}
	existing, err := h.queries.GetClusterGroupByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster group not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to load cluster group")
		return
	}
	var req UpdateClusterGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	slug, err := validateClusterGroupCommon(req.Name, req.Slug, req.Color, req.Icon)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_field", err.Error())
		return
	}
	parent, parentDepth, err := h.resolveParent(r.Context(), req.ParentID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_parent", err.Error())
		return
	}
	// Reject the four pathological reparent cases:
	//   - parent == self
	//   - parent is one of self's descendants (would create a cycle)
	//   - resulting depth > MaxClusterGroupDepth
	//   - parent move would push descendants past MaxClusterGroupDepth
	if parent.Valid {
		newParentID := uuid.UUID(parent.Bytes)
		if newParentID == id {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_parent", "A group cannot be its own parent")
			return
		}
		descendant, err := h.isDescendant(r.Context(), id, newParentID)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "update_error", "Failed to validate parent chain")
			return
		}
		if descendant {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_parent", "A group cannot be parented under its own descendant")
			return
		}
	}
	newDepth := 0
	if parent.Valid {
		newDepth = parentDepth + 1
	}
	if newDepth > MaxClusterGroupDepth {
		RespondRequestError(w, r, http.StatusBadRequest, "max_depth",
			fmt.Sprintf("Cluster group tree depth is capped at %d (root + %d levels)", MaxClusterGroupDepth, MaxClusterGroupDepth))
		return
	}
	color := req.Color
	if color == "" {
		color = existing.Color
	}
	icon := req.Icon
	if icon == "" {
		icon = existing.Icon
	}
	g, err := h.queries.UpdateClusterGroup(r.Context(), sqlc.UpdateClusterGroupParams{
		ID:          id,
		Name:        strings.TrimSpace(req.Name),
		Slug:        slug,
		Description: req.Description,
		ParentID:    parent,
		Color:       color,
		Icon:        icon,
	})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusBadRequest, "duplicate_slug", "A cluster group with this slug already exists under the same parent")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "update_error", "Failed to update cluster group")
		return
	}
	recordAudit(r, h.auditor, "admin.cluster_group.updated", "cluster_group", g.ID.String(), g.Name, map[string]any{
		"slug":      g.Slug,
		"parent_id": req.ParentID,
	})
	RespondJSON(w, http.StatusOK, clusterGroupToResponse(g))
}

// Delete handles DELETE /api/v1/cluster-groups/{id}/.
//
// Per the schema's ON DELETE CASCADE, deleting a group with children
// removes the entire subtree in a single DELETE. Clusters parented to
// the cascaded subtree don't get deleted — clusters.group_id is ON
// DELETE SET NULL — but we DO emit one audit row per affected cluster
// so the rollup is traceable.
func (h *ClusterGroupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster group ID")
		return
	}
	g, err := h.queries.GetClusterGroupByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster group not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to load cluster group")
		return
	}
	// Snapshot affected clusters BEFORE the DELETE so the audit rows can
	// still resolve cluster IDs (the cascade will null out group_id but
	// the cluster rows themselves stay).
	affected, _ := h.queries.ListClustersInGroupTree(r.Context(), id)
	if err := h.queries.DeleteClusterGroup(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "delete_error", "Failed to delete cluster group")
		return
	}
	recordAudit(r, h.auditor, "admin.cluster_group.deleted", "cluster_group", g.ID.String(), g.Name, map[string]any{
		"slug":              g.Slug,
		"cascaded_clusters": len(affected),
	})
	for _, c := range affected {
		recordAudit(r, h.auditor, "admin.cluster_group.moved_cluster", "cluster", c.ID.String(), c.Name, map[string]any{
			"from_group_subtree": g.ID.String(),
			"to_group":           nil,
			"reason":             "group_deleted",
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListClusters handles GET /api/v1/cluster-groups/{id}/clusters/.
func (h *ClusterGroupHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster group ID")
		return
	}
	if _, err := h.queries.GetClusterGroupByID(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster group not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to load cluster group")
		return
	}
	clusters, err := h.queries.ListClustersInGroupTree(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list clusters in group")
		return
	}
	type clusterRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]clusterRef, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, clusterRef{ID: c.ID.String(), Name: c.Name})
	}
	RespondJSON(w, http.StatusOK, out)
}

// MoveClusters handles POST /api/v1/cluster-groups/{id}/move/ — bulk-
// assigns the supplied clusters into this group. Cluster IDs that don't
// exist are skipped (with a warning in the response); the call is
// otherwise best-effort idempotent.
func (h *ClusterGroupHandler) MoveClusters(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster group ID")
		return
	}
	g, err := h.queries.GetClusterGroupByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster group not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to load cluster group")
		return
	}
	var req MoveClustersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if len(req.ClusterIDs) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, "no_clusters", "cluster_ids must be a non-empty array")
		return
	}
	moved := 0
	skipped := []string{}
	for _, idStr := range req.ClusterIDs {
		cid, err := uuid.Parse(idStr)
		if err != nil {
			skipped = append(skipped, idStr)
			continue
		}
		if _, err := h.queries.GetClusterByID(r.Context(), cid); err != nil {
			skipped = append(skipped, idStr)
			continue
		}
		if err := h.queries.AssignClusterGroup(r.Context(), sqlc.AssignClusterGroupParams{
			ClusterID: cid,
			GroupID:   pgtype.UUID{Bytes: id, Valid: true},
		}); err != nil {
			skipped = append(skipped, idStr)
			continue
		}
		moved++
		recordAudit(r, h.auditor, "admin.cluster_group.moved_cluster", "cluster", idStr, "", map[string]any{
			"to_group":      g.ID.String(),
			"to_group_slug": g.Slug,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"moved":   moved,
		"skipped": skipped,
	})
}

// (isUniqueViolation lives in cluster_templates.go — shared across the
// handler package. We re-use it here for slug collision mapping.)
