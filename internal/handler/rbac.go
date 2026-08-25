package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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

// RBACMutationTx is the transaction-bound write surface for privilege
// changes. Production passes sqlc.New(tx), making the role/binding mutation
// and its sanitized audit intent one commit decision.
type RBACMutationTx interface {
	audit.OutboxQuerier
	CreateGlobalRole(context.Context, sqlc.CreateGlobalRoleParams) (sqlc.GlobalRole, error)
	CreateClusterRole(context.Context, sqlc.CreateClusterRoleParams) (sqlc.ClusterRole, error)
	CreateProjectRole(context.Context, sqlc.CreateProjectRoleParams) (sqlc.ProjectRole, error)
	UpdateGlobalRole(context.Context, sqlc.UpdateGlobalRoleParams) (sqlc.GlobalRole, error)
	UpdateClusterRole(context.Context, sqlc.UpdateClusterRoleParams) (sqlc.ClusterRole, error)
	UpdateProjectRole(context.Context, sqlc.UpdateProjectRoleParams) (sqlc.ProjectRole, error)
	DeleteGlobalRole(context.Context, uuid.UUID) error
	DeleteClusterRole(context.Context, uuid.UUID) error
	DeleteProjectRole(context.Context, uuid.UUID) error
	CreateGlobalRoleBinding(context.Context, sqlc.CreateGlobalRoleBindingParams) (sqlc.GlobalRoleBinding, error)
	CreateClusterRoleBinding(context.Context, sqlc.CreateClusterRoleBindingParams) (sqlc.ClusterRoleBinding, error)
	CreateProjectRoleBinding(context.Context, sqlc.CreateProjectRoleBindingParams) (sqlc.ProjectRoleBinding, error)
	DeleteGlobalRoleBinding(context.Context, uuid.UUID) error
	DeleteClusterRoleBinding(context.Context, uuid.UUID) error
	DeleteProjectRoleBinding(context.Context, uuid.UUID) error
}

type rbacRunTxFunc func(context.Context, func(RBACMutationTx) error) error

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
	runTx     rbacRunTxFunc
}

func NewRBACHandler(queries RBACQuerier) *RBACHandler {
	return &RBACHandler{queries: queries}
}

// SetRunTx enables the production atomic RBAC-mutation + audit-intent path.
func (h *RBACHandler) SetRunTx(runTx rbacRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *RBACHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

type rbacAuditEvent struct {
	action       string
	resourceType string
	resourceID   string
	resourceName string
	status       int
	detail       map[string]any
}

// executeRBACMutation keeps the compatibility fallback used by narrow unit
// fakes, while production always executes mutate and RecordOutbox through the
// same transaction-bound sqlc query object.
func executeRBACMutation[T any](
	r *http.Request,
	h *RBACHandler,
	mutate func(RBACMutationTx) (T, error),
	fallback func() (T, error),
	describe func(T) rbacAuditEvent,
) (T, error) {
	var zero T
	if h == nil {
		return zero, fmt.Errorf("rbac handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q RBACMutationTx) error {
			var mutationErr error
			result, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return result, err
	}

	result, err := fallback()
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.queries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
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

// openapi:request RBACRoleRequest
type roleRequest struct {
	Name           string          `json:"name" validate:"required"`
	DisplayName    string          `json:"display_name"`
	DisplayNameAlt string          `json:"displayName"`
	Description    string          `json:"description"`
	Permissions    json.RawMessage `json:"permissions"`
	Rules          json.RawMessage `json:"rules"`
	// Scope is path-implied and ignored, but remains accepted so clients built
	// from the original v1 schema do not fail during the sunset window.
	Scope string `json:"scope,omitempty"`
	// No IsBuiltin. is_builtin is migration-owned (see rejectBuiltinRoleWrite):
	// it now freezes a row against update AND delete, so honouring it from a
	// request body would let anyone holding rbac:create mint a role that not
	// even a superuser can edit or remove through the API. An `is_builtin` key
	// in the body is accepted and ignored — the decoder does not reject unknown
	// fields, so existing clients that echo it back keep working.
}

// resolveDisplayName returns the display_name from the request, falling back
// to the camelCase displayName key the Next.js frontend currently sends.
func (req *roleRequest) resolveDisplayName() string {
	if req.DisplayName != "" {
		return req.DisplayName
	}
	return req.DisplayNameAlt
}

// openapi:request RBACBindingRequest
type globalRoleBindingRequest struct {
	UserID string `json:"user_id"`
	Group  string `json:"group"`
	RoleID string `json:"role_id"`
}

// openapi:request RBACClusterBindingRequest
type clusterRoleBindingRequest struct {
	UserID    string `json:"user_id"`
	Group     string `json:"group"`
	RoleID    string `json:"role_id"`
	ClusterID string `json:"cluster_id"`
	// Namespace optionally narrows a cluster binding to one Kubernetes
	// namespace. Empty means the binding applies to the full cluster scope.
	Namespace string `json:"namespace"`
}

// openapi:request RBACProjectBindingRequest
type projectRoleBindingRequest struct {
	UserID    string `json:"user_id"`
	Group     string `json:"group"`
	RoleID    string `json:"role_id"`
	ProjectID string `json:"project_id"`
}

func (h *RBACHandler) ListGlobalRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListGlobalRoles(r.Context(), sqlc.ListGlobalRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list global roles")
		return
	}
	total, err := h.queries.CountGlobalRoles(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count global roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateGlobalRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if rejectGlobalCRDGrants(w, r, defaultJSON(req.Rules)) {
		return
	}
	params := sqlc.CreateGlobalRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.GlobalRole, error) { return q.CreateGlobalRole(r.Context(), params) },
		func() (sqlc.GlobalRole, error) { return h.queries.CreateGlobalRole(r.Context(), params) },
		func(role sqlc.GlobalRole) rbacAuditEvent {
			return rbacAuditEvent{
				action: "role.create", resourceType: "global_role", resourceID: role.ID.String(), resourceName: role.Name,
				status: http.StatusCreated, detail: map[string]any{"scope": "global", "is_builtin": role.IsBuiltin},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create global role")
		return
	}
	w.Header().Set("Location", "/api/v1/rbac/global-roles/"+role.ID.String()+"/")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if rejectGlobalCRDGrants(w, r, defaultJSON(req.Rules)) {
		return
	}
	if !h.guardGlobalRoleRules(w, r, id, defaultJSON(req.Rules)) {
		return
	}
	params := sqlc.UpdateGlobalRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.GlobalRole, error) { return q.UpdateGlobalRole(r.Context(), params) },
		func() (sqlc.GlobalRole, error) { return h.queries.UpdateGlobalRole(r.Context(), params) },
		func(role sqlc.GlobalRole) rbacAuditEvent {
			return rbacAuditEvent{action: "role.update", resourceType: "global_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "global"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update global role")
		return
	}
	// Role rules are denormalised into every cached binding for every user
	// bound to this role. Without a reverse index we can't target the affected
	// users, so dump the whole cache; refill cost is one query per active user.
	h.invalidateAll()
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteGlobalRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	// Read the row first: the built-in check needs it, so a failed lookup is
	// now a 404 instead of a delete-anyway (a DELETE that matches no row is not
	// an error in Postgres, so the old path answered 204 for an unknown ID).
	existing, err := h.queries.GetGlobalRoleByID(r.Context(), id)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return
	}
	roleName := existing.Name
	_, err = executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteGlobalRole(r.Context(), id) },
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteGlobalRole(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "role.delete", resourceType: "global_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "global"}}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return
	}
	// ON DELETE CASCADE on global_role_bindings means every binding for this
	// role just vanished too — invalidate broadly.
	h.invalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListClusterRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListClusterRoles(r.Context(), sqlc.ListClusterRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster roles")
		return
	}
	total, err := h.queries.CountClusterRoles(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count cluster roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateClusterRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	params := sqlc.CreateClusterRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ClusterRole, error) { return q.CreateClusterRole(r.Context(), params) },
		func() (sqlc.ClusterRole, error) { return h.queries.CreateClusterRole(r.Context(), params) },
		func(role sqlc.ClusterRole) rbacAuditEvent {
			return rbacAuditEvent{
				action: "role.create", resourceType: "cluster_role", resourceID: role.ID.String(), resourceName: role.Name,
				status: http.StatusCreated, detail: map[string]any{"scope": "cluster", "is_builtin": role.IsBuiltin},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster role")
		return
	}
	w.Header().Set("Location", "/api/v1/rbac/cluster-roles/"+role.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, role)
}

func (h *RBACHandler) GetClusterRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	role, err := h.queries.GetClusterRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if !h.guardClusterRoleRules(w, r, id, defaultJSON(req.Rules)) {
		return
	}
	params := sqlc.UpdateClusterRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ClusterRole, error) { return q.UpdateClusterRole(r.Context(), params) },
		func() (sqlc.ClusterRole, error) { return h.queries.UpdateClusterRole(r.Context(), params) },
		func(role sqlc.ClusterRole) rbacAuditEvent {
			return rbacAuditEvent{action: "role.update", resourceType: "cluster_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "cluster"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update cluster role")
		return
	}
	h.invalidateAll()
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteClusterRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	existing, err := h.queries.GetClusterRoleByID(r.Context(), id)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return
	}
	roleName := existing.Name
	_, err = executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteClusterRole(r.Context(), id) },
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteClusterRole(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "role.delete", resourceType: "cluster_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "cluster"}}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return
	}
	h.invalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListProjectRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListProjectRoles(r.Context(), sqlc.ListProjectRolesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project roles")
		return
	}
	total, err := h.queries.CountProjectRoles(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project roles")
		return
	}
	RespondPaginated(w, r, items, total)
}

func (h *RBACHandler) CreateProjectRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	params := sqlc.CreateProjectRoleParams{
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ProjectRole, error) { return q.CreateProjectRole(r.Context(), params) },
		func() (sqlc.ProjectRole, error) { return h.queries.CreateProjectRole(r.Context(), params) },
		func(role sqlc.ProjectRole) rbacAuditEvent {
			return rbacAuditEvent{
				action: "role.create", resourceType: "project_role", resourceID: role.ID.String(), resourceName: role.Name,
				status: http.StatusCreated, detail: map[string]any{"scope": "project", "is_builtin": role.IsBuiltin},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create project role")
		return
	}
	w.Header().Set("Location", "/api/v1/rbac/project-roles/"+role.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, role)
}

func (h *RBACHandler) GetProjectRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	role, err := h.queries.GetProjectRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if !h.guardProjectRoleRules(w, r, id, defaultJSON(req.Rules)) {
		return
	}
	params := sqlc.UpdateProjectRoleParams{
		ID:          id,
		Name:        req.Name,
		DisplayName: req.resolveDisplayName(),
		Description: req.Description,
		Permissions: defaultJSON(req.Permissions),
		Rules:       defaultJSON(req.Rules),
	}
	role, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ProjectRole, error) { return q.UpdateProjectRole(r.Context(), params) },
		func() (sqlc.ProjectRole, error) { return h.queries.UpdateProjectRole(r.Context(), params) },
		func(role sqlc.ProjectRole) rbacAuditEvent {
			return rbacAuditEvent{action: "role.update", resourceType: "project_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "project"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update project role")
		return
	}
	h.invalidateAll()
	RespondJSON(w, http.StatusOK, role)
}

func (h *RBACHandler) DeleteProjectRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return
	}
	existing, err := h.queries.GetProjectRoleByID(r.Context(), id)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return
	}
	roleName := existing.Name
	_, err = executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteProjectRole(r.Context(), id) },
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteProjectRole(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "role.delete", resourceType: "project_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "project"}}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return
	}
	h.invalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListGlobalRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	items, err := h.queries.ListGlobalRoleBindings(r.Context(), sqlc.ListGlobalRoleBindingsParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list global role bindings")
		return
	}
	RespondList(w, bindingListResponse(items), NewPaginationFromPage(int(limit), int(offset), len(items)))
}

func (h *RBACHandler) CreateGlobalRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req globalRoleBindingRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if rejectGroupBinding(w, r, req.UserID, req.Group) {
		return
	}
	roleID, userID, ok := parseBindingRefs(w, r, req.RoleID, req.UserID)
	if !ok {
		return
	}
	// Privilege-escalation guard: the caller may only grant a role whose every
	// rule the caller already holds at this (global) scope. Superusers bypass.
	if !h.guardGlobalBinding(w, r, roleID) {
		return
	}
	params := sqlc.CreateGlobalRoleBindingParams{
		UserID: userID,
		Group:  req.Group,
		RoleID: roleID,
	}
	binding, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.GlobalRoleBinding, error) {
			return q.CreateGlobalRoleBinding(r.Context(), params)
		},
		func() (sqlc.GlobalRoleBinding, error) { return h.queries.CreateGlobalRoleBinding(r.Context(), params) },
		func(binding sqlc.GlobalRoleBinding) rbacAuditEvent {
			return rbacAuditEvent{
				action: "binding.create", resourceType: "global_role_binding", resourceID: binding.ID.String(), status: http.StatusCreated,
				detail: map[string]any{"scope": "global", "role_id": roleID.String(), "user_id": req.UserID, "group": req.Group},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create global role binding")
		return
	}
	// Targeted invalidation when this is a user-scoped binding. Group-scoped
	// bindings (UserID empty) aren't keyed by user_id in the cache today —
	// group membership isn't expanded in GetUserBindings — so they don't
	// surface in the cache and need no invalidation.
	// TODO(rbac-invalidation): expand on group→users membership when groups
	// become first-class.
	h.invalidateUser(req.UserID)
	w.Header().Set("Location", "/api/v1/rbac/global-role-bindings/"+binding.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteGlobalRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	// Look up the affected user before deleting so we can invalidate after.
	affectedUserID := h.lookupGlobalBindingUserID(r.Context(), id)
	_, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteGlobalRoleBinding(r.Context(), id)
		},
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteGlobalRoleBinding(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "binding.delete", resourceType: "global_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "global"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Global role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListClusterRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	clusterID := r.URL.Query().Get("cluster_id")
	var (
		items []sqlc.ClusterRoleBinding
		err   error
	)
	if clusterID != "" {
		parsed, parseErr := uuid.Parse(clusterID)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster role bindings")
		return
	}
	RespondList(w, bindingListResponse(items), NewPaginationFromPage(int(limit), int(offset), len(items)))
}

func (h *RBACHandler) CreateClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req clusterRoleBindingRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if rejectGroupBinding(w, r, req.UserID, req.Group) {
		return
	}
	roleID, userID, ok := parseBindingRefs(w, r, req.RoleID, req.UserID)
	if !ok {
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Cluster ID is required")
		return
	}
	// A non-empty namespace scopes the binding to a single Kubernetes namespace
	// (empty == cluster-wide). Validate it as a DNS-1123 label before persisting,
	// mirroring the preview handler (rbac_effective.go).
	if req.Namespace != "" {
		if errs := k8svalidation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
				"namespace must be a valid Kubernetes namespace")
			return
		}
	}
	// Privilege-escalation guard: the caller may only grant a role whose every
	// rule the caller already holds at this cluster (and namespace) scope.
	if !h.guardClusterBinding(w, r, roleID, clusterID, req.Namespace) {
		return
	}
	params := sqlc.CreateClusterRoleBindingParams{
		UserID:    userID,
		Group:     req.Group,
		RoleID:    roleID,
		ClusterID: clusterID,
		Namespace: req.Namespace,
	}
	binding, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ClusterRoleBinding, error) {
			return q.CreateClusterRoleBinding(r.Context(), params)
		},
		func() (sqlc.ClusterRoleBinding, error) {
			return h.queries.CreateClusterRoleBinding(r.Context(), params)
		},
		func(binding sqlc.ClusterRoleBinding) rbacAuditEvent {
			return rbacAuditEvent{
				action: "binding.create", resourceType: "cluster_role_binding", resourceID: binding.ID.String(), status: http.StatusCreated,
				detail: map[string]any{
					"scope": "cluster", "role_id": roleID.String(), "user_id": req.UserID, "group": req.Group,
					"cluster_id": clusterID.String(), "namespace": req.Namespace,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster role binding")
		return
	}
	h.invalidateUser(req.UserID)
	w.Header().Set("Location", "/api/v1/rbac/cluster-role-bindings/"+binding.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupClusterBindingUserID(r.Context(), id)
	_, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteClusterRoleBinding(r.Context(), id)
		},
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteClusterRoleBinding(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "binding.delete", resourceType: "cluster_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "cluster"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Cluster role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListProjectRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	projectID := r.URL.Query().Get("project_id")
	var (
		items []sqlc.ProjectRoleBinding
		err   error
	)
	if projectID != "" {
		parsed, parseErr := uuid.Parse(projectID)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project role bindings")
		return
	}
	RespondList(w, bindingListResponse(items), NewPaginationFromPage(int(limit), int(offset), len(items)))
}

func (h *RBACHandler) CreateProjectRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req projectRoleBindingRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if rejectGroupBinding(w, r, req.UserID, req.Group) {
		return
	}
	roleID, userID, ok := parseBindingRefs(w, r, req.RoleID, req.UserID)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Project ID is required")
		return
	}

	// Privilege-escalation guard: the caller may only grant a role whose every
	// rule the caller already holds at this project scope. Superusers bypass.
	if !h.guardProjectBinding(w, r, roleID, projectID) {
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
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate project member quota")
			return
		}
		if userID.Valid {
			if err := h.enforcer.CheckUserProjectAdd(r.Context(), uuid.UUID(userID.Bytes)); err != nil {
				if qe, ok := quota.IsQuotaExceeded(err); ok {
					WriteQuotaExceeded(w, qe)
					return
				}
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate user project quota")
				return
			}
		}
	}

	params := sqlc.CreateProjectRoleBindingParams{
		UserID:    userID,
		Group:     req.Group,
		RoleID:    roleID,
		ProjectID: projectID,
	}
	binding, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (sqlc.ProjectRoleBinding, error) {
			return q.CreateProjectRoleBinding(r.Context(), params)
		},
		func() (sqlc.ProjectRoleBinding, error) {
			return h.queries.CreateProjectRoleBinding(r.Context(), params)
		},
		func(binding sqlc.ProjectRoleBinding) rbacAuditEvent {
			return rbacAuditEvent{
				action: "binding.create", resourceType: "project_role_binding", resourceID: binding.ID.String(), status: http.StatusCreated,
				detail: map[string]any{
					"scope": "project", "role_id": roleID.String(), "user_id": req.UserID,
					"group": req.Group, "project_id": projectID.String(),
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create project role binding")
		return
	}
	h.invalidateUser(req.UserID)
	w.Header().Set("Location", "/api/v1/rbac/project-role-bindings/"+binding.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *RBACHandler) DeleteProjectRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupProjectBindingUserID(r.Context(), id)
	_, err := executeRBACMutation(r, h,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteProjectRoleBinding(r.Context(), id)
		},
		func() (struct{}, error) { return struct{}{}, h.queries.DeleteProjectRoleBinding(r.Context(), id) },
		func(struct{}) rbacAuditEvent {
			return rbacAuditEvent{action: "binding.delete", resourceType: "project_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "project"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Project role binding not found")
		return
	}
	h.invalidateUser(affectedUserID)
	w.WriteHeader(http.StatusNoContent)
}

// MyRoles handles GET /api/v1/rbac/my-roles/.
// Returns the current user's effective role bindings.
func (h *RBACHandler) MyRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load user bindings")
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	resource := r.URL.Query().Get("resource")
	verb := r.URL.Query().Get("verb")
	if resource == "" || verb == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Both 'resource' and 'verb' query params are required")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load user bindings")
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
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return sqlc.GlobalRole{}, false
	}
	return role, true
}

// guardGlobalBinding enforces the privilege-escalation check for a global role
// binding: it loads the target role and rejects unless the caller already holds
// every rule at global scope (or is a superuser). See enforceNoEscalation.
func (h *RBACHandler) guardGlobalBinding(w http.ResponseWriter, r *http.Request, roleID uuid.UUID) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetGlobalRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardClusterBinding is the cluster-scoped counterpart of guardGlobalBinding.
// The caller must hold each target rule at the given cluster (and namespace, if
// the binding is namespace-narrowed) scope.
func (h *RBACHandler) guardClusterBinding(w http.ResponseWriter, r *http.Request, roleID, clusterID uuid.UUID, namespace string) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetClusterRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, clusterID, uuid.UUID{}, namespace)
}

// guardProjectBinding is the project-scoped counterpart of guardGlobalBinding.
func (h *RBACHandler) guardProjectBinding(w http.ResponseWriter, r *http.Request, roleID, projectID uuid.UUID) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetProjectRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, uuid.UUID{}, projectID, "")
}

// guardGlobalRoleRules is the role-definition counterpart of
// guardGlobalBinding: it gates a write to the role itself rather than to a
// binding. Without it the binding guard is trivially bypassed — instead of
// granting themselves a stronger role, a caller holding rbac:update rewrites
// the rules of a role they are already bound to, and h.invalidateAll() makes it
// effective on the next request. rules is the incoming rule set.
func (h *RBACHandler) guardGlobalRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetGlobalRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	// Global scope (all-zero IDs): a role definition is not attached to a
	// cluster or project — it can be bound anywhere — so the caller must hold
	// each rule everywhere. It is also the scope the route middleware itself
	// resolves for these paths, which carry no cluster_id/project_id param.
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardClusterRoleRules is the cluster-role-definition counterpart.
func (h *RBACHandler) guardClusterRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetClusterRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardProjectRoleRules is the project-role-definition counterpart.
func (h *RBACHandler) guardProjectRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetProjectRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// rejectBuiltinRoleWrite freezes the seeded built-in roles against update and
// delete. Returns true (and writes the 403) when the write must be refused.
//
// is_builtin is MIGRATION-OWNED and must stay that way: this freeze is
// permanent and unrecoverable through the API, so the create handlers
// deliberately do not read the flag off the request body (see roleRequest).
// Otherwise any caller holding rbac:create could plant roles nobody — superuser
// included — can ever edit or delete.
//
// Rancher's equivalent (pkg/api/norman/customization/globalrole/validator.go)
// strips the mutable fields from a PUT to a builtin instead of failing, because
// one field (newUserDefault) legitimately stays writable there. Our role rows
// have no such field — name, display_name, description, permissions and rules
// are the whole record — so stripping would turn every PUT into a 200 that
// changed nothing while recording a role.update audit event that never
// happened. We reject instead, so the operator learns the truth. 403 rather
// than 409: the refusal is permanent and independent of the request body, and
// it matches how the rest of this surface reports "not allowed". Built-ins are
// reconciled by migrations (see 141) so an accepted edit would not survive the
// next upgrade anyway.
//
// Deliberately NOT here: an escalation check on delete. Deleting a role hands
// the caller no permission, and requiring the caller to hold the deleted role's
// rules would take an ability away from a role that ships today — the seeded
// 'RBAC Administrator' (migration 032: rbac:* plus users:read/list) could no
// longer clean up any operator-authored role carrying permissions beyond its
// own, with no migration able to give that back. Kubernetes draws the same line:
// its escalate check applies only to writes that create rules. The lockout half
// of the finding is closed by the built-in freeze above — the cascade from
// global_role_bindings can no longer take out the seeded 'Administrator' row.
func rejectBuiltinRoleWrite(w http.ResponseWriter, r *http.Request, isBuiltin bool) bool {
	if !isBuiltin {
		return false
	}
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
		"Built-in roles cannot be modified or deleted")
	return true
}

// enforceNoEscalation implements Kubernetes' "you cannot grant permissions you
// do not hold" escalate/bind guard. For every (resource, verb) in the target
// role's rules, the CALLER must already hold that permission at the binding's
// scope (clusterID/projectID/namespace identify the scope; all zero means
// global). Superusers bypass via the engine's IsSuperuser short-circuit. It
// writes a 403 and returns false on denial; true means the binding may proceed.
//
// Wildcard semantics come straight from the engine: a caller holding only
// rbac:* is NOT allowed to grant a role carrying resource "*" — the engine only
// matches a request for resource "*" against a caller rule whose resource is
// itself "*". So self-escalation to full admin requires the caller to already
// be full admin.
//
// Callers gate this behind an engine/bindings nil-check (see the guard*Binding
// wrappers), preserving the handler's optional-authorization contract used by
// unit tests and pre-authorization deployments.
func (h *RBACHandler) enforceNoEscalation(w http.ResponseWriter, r *http.Request, rawRules json.RawMessage, clusterID, projectID uuid.UUID, namespace string) bool {
	targetRules, err := decodeRoleRules(rawRules)
	if err != nil {
		// A role whose rules we cannot parse cannot be safely granted; fail closed.
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to decode target role rules")
		return false
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to grant this role")
		return false
	}
	callerBindings, err := h.bindings.GetUserBindings(r.Context(), user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load caller bindings")
		return false
	}
	for _, rule := range targetRules {
		if rule.IsCRDGrant() {
			for _, group := range rule.CRDAPIGroups() {
				if escalationGroups[strings.ToLower(strings.TrimSpace(group))] {
					RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
						"CRD grants cannot target privilege-escalation API groups")
					return false
				}
				for _, resource := range rule.ResourceNames() {
					charged := nativeRuleChargedResources(group, resource)
					for _, verb := range rbac.NormalizeNativeVerbs(rule.Verbs) {
						for _, mapped := range charged {
							if !h.engine.CheckPermission(callerBindings, mapped, rbac.Verb(verb), clusterID, projectID, namespace) {
								RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot grant a role that includes permissions you do not hold")
								return false
							}
						}
					}
				}
			}
			continue
		}
		for _, verb := range rule.Verbs {
			if !h.engine.CheckPermission(callerBindings, rbac.Resource(rule.Resource), rbac.Verb(verb), clusterID, projectID, namespace) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot grant a role that includes permissions you do not hold")
				return false
			}
		}
	}
	return true
}

func rejectGlobalCRDGrants(w http.ResponseWriter, r *http.Request, raw json.RawMessage) bool {
	rules, err := decodeRoleRules(raw)
	if err != nil {
		return false
	}
	for _, rule := range rules {
		if rule.IsCRDGrant() {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody,
				"CRD grants belong on cluster or project roles, not global roles")
			return true
		}
	}
	return false
}

// decodeRoleRules parses a role's rules JSONB into the RBAC rule slice. Empty
// input yields no rules (a role that grants nothing).
func decodeRoleRules(raw json.RawMessage) ([]rbac.Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rules []rbac.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseUUIDURLParam(w http.ResponseWriter, r *http.Request, param, label string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid "+label+" ID")
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

// rejectGroupBinding blocks the manual role-binding API from creating
// group-scoped (or user-less) bindings. Group-scoped bindings are stored
// and indexed but never expanded at authorization time — GetUserBindings /
// ListUserBindingsWithRoles resolve strictly by user_id, so a binding with
// no user_id (or a "group" set) silently grants nothing. Group membership
// is driven by identity group mappings, not this endpoint. We fail closed
// with a 400 so operators notice instead of trusting a no-op grant.
//
// Returns true (and writes the 400) when the request must be rejected;
// false means the binding carries a concrete user_id and may proceed.
func rejectGroupBinding(w http.ResponseWriter, r *http.Request, userID, group string) bool {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(group) != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			"group bindings are managed via identity group mappings, not the manual binding API")
		return true
	}
	return false
}

func parseBindingRefs(w http.ResponseWriter, r *http.Request, roleIDValue, userIDValue string) (uuid.UUID, pgtype.UUID, bool) {
	roleID, err := uuid.Parse(roleIDValue)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Role ID is required")
		return uuid.UUID{}, pgtype.UUID{}, false
	}
	if userIDValue == "" {
		return roleID, pgtype.UUID{}, true
	}
	userID, err := uuid.Parse(userIDValue)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
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
