package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type RBACQuerier interface {
	CountGlobalRoles(ctx context.Context) (int64, error)
	CountClusterRoles(ctx context.Context) (int64, error)
	CountProjectRoles(ctx context.Context) (int64, error)
	ListGlobalRoles(ctx context.Context, arg sqlc.ListGlobalRolesParams) ([]sqlc.GlobalRole, error)
	ListClusterRoles(ctx context.Context, arg sqlc.ListClusterRolesParams) ([]sqlc.ClusterRole, error)
	ListProjectRoles(ctx context.Context, arg sqlc.ListProjectRolesParams) ([]sqlc.ProjectRole, error)
	GetGlobalRoleByID(ctx context.Context, id uuid.UUID) (sqlc.GlobalRole, error)
	GetClusterRoleByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRole, error)
	GetProjectRoleByID(ctx context.Context, id uuid.UUID) (sqlc.ProjectRole, error)
	CreateGlobalRole(ctx context.Context, arg sqlc.CreateGlobalRoleParams) (sqlc.GlobalRole, error)
	CreateClusterRole(ctx context.Context, arg sqlc.CreateClusterRoleParams) (sqlc.ClusterRole, error)
	CreateProjectRole(ctx context.Context, arg sqlc.CreateProjectRoleParams) (sqlc.ProjectRole, error)
	UpdateGlobalRole(ctx context.Context, arg sqlc.UpdateGlobalRoleParams) (sqlc.GlobalRole, error)
	UpdateClusterRole(ctx context.Context, arg sqlc.UpdateClusterRoleParams) (sqlc.ClusterRole, error)
	UpdateProjectRole(ctx context.Context, arg sqlc.UpdateProjectRoleParams) (sqlc.ProjectRole, error)
	DeleteGlobalRole(ctx context.Context, id uuid.UUID) error
	DeleteClusterRole(ctx context.Context, id uuid.UUID) error
	DeleteProjectRole(ctx context.Context, id uuid.UUID) error
	ListGlobalRoleBindings(ctx context.Context, arg sqlc.ListGlobalRoleBindingsParams) ([]sqlc.GlobalRoleBinding, error)
	ListClusterRoleBindings(ctx context.Context, arg sqlc.ListClusterRoleBindingsParams) ([]sqlc.ClusterRoleBinding, error)
	ListClusterRoleBindingsByCluster(ctx context.Context, arg sqlc.ListClusterRoleBindingsByClusterParams) ([]sqlc.ClusterRoleBinding, error)
	ListProjectRoleBindings(ctx context.Context, arg sqlc.ListProjectRoleBindingsParams) ([]sqlc.ProjectRoleBinding, error)
	ListProjectRoleBindingsByProject(ctx context.Context, arg sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error)
	GetGlobalRoleBindingByID(ctx context.Context, id uuid.UUID) (sqlc.GlobalRoleBinding, error)
	GetClusterRoleBindingByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRoleBinding, error)
	GetProjectRoleBindingByID(ctx context.Context, id uuid.UUID) (sqlc.ProjectRoleBinding, error)
	CreateGlobalRoleBinding(ctx context.Context, arg sqlc.CreateGlobalRoleBindingParams) (sqlc.GlobalRoleBinding, error)
	CreateClusterRoleBinding(ctx context.Context, arg sqlc.CreateClusterRoleBindingParams) (sqlc.ClusterRoleBinding, error)
	CreateProjectRoleBinding(ctx context.Context, arg sqlc.CreateProjectRoleBindingParams) (sqlc.ProjectRoleBinding, error)
	DeleteGlobalRoleBinding(ctx context.Context, id uuid.UUID) error
	DeleteClusterRoleBinding(ctx context.Context, id uuid.UUID) error
	DeleteProjectRoleBinding(ctx context.Context, id uuid.UUID) error
}

type RBACHandler struct {
	queries  RBACQuerier
	engine   *rbac.Engine
	bindings middleware.RBACQuerier
	// enforcer gates CreateProjectRoleBinding against the per-project
	// max_members_per_project and per-user max_projects_per_user caps
	// (migration 051). Optional; nil disables the check.
	enforcer *quota.Enforcer
	// templates is the pre-loaded role-templates catalog (T1.1).
	// Optional; the ListTemplates / GetTemplate endpoints 503 when nil
	// so a misconfigured deploy notices instead of silently returning
	// an empty list.
	templates *rbac.Catalog
}

func NewRBACHandler(queries RBACQuerier) *RBACHandler {
	return &RBACHandler{queries: queries}
}

// SetAuthorization wires the RBAC engine and binding lookup used by my-roles endpoints.
func (h *RBACHandler) SetAuthorization(engine *rbac.Engine, bindings middleware.RBACQuerier) {
	h.engine = engine
	h.bindings = bindings
}

// SetQuotaEnforcer wires the per-tenant quota enforcer for the RBAC
// handler. Optional; without it CreateProjectRoleBinding skips the
// per-project member / per-user project caps from migration 051.
func (h *RBACHandler) SetQuotaEnforcer(e *quota.Enforcer) {
	if h == nil {
		return
	}
	h.enforcer = e
}

// invalidateUser drops the per-user RBAC cache entry, if the configured
// bindings querier supports it. Mutation handlers call this after a binding
// create/delete so the next authenticated request sees the change instead of
// waiting up to the cache TTL. No-op when caching isn't wired (tests).
func (h *RBACHandler) invalidateUser(userID string) {
	if h == nil || h.bindings == nil || userID == "" {
		return
	}
	if inv, ok := h.bindings.(middleware.RBACCacheInvalidator); ok {
		inv.Invalidate(userID)
	}
}

// invalidateAll dumps the whole RBAC cache. Used after a role mutation: the
// role's rules are denormalised into every cached binding for every user
// holding it, so a targeted invalidation isn't tractable. Cheaper to refill.
func (h *RBACHandler) invalidateAll() {
	if h == nil || h.bindings == nil {
		return
	}
	if inv, ok := h.bindings.(middleware.RBACCacheInvalidator); ok {
		inv.InvalidateAll()
	}
}

// lookupBindingUserIDs is a tiny helper for delete paths where the request
// body doesn't carry user_id — we read the binding by ID first to learn whom
// it affects, then invalidate. Returns empty when the binding doesn't exist
// (already deleted) or when the queries surface lacks the lookup. We do this
// BEFORE issuing the delete so we still have the row to read.
func (h *RBACHandler) lookupGlobalBindingUserID(ctx context.Context, id uuid.UUID) string {
	if h == nil || h.queries == nil {
		return ""
	}
	b, err := h.queries.GetGlobalRoleBindingByID(ctx, id)
	if err != nil || !b.UserID.Valid {
		return ""
	}
	return uuid.UUID(b.UserID.Bytes).String()
}

func (h *RBACHandler) lookupClusterBindingUserID(ctx context.Context, id uuid.UUID) string {
	if h == nil || h.queries == nil {
		return ""
	}
	b, err := h.queries.GetClusterRoleBindingByID(ctx, id)
	if err != nil || !b.UserID.Valid {
		return ""
	}
	return uuid.UUID(b.UserID.Bytes).String()
}

func (h *RBACHandler) lookupProjectBindingUserID(ctx context.Context, id uuid.UUID) string {
	if h == nil || h.queries == nil {
		return ""
	}
	b, err := h.queries.GetProjectRoleBindingByID(ctx, id)
	if err != nil || !b.UserID.Valid {
		return ""
	}
	return uuid.UUID(b.UserID.Bytes).String()
}

type roleRequest struct {
	Name            string          `json:"name"`
	DisplayName     string          `json:"display_name"`
	DisplayNameAlt  string          `json:"displayName"`
	Description     string          `json:"description"`
	Permissions     json.RawMessage `json:"permissions"`
	Rules           json.RawMessage `json:"rules"`
	IsBuiltin       bool            `json:"is_builtin"`
}

// resolveDisplayName returns the display_name from the request, falling back
// to the camelCase displayName key the Next.js frontend currently sends.
func (req *roleRequest) resolveDisplayName() string {
	if req.DisplayName != "" {
		return req.DisplayName
	}
	return req.DisplayNameAlt
}

type roleBindingRequest struct {
	UserID    string `json:"user_id"`
	Group     string `json:"group"`
	RoleID    string `json:"role_id"`
	ClusterID string `json:"cluster_id"`
	ProjectID string `json:"project_id"`
}

func (h *RBACHandler) ListGlobalRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListGlobalRoles(r.Context(), sqlc.ListGlobalRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list global roles")
		return
	}
	total, err := h.queries.CountGlobalRoles(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count global roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateGlobalRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Role name is required")
		return
	}
	role, err := h.queries.CreateGlobalRole(r.Context(), sqlc.CreateGlobalRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
		IsBuiltin:   req.IsBuiltin,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create global role")
		return
	}
	recordAudit(r, h.queries, "role.create", "global_role", role.ID.String(), role.Name, map[string]any{
		"scope":      "global",
		"is_builtin": role.IsBuiltin,
	})
	RespondJSON(w, http.StatusCreated, role)
}

func (h *RBACHandler) GetGlobalRole(w http.ResponseWriter, r *http.Request) {
	role, ok := h.getGlobalRole(w, r)
	if !ok {
		return
	}
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) UpdateGlobalRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	role, err := h.queries.UpdateGlobalRole(r.Context(), sqlc.UpdateGlobalRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update global role")
		return
	}
	// Role rules are denormalised into every cached binding for every user
	// bound to this role. Without a reverse index we can't target the affected
	// users, so dump the whole cache; refill cost is one query per active user.
	h.invalidateAll()
	recordAudit(r, h.queries, "role.update", "global_role", role.ID.String(), role.Name, map[string]any{"scope": "global"})
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteGlobalRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	roleName := ""
	if existing, lookupErr := h.queries.GetGlobalRoleByID(r.Context(), id); lookupErr == nil {
		roleName = existing.Name
	}
	if err := h.queries.DeleteGlobalRole(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Global role not found")
		return
	}
	// ON DELETE CASCADE on global_role_bindings means every binding for this
	// role just vanished too — invalidate broadly.
	h.invalidateAll()
	recordAudit(r, h.queries, "role.delete", "global_role", id.String(), roleName, map[string]any{"scope": "global"})
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListClusterRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListClusterRoles(r.Context(), sqlc.ListClusterRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list cluster roles")
		return
	}
	total, err := h.queries.CountClusterRoles(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count cluster roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateClusterRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Role name is required")
		return
	}
	role, err := h.queries.CreateClusterRole(r.Context(), sqlc.CreateClusterRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
		IsBuiltin:   req.IsBuiltin,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create cluster role")
		return
	}
	recordAudit(r, h.queries, "role.create", "cluster_role", role.ID.String(), role.Name, map[string]any{
		"scope":      "cluster",
		"is_builtin": role.IsBuiltin,
	})
	RespondJSON(w, http.StatusCreated, role)
}

func (h *RBACHandler) GetClusterRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	role, err := h.queries.GetClusterRoleByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster role not found")
		return
	}
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) UpdateClusterRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	role, err := h.queries.UpdateClusterRole(r.Context(), sqlc.UpdateClusterRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update cluster role")
		return
	}
	h.invalidateAll()
	recordAudit(r, h.queries, "role.update", "cluster_role", role.ID.String(), role.Name, map[string]any{"scope": "cluster"})
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteClusterRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	roleName := ""
	if existing, lookupErr := h.queries.GetClusterRoleByID(r.Context(), id); lookupErr == nil {
		roleName = existing.Name
	}
	if err := h.queries.DeleteClusterRole(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster role not found")
		return
	}
	h.invalidateAll()
	recordAudit(r, h.queries, "role.delete", "cluster_role", id.String(), roleName, map[string]any{"scope": "cluster"})
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListProjectRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListProjectRoles(r.Context(), sqlc.ListProjectRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list project roles")
		return
	}
	total, err := h.queries.CountProjectRoles(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count project roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateProjectRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Role name is required")
		return
	}
	role, err := h.queries.CreateProjectRole(r.Context(), sqlc.CreateProjectRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
		IsBuiltin:   req.IsBuiltin,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create project role")
		return
	}
	recordAudit(r, h.queries, "role.create", "project_role", role.ID.String(), role.Name, map[string]any{
		"scope":      "project",
		"is_builtin": role.IsBuiltin,
	})
	RespondJSON(w, http.StatusCreated, role)
}

func (h *RBACHandler) GetProjectRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	role, err := h.queries.GetProjectRoleByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project role not found")
		return
	}
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) UpdateProjectRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	role, err := h.queries.UpdateProjectRole(r.Context(), sqlc.UpdateProjectRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update project role")
		return
	}
	h.invalidateAll()
	recordAudit(r, h.queries, "role.update", "project_role", role.ID.String(), role.Name, map[string]any{"scope": "project"})
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteProjectRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	roleName := ""
	if existing, lookupErr := h.queries.GetProjectRoleByID(r.Context(), id); lookupErr == nil {
		roleName = existing.Name
	}
	if err := h.queries.DeleteProjectRole(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project role not found")
		return
	}
	h.invalidateAll()
	recordAudit(r, h.queries, "role.delete", "project_role", id.String(), roleName, map[string]any{"scope": "project"})
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListGlobalRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListGlobalRoleBindings(r.Context(), sqlc.ListGlobalRoleBindingsParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list global role bindings")
		return
	}
	RespondJSON(w, http.StatusOK, bindingListResponse(items))
}

func (h *RBACHandler) CreateGlobalRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req roleBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	roleID, userID, ok := parseBindingRefs(w, req)
	if !ok {
		return
	}
	binding, err := h.queries.CreateGlobalRoleBinding(r.Context(), sqlc.CreateGlobalRoleBindingParams{
		UserID: userID,
		Group:  req.Group,
		RoleID: roleID,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create global role binding")
		return
	}
	// Targeted invalidation when this is a user-scoped binding. Group-scoped
	// bindings (UserID empty) aren't keyed by user_id in the cache today —
	// group membership isn't expanded in GetUserBindings — so they don't
	// surface in the cache and need no invalidation.
	// TODO(rbac-invalidation): expand on group→users membership when groups
	// become first-class.
	h.invalidateUser(req.UserID)
	recordAudit(r, h.queries, "binding.create", "global_role_binding", binding.ID.String(), "", map[string]any{
		"scope":   "global",
		"role_id": roleID.String(),
		"user_id": req.UserID,
		"group":   req.Group,
	})
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteGlobalRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	// Look up the affected user before deleting so we can invalidate after.
	affectedUserID := h.lookupGlobalBindingUserID(r.Context(), id)
	if err := h.queries.DeleteGlobalRoleBinding(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Global role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	recordAudit(r, h.queries, "binding.delete", "global_role_binding", id.String(), "", map[string]any{"scope": "global"})
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListClusterRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	clusterID := r.URL.Query().Get("cluster_id")
	var (
		items []sqlc.ClusterRoleBinding
		err   error
	)
	if clusterID != "" {
		parsed, parseErr := uuid.Parse(clusterID)
		if parseErr != nil {
			RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
			return
		}
		items, err = h.queries.ListClusterRoleBindingsByCluster(r.Context(), sqlc.ListClusterRoleBindingsByClusterParams{
			ClusterID: parsed,
			Limit:     limit,
			Offset:    offset,
		})
	} else {
		items, err = h.queries.ListClusterRoleBindings(r.Context(), sqlc.ListClusterRoleBindingsParams{Limit: limit, Offset: offset})
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list cluster role bindings")
		return
	}
	RespondJSON(w, http.StatusOK, bindingListResponse(items))
}

func (h *RBACHandler) CreateClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req roleBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	roleID, userID, ok := parseBindingRefs(w, req)
	if !ok {
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Cluster ID is required")
		return
	}
	binding, err := h.queries.CreateClusterRoleBinding(r.Context(), sqlc.CreateClusterRoleBindingParams{
		UserID:    userID,
		Group:     req.Group,
		RoleID:    roleID,
		ClusterID: clusterID,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create cluster role binding")
		return
	}
	h.invalidateUser(req.UserID)
	recordAudit(r, h.queries, "binding.create", "cluster_role_binding", binding.ID.String(), "", map[string]any{
		"scope":      "cluster",
		"role_id":    roleID.String(),
		"user_id":    req.UserID,
		"group":      req.Group,
		"cluster_id": clusterID.String(),
	})
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupClusterBindingUserID(r.Context(), id)
	if err := h.queries.DeleteClusterRoleBinding(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	recordAudit(r, h.queries, "binding.delete", "cluster_role_binding", id.String(), "", map[string]any{"scope": "cluster"})
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListProjectRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	projectID := r.URL.Query().Get("project_id")
	var (
		items []sqlc.ProjectRoleBinding
		err   error
	)
	if projectID != "" {
		parsed, parseErr := uuid.Parse(projectID)
		if parseErr != nil {
			RespondError(w, http.StatusBadRequest, "invalid_project_id", "Invalid project ID")
			return
		}
		items, err = h.queries.ListProjectRoleBindingsByProject(r.Context(), sqlc.ListProjectRoleBindingsByProjectParams{
			ProjectID: parsed,
			Limit:     limit,
			Offset:    offset,
		})
	} else {
		items, err = h.queries.ListProjectRoleBindings(r.Context(), sqlc.ListProjectRoleBindingsParams{Limit: limit, Offset: offset})
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list project role bindings")
		return
	}
	RespondJSON(w, http.StatusOK, bindingListResponse(items))
}

func (h *RBACHandler) CreateProjectRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req roleBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	roleID, userID, ok := parseBindingRefs(w, req)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_project_id", "Project ID is required")
		return
	}

	// Per-tenant quota checks (migration 051). Two caps apply at the
	// "add user-X to project-Y" pivot: the per-project member cap and
	// the per-user project cap. Group bindings (user_id == nil) skip
	// the per-user check since group membership is dynamic and the
	// quota count would be ambiguous.
	if h.enforcer != nil {
		if err := h.enforcer.CheckProjectMemberAdd(r.Context(), projectID); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondError(w, http.StatusInternalServerError, "quota_check_error", "Failed to evaluate project member quota")
			return
		}
		if userID.Valid {
			if err := h.enforcer.CheckUserProjectAdd(r.Context(), uuid.UUID(userID.Bytes)); err != nil {
				if qe, ok := quota.IsQuotaExceeded(err); ok {
					WriteQuotaExceeded(w, qe)
					return
				}
				RespondError(w, http.StatusInternalServerError, "quota_check_error", "Failed to evaluate user project quota")
				return
			}
		}
	}

	binding, err := h.queries.CreateProjectRoleBinding(r.Context(), sqlc.CreateProjectRoleBindingParams{
		UserID:    userID,
		Group:     req.Group,
		RoleID:    roleID,
		ProjectID: projectID,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create project role binding")
		return
	}
	h.invalidateUser(req.UserID)
	recordAudit(r, h.queries, "binding.create", "project_role_binding", binding.ID.String(), "", map[string]any{
		"scope":      "project",
		"role_id":    roleID.String(),
		"user_id":    req.UserID,
		"group":      req.Group,
		"project_id": projectID.String(),
	})
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteProjectRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupProjectBindingUserID(r.Context(), id)
	if err := h.queries.DeleteProjectRoleBinding(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Project role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	recordAudit(r, h.queries, "binding.delete", "project_role_binding", id.String(), "", map[string]any{"scope": "project"})
	w.WriteHeader(http.StatusNoContent)
}

// MyRoles handles GET /api/v1/rbac/my-roles/.
// Returns the current user's effective role bindings.
func (h *RBACHandler) MyRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	if h.bindings == nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"user_id":  user.ID,
			"bindings": []any{},
		})
		return
	}
	bindings, err := h.bindings.GetUserBindings(r.Context(), user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "load_error", "Failed to load user bindings")
		return
	}
	items := make([]map[string]any, 0, len(bindings))
	for _, b := range bindings {
		rules := make([]map[string]any, 0, len(b.RoleRules))
		for _, rule := range b.RoleRules {
			rules = append(rules, map[string]any{
				"resource": rule.Resource,
				"verbs":    rule.Verbs,
			})
		}
		items = append(items, map[string]any{
			"user_id":    b.UserID,
			"group":      b.Group,
			"cluster_id": b.ClusterID,
			"project_id": b.ProjectID,
			"rules":      rules,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"user_id":  user.ID,
		"bindings": items,
	})
}

// CheckMyRole handles GET /api/v1/rbac/my-roles/check/.
// Query params: resource, verb, cluster_id (optional), project_id (optional).
func (h *RBACHandler) CheckMyRole(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	resource := r.URL.Query().Get("resource")
	verb := r.URL.Query().Get("verb")
	if resource == "" || verb == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Both 'resource' and 'verb' query params are required")
		return
	}
	clusterIDStr := r.URL.Query().Get("cluster_id")
	projectIDStr := r.URL.Query().Get("project_id")

	var clusterID, projectID uuid.UUID
	if clusterIDStr != "" {
		if parsed, err := uuid.Parse(clusterIDStr); err == nil {
			clusterID = parsed
		}
	}
	if projectIDStr != "" {
		if parsed, err := uuid.Parse(projectIDStr); err == nil {
			projectID = parsed
		}
	}

	if h.engine == nil || h.bindings == nil {
		// Without an engine, allow by default (matches unrestricted bindingsForContext path).
		RespondJSON(w, http.StatusOK, map[string]any{
			"allowed":    true,
			"resource":   resource,
			"verb":       verb,
			"cluster_id": clusterIDStr,
			"project_id": projectIDStr,
		})
		return
	}

	bindings, err := h.bindings.GetUserBindings(r.Context(), user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "load_error", "Failed to load user bindings")
		return
	}
	allowed := h.engine.CheckPermission(bindings, rbac.Resource(resource), rbac.Verb(verb), clusterID, projectID)
	RespondJSON(w, http.StatusOK, map[string]any{
		"allowed":    allowed,
		"resource":   resource,
		"verb":       verb,
		"cluster_id": clusterIDStr,
		"project_id": projectIDStr,
	})
}

func (h *RBACHandler) getGlobalRole(w http.ResponseWriter, r *http.Request) (sqlc.GlobalRole, bool) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return sqlc.GlobalRole{}, false
	}
	role, err := h.queries.GetGlobalRoleByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Global role not found")
		return sqlc.GlobalRole{}, false
	}
	return role, true
}

func parseUUIDURLParam(w http.ResponseWriter, r *http.Request, param, label string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid "+label+" ID")
		return uuid.UUID{}, false
	}
	return id, true
}

func defaultJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}

func parseBindingRefs(w http.ResponseWriter, req roleBindingRequest) (uuid.UUID, pgtype.UUID, bool) {
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_role_id", "Role ID is required")
		return uuid.UUID{}, pgtype.UUID{}, false
	}
	if req.UserID == "" {
		return roleID, pgtype.UUID{}, true
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_user_id", "Invalid user ID")
		return uuid.UUID{}, pgtype.UUID{}, false
	}
	return roleID, pgtype.UUID{Bytes: userID, Valid: true}, true
}

// bindingListResponse used to wrap items in {"items": ...}, which combined
// with RespondJSON produced a double-wrap ({"data": {"items": [...]}}). The
// frontend (and every other list endpoint) expects {"data": [...]} so we now
// return the slice directly.
func bindingListResponse(items any) any {
	return items
}

// bindingResponse used to wrap the binding in {"binding": ...}, producing a
// triple-wrapped response ({"data": {"binding": {...}}}) once RespondJSON
// added its outer envelope. Returning the binding directly yields the
// expected {"data": {...}} shape.
func bindingResponse(binding any) any {
	return binding
}
