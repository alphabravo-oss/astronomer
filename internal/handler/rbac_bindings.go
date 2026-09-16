package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/google/uuid"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

func (h *RBACHandler) ListGlobalRoleBindings(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	items, err := h.queries.ListGlobalRoleBindings(r.Context(), sqlc.ListGlobalRoleBindingsParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list global role bindings")
		return
	}
	paging.Write(w, items, paging.FromPage(int(limit), int(offset), len(items)))
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
	binding, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.GlobalRoleBinding, error) {
			return q.CreateGlobalRoleBinding(r.Context(), params)
		},
		func(binding sqlc.GlobalRoleBinding) mutationAuditEvent {
			return mutationAuditEvent{
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
	RespondJSON(w, http.StatusCreated, binding)
}

func (h *RBACHandler) DeleteGlobalRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	// Look up the affected user before deleting so we can invalidate after.
	affectedUserID := h.lookupGlobalBindingUserID(r.Context(), id)
	_, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteGlobalRoleBinding(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "binding.delete", resourceType: "global_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "global"}}
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
	offset := int32(queryOffset(r))
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
	paging.Write(w, items, paging.FromPage(int(limit), int(offset), len(items)))
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
	binding, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ClusterRoleBinding, error) {
			return q.CreateClusterRoleBinding(r.Context(), params)
		},
		func(binding sqlc.ClusterRoleBinding) mutationAuditEvent {
			return mutationAuditEvent{
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
	RespondJSON(w, http.StatusCreated, binding)
}

func (h *RBACHandler) DeleteClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupClusterBindingUserID(r.Context(), id)
	_, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteClusterRoleBinding(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "binding.delete", resourceType: "cluster_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "cluster"}}
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
	offset := int32(queryOffset(r))
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
	paging.Write(w, items, paging.FromPage(int(limit), int(offset), len(items)))
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
	binding, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ProjectRoleBinding, error) {
			return q.CreateProjectRoleBinding(r.Context(), params)
		},
		func(binding sqlc.ProjectRoleBinding) mutationAuditEvent {
			return mutationAuditEvent{
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
	RespondJSON(w, http.StatusCreated, binding)
}

func (h *RBACHandler) DeleteProjectRoleBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDURLParam(w, r, "id", "binding")
	if !ok {
		return
	}
	affectedUserID := h.lookupProjectBindingUserID(r.Context(), id)
	_, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteProjectRoleBinding(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "binding.delete", resourceType: "project_role_binding", resourceID: id.String(), status: http.StatusNoContent, detail: map[string]any{"scope": "project"}}
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
