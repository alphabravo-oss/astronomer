package handler

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

func (h *RBACHandler) ListGlobalRoles(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
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
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.GlobalRole, error) { return q.CreateGlobalRole(r.Context(), params) },
		func(role sqlc.GlobalRole) mutationAuditEvent {
			return mutationAuditEvent{
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.GlobalRole, error) { return q.UpdateGlobalRole(r.Context(), params) },
		func(role sqlc.GlobalRole) mutationAuditEvent {
			return mutationAuditEvent{action: "role.update", resourceType: "global_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "global"}}
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
	_, err = executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteGlobalRole(r.Context(), id) },
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "role.delete", resourceType: "global_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "global"}}
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
	offset := int32(queryOffset(r))
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
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ClusterRole, error) { return q.CreateClusterRole(r.Context(), params) },
		func(role sqlc.ClusterRole) mutationAuditEvent {
			return mutationAuditEvent{
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ClusterRole, error) { return q.UpdateClusterRole(r.Context(), params) },
		func(role sqlc.ClusterRole) mutationAuditEvent {
			return mutationAuditEvent{action: "role.update", resourceType: "cluster_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "cluster"}}
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
	_, err = executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteClusterRole(r.Context(), id) },
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "role.delete", resourceType: "cluster_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "cluster"}}
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
	offset := int32(queryOffset(r))
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
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ProjectRole, error) { return q.CreateProjectRole(r.Context(), params) },
		func(role sqlc.ProjectRole) mutationAuditEvent {
			return mutationAuditEvent{
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
	role, err := executeMutation(r, h.runTx,
		func(q RBACMutationTx) (sqlc.ProjectRole, error) { return q.UpdateProjectRole(r.Context(), params) },
		func(role sqlc.ProjectRole) mutationAuditEvent {
			return mutationAuditEvent{action: "role.update", resourceType: "project_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusOK, detail: map[string]any{"scope": "project"}}
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
	_, err = executeMutation(r, h.runTx,
		func(q RBACMutationTx) (struct{}, error) { return struct{}{}, q.DeleteProjectRole(r.Context(), id) },
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{action: "role.delete", resourceType: "project_role", resourceID: id.String(), resourceName: roleName, status: http.StatusNoContent, detail: map[string]any{"scope": "project"}}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return
	}
	h.invalidateAll()
	w.WriteHeader(http.StatusNoContent)
}
