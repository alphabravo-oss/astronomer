package handler

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type authorizationSupport struct {
	engine  *rbac.Engine
	querier middleware.RBACQuerier
}

func (a *authorizationSupport) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	a.engine = engine
	a.querier = querier
}

func (a *authorizationSupport) authorizeClusterAction(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, resource rbac.Resource, verb rbac.Verb) bool {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if !a.allowsCluster(bindings, clusterID, resource, verb) {
		RespondError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
		return false
	}
	return true
}

func (a *authorizationSupport) bindingsForContext(ctx context.Context) ([]rbac.RoleBinding, bool, error) {
	if a == nil || a.engine == nil || a.querier == nil {
		return nil, false, nil
	}
	user, ok := middleware.GetAuthenticatedUser(ctx)
	if !ok || user == nil {
		return nil, true, nil
	}
	bindings, err := a.querier.GetUserBindings(ctx, user.ID)
	if err != nil {
		return nil, true, err
	}
	return bindings, true, nil
}

func (a *authorizationSupport) allowsCluster(bindings []rbac.RoleBinding, clusterID uuid.UUID, resource rbac.Resource, verb rbac.Verb) bool {
	if a == nil || a.engine == nil {
		return true
	}
	return a.engine.CheckPermission(bindings, resource, verb, clusterID, uuid.UUID{})
}

func (a *authorizationSupport) authorizeGlobalAction(w http.ResponseWriter, r *http.Request, resource rbac.Resource, verb rbac.Verb) bool {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if !a.allowsGlobal(bindings, resource, verb) {
		RespondError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
		return false
	}
	return true
}

func (a *authorizationSupport) allowsGlobal(bindings []rbac.RoleBinding, resource rbac.Resource, verb rbac.Verb) bool {
	if a == nil || a.engine == nil {
		return true
	}
	return a.engine.CheckPermission(bindings, resource, verb, uuid.UUID{}, uuid.UUID{})
}
