// Package handler — admin CRUD for identity_group_mappings + the
// admin-triggered user re-sync endpoint. All routes are superuser-
// gated inside the handler so the failure mode is a clean 403 rather
// than a generic permission rejection (same pattern as admin_drill.go,
// admin_queues.go).
//
//	GET    /api/v1/admin/group-mappings/           — paginated list
//	POST   /api/v1/admin/group-mappings/           — create
//	GET    /api/v1/admin/group-mappings/{id}/      — get one
//	DELETE /api/v1/admin/group-mappings/{id}/      — delete
//	POST   /api/v1/admin/users/{id}/resync-groups/ — re-run sync against
//	                                                 the user's last
//	                                                 user_idp_groups
//	                                                 snapshot. Useful
//	                                                 after mappings
//	                                                 change without
//	                                                 forcing a logout.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// GroupMappingsQuerier is the narrow DB surface the admin handler
// needs. Implemented by *sqlc.Queries; tests pass a hand-rolled fake.
// The embedded auth.GroupSyncQuerier handles the resync path.
type GroupMappingsQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CreateGroupMapping(ctx context.Context, arg sqlc.CreateGroupMappingParams) (sqlc.IdentityGroupMapping, error)
	GetGroupMappingByID(ctx context.Context, id uuid.UUID) (sqlc.IdentityGroupMapping, error)
	ListGroupMappings(ctx context.Context, arg sqlc.ListGroupMappingsParams) ([]sqlc.IdentityGroupMapping, error)
	CountGroupMappings(ctx context.Context) (int64, error)
	DeleteGroupMapping(ctx context.Context, id uuid.UUID) error
	auth.GroupSyncQuerier
}

// GroupMappingsHandler owns the CRUD + resync endpoints.
type GroupMappingsHandler struct {
	queries   GroupMappingsQuerier
	rbacCache SSORBACInvalidator // optional; nil-safe
}

// NewGroupMappingsHandler builds a usable handler. queries may be nil
// for degenerate installs (no management DB); the handler renders 503
// in that case rather than panicking.
func NewGroupMappingsHandler(queries GroupMappingsQuerier) *GroupMappingsHandler {
	return &GroupMappingsHandler{queries: queries}
}

// SetRBACCacheInvalidator wires the per-user cache hook. The resync
// endpoint calls Invalidate after a successful run so the operator
// who fixed a mapping sees the change without logging the user out.
func (h *GroupMappingsHandler) SetRBACCacheInvalidator(inv SSORBACInvalidator) {
	if h == nil {
		return
	}
	h.rbacCache = inv
}

// GroupMappingRequest is the POST body. All UUIDs come as strings to
// keep the wire shape friendly to the JS frontend (uuid.UUID's JSON
// codec accepts strings, but bare-empty-string for nullable fields is
// what the UI sends).
type GroupMappingRequest struct {
	ConnectorID string `json:"connector_id"` // empty = wildcard
	GroupName   string `json:"group_name"`
	Scope       string `json:"scope"`
	RoleID      string `json:"role_id"`
	ClusterID   string `json:"cluster_id"` // required when scope='cluster'
	ProjectID   string `json:"project_id"` // required when scope='project'
}

// GroupMappingResponse is the wire shape returned by every endpoint.
// Mirrors the DB row but renders nullable UUIDs as empty strings so
// the frontend doesn't have to special-case `null`.
type GroupMappingResponse struct {
	ID          string    `json:"id"`
	ConnectorID string    `json:"connector_id"`
	GroupName   string    `json:"group_name"`
	Scope       string    `json:"scope"`
	RoleID      string    `json:"role_id"`
	ClusterID   string    `json:"cluster_id"`
	ProjectID   string    `json:"project_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toGroupMappingResponse(row sqlc.IdentityGroupMapping) GroupMappingResponse {
	out := GroupMappingResponse{
		ID:        row.ID.String(),
		GroupName: row.GroupName,
		Scope:     row.Scope,
		RoleID:    row.RoleID.String(),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.ConnectorID.Valid {
		out.ConnectorID = uuid.UUID(row.ConnectorID.Bytes).String()
	}
	if row.ClusterID.Valid {
		out.ClusterID = uuid.UUID(row.ClusterID.Bytes).String()
	}
	if row.ProjectID.Valid {
		out.ProjectID = uuid.UUID(row.ProjectID.Bytes).String()
	}
	return out
}

// List handles GET /api/v1/admin/group-mappings/.
func (h *GroupMappingsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	limit, offset := queryLimitOffset(r, 20)
	rows, err := h.queries.ListGroupMappings(r.Context(), sqlc.ListGroupMappingsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to list group mappings")
		return
	}
	total, err := h.queries.CountGroupMappings(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to count group mappings")
		return
	}
	out := make([]GroupMappingResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toGroupMappingResponse(row))
	}
	RespondPaginated(w, r, out, total)
}

// Get handles GET /api/v1/admin/group-mappings/{id}/.
func (h *GroupMappingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid mapping ID")
		return
	}
	row, err := h.queries.GetGroupMappingByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Group mapping not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to get group mapping")
		return
	}
	RespondJSON(w, http.StatusOK, toGroupMappingResponse(row))
}

// Create handles POST /api/v1/admin/group-mappings/. Validates the
// scope/cluster/project triple before passing through; the DB's
// scope_matches CHECK is the second line of defence.
func (h *GroupMappingsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req GroupMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.GroupName == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_group_name", "group_name is required")
		return
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope != "global" && scope != "cluster" && scope != "project" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_scope", "scope must be one of global|cluster|project")
		return
	}
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_role_id", "role_id must be a UUID")
		return
	}

	params := sqlc.CreateGroupMappingParams{
		GroupName: req.GroupName,
		Scope:     scope,
		RoleID:    roleID,
	}
	if req.ConnectorID != "" {
		cid, err := uuid.Parse(req.ConnectorID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_connector_id", "connector_id must be a UUID or empty")
			return
		}
		params.ConnectorID = pgtype.UUID{Bytes: cid, Valid: true}
	}
	switch scope {
	case "cluster":
		if req.ClusterID == "" {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "cluster_id is required for scope=cluster")
			return
		}
		cl, err := uuid.Parse(req.ClusterID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "cluster_id must be a UUID")
			return
		}
		params.ClusterID = pgtype.UUID{Bytes: cl, Valid: true}
		if req.ProjectID != "" {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_scope_params", "project_id must be empty for scope=cluster")
			return
		}
	case "project":
		if req.ProjectID == "" {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_project_id", "project_id is required for scope=project")
			return
		}
		pj, err := uuid.Parse(req.ProjectID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_project_id", "project_id must be a UUID")
			return
		}
		params.ProjectID = pgtype.UUID{Bytes: pj, Valid: true}
		if req.ClusterID != "" {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_scope_params", "cluster_id must be empty for scope=project")
			return
		}
	case "global":
		if req.ClusterID != "" || req.ProjectID != "" {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_scope_params", "cluster_id and project_id must be empty for scope=global")
			return
		}
	}

	row, err := h.queries.CreateGroupMapping(r.Context(), params)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "create_error", "Failed to create group mapping")
		return
	}
	recordAudit(r, h.queries, "admin.group_mapping.created", "group_mapping", row.ID.String(), row.GroupName, map[string]any{
		"connector_id": uuidPgOrEmpty(row.ConnectorID),
		"group_name":   row.GroupName,
		"scope":        row.Scope,
		"role_id":      row.RoleID.String(),
		"cluster_id":   uuidPgOrEmpty(row.ClusterID),
		"project_id":   uuidPgOrEmpty(row.ProjectID),
	})
	RespondJSON(w, http.StatusCreated, toGroupMappingResponse(row))
}

// Delete handles DELETE /api/v1/admin/group-mappings/{id}/.
func (h *GroupMappingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid mapping ID")
		return
	}
	existing, err := h.queries.GetGroupMappingByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Group mapping not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to get group mapping")
		return
	}
	if err := h.queries.DeleteGroupMapping(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "delete_error", "Failed to delete group mapping")
		return
	}
	recordAudit(r, h.queries, "admin.group_mapping.deleted", "group_mapping", existing.ID.String(), existing.GroupName, map[string]any{
		"connector_id": uuidPgOrEmpty(existing.ConnectorID),
		"group_name":   existing.GroupName,
		"scope":        existing.Scope,
		"role_id":      existing.RoleID.String(),
		"cluster_id":   uuidPgOrEmpty(existing.ClusterID),
		"project_id":   uuidPgOrEmpty(existing.ProjectID),
	})
	w.WriteHeader(http.StatusNoContent)
}

// ResyncUser handles POST /api/v1/admin/users/{id}/resync-groups/.
// Re-runs the sync function against the user's last persisted
// user_idp_groups snapshot. Use after editing mappings if you don't
// want to force a logout: the operator updates the mapping, this
// endpoint replays the matched-mappings calculation, and the
// affected role bindings are reconciled.
func (h *GroupMappingsHandler) ResyncUser(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	uid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "User not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to load user")
		return
	}
	snapshot, err := h.queries.GetUserIDPGroups(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusConflict, "no_snapshot",
				"User has no IdP-groups snapshot yet; ask them to log in via SSO once")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "Failed to load user IdP-groups snapshot")
		return
	}

	var groups []string
	if len(snapshot.Groups) > 0 {
		if err := json.Unmarshal(snapshot.Groups, &groups); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "snapshot_parse", "Failed to parse IdP-groups snapshot")
			return
		}
	}

	result, err := auth.SyncUserGroups(r.Context(), h.queries, user.ID, snapshot.ConnectorID, groups, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "sync_error", "Failed to sync user groups")
		return
	}

	for _, added := range result.Added {
		recordAudit(r, h.queries, "auth.group_sync.binding_added", "role_binding", added.BindingID.String(), "",
			map[string]any{
				"user_id":    user.ID.String(),
				"group_name": added.GroupName,
				"role_id":    added.RoleID.String(),
				"scope":      added.Scope,
				"cluster_id": uuidOrEmpty(added.ClusterID),
				"project_id": uuidOrEmpty(added.ProjectID),
				"trigger":    "admin_resync",
			},
		)
	}
	for _, removed := range result.Removed {
		recordAudit(r, h.queries, "auth.group_sync.binding_removed", "role_binding", removed.BindingID.String(), "",
			map[string]any{
				"user_id":    user.ID.String(),
				"role_id":    removed.RoleID.String(),
				"scope":      removed.Scope,
				"cluster_id": uuidOrEmpty(removed.ClusterID),
				"project_id": uuidOrEmpty(removed.ProjectID),
				"trigger":    "admin_resync",
			},
		)
	}
	if (len(result.Added) > 0 || len(result.Removed) > 0) && h.rbacCache != nil {
		h.rbacCache.Invalidate(user.ID.String())
	}

	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"success":       true,
		"user_id":       user.ID.String(),
		"added_count":   len(result.Added),
		"removed_count": len(result.Removed),
		"groups":        groups,
	})
}

// gate enforces superuser-only access. Mirrors the in-handler gate
// pattern used by admin_drill.go and admin_queues.go: 403 for
// unauthenticated or non-superuser callers.
func (h *GroupMappingsHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "store_unavailable", "Group-mapping store not configured")
		return false
	}
	if err := requireSuperuserFromContext(r, h.queries); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return false
	}
	return true
}

// uuidPgOrEmpty stringifies a pgtype.UUID for audit JSON, rendering
// Invalid (NULL) values as "" so the column doesn't show the
// zero-UUID noise.
func uuidPgOrEmpty(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
