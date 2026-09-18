package handler

import (
	"context"
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
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
	ApplyProjectRoleTemplate(context.Context, sqlc.ApplyProjectRoleTemplateParams) (sqlc.ApplyProjectRoleTemplateRow, error)
	DeleteGlobalRoleBinding(context.Context, uuid.UUID) error
	DeleteClusterRoleBinding(context.Context, uuid.UUID) error
	DeleteProjectRoleBinding(context.Context, uuid.UUID) error
}

type rbacRunTxFunc func(context.Context, func(RBACMutationTx) error) error

type RBACHandler struct {
	queries  RBACQuerier
	engine   *rbac.Engine
	bindings rbac.BindingQuerier
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

// SetAuthorization wires the RBAC engine and binding lookup used by my-roles endpoints.
func (h *RBACHandler) SetAuthorization(engine *rbac.Engine, bindings rbac.BindingQuerier) {
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
	if inv, ok := h.bindings.(rbac.CacheInvalidator); ok {
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
	if inv, ok := h.bindings.(rbac.CacheInvalidator); ok {
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
