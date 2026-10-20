package handler

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func (h *RBACHandler) MyRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil {
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication required")
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
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.LoadError, "Failed to load user bindings")
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
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil {
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication required")
		return
	}
	resource := r.URL.Query().Get("resource")
	verb := r.URL.Query().Get("verb")
	if resource == "" || verb == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Both 'resource' and 'verb' query params are required")
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
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.LoadError, "Failed to load user bindings")
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
