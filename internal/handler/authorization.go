package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type authorizationSupport struct {
	engine  *rbac.Engine
	querier middleware.RBACQuerier
}

var errAuthorizationNotConfigured = errors.New("authorization support is not configured")

type UserByIDQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

type userByIDQuerier = UserByIDQuerier

var (
	errAuthenticatedUserMissing      = errors.New("authenticated user missing")
	errAuthenticatedUserInvalid      = errors.New("authenticated user invalid")
	errAuthenticatedUserStoreMissing = errors.New("authenticated user store missing")
	errAuthenticatedUserLookup       = errors.New("authenticated user lookup failed")
)

type superuserGateConfig struct {
	StoreUnavailableStatus  int
	StoreUnavailableCode    string
	StoreUnavailableMessage string
	InvalidUserStatus       int
	InvalidUserCode         string
	InvalidUserMessage      string
	ForbiddenMessage        string
}

type SuperuserGateConfig struct {
	StoreUnavailableStatus  int
	StoreUnavailableCode    string
	StoreUnavailableMessage string
	InvalidUserStatus       int
	InvalidUserCode         string
	InvalidUserMessage      string
	ForbiddenMessage        string
}

func (cfg SuperuserGateConfig) internal() superuserGateConfig {
	return superuserGateConfig{
		StoreUnavailableStatus:  cfg.StoreUnavailableStatus,
		StoreUnavailableCode:    cfg.StoreUnavailableCode,
		StoreUnavailableMessage: cfg.StoreUnavailableMessage,
		InvalidUserStatus:       cfg.InvalidUserStatus,
		InvalidUserCode:         cfg.InvalidUserCode,
		InvalidUserMessage:      cfg.InvalidUserMessage,
		ForbiddenMessage:        cfg.ForbiddenMessage,
	}
}

func RequireSuperuser(w http.ResponseWriter, r *http.Request, querier UserByIDQuerier, cfg SuperuserGateConfig) (sqlc.User, bool) {
	return requireSuperuser(w, r, querier, cfg.internal())
}

func authenticatedUserFromRequest(r *http.Request, querier userByIDQuerier) (sqlc.User, error) {
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || caller == nil {
		return sqlc.User{}, errAuthenticatedUserMissing
	}
	callerID, err := uuid.Parse(caller.ID)
	if err != nil {
		return sqlc.User{}, errAuthenticatedUserInvalid
	}
	if querier == nil {
		return sqlc.User{}, errAuthenticatedUserStoreMissing
	}
	user, err := querier.GetUserByID(r.Context(), callerID)
	if err != nil {
		return sqlc.User{}, errAuthenticatedUserLookup
	}
	return user, nil
}

func requireSuperuser(w http.ResponseWriter, r *http.Request, querier userByIDQuerier, cfg superuserGateConfig) (sqlc.User, bool) {
	user, err := authenticatedUserFromRequest(r, querier)
	switch {
	case err == nil:
	case errors.Is(err, errAuthenticatedUserMissing):
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return sqlc.User{}, false
	case errors.Is(err, errAuthenticatedUserInvalid):
		status := cfg.InvalidUserStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		code := cfg.InvalidUserCode
		if code == "" {
			code = "internal_error"
		}
		message := cfg.InvalidUserMessage
		if message == "" {
			message = "Invalid user ID"
		}
		RespondRequestError(w, r, status, code, message)
		return sqlc.User{}, false
	case errors.Is(err, errAuthenticatedUserStoreMissing):
		status := cfg.StoreUnavailableStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		code := cfg.StoreUnavailableCode
		if code == "" {
			code = "store_unavailable"
		}
		message := cfg.StoreUnavailableMessage
		if message == "" {
			message = "Admin store not configured"
		}
		RespondRequestError(w, r, status, code, message)
		return sqlc.User{}, false
	default:
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "Caller not found")
		return sqlc.User{}, false
	}
	if !user.IsSuperuser {
		message := cfg.ForbiddenMessage
		if message == "" {
			message = "Superuser required"
		}
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", message)
		return sqlc.User{}, false
	}
	return user, true
}

func (a *authorizationSupport) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	a.engine = engine
	a.querier = querier
}

func (a *authorizationSupport) authorizeClusterAction(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, resource rbac.Resource, verb rbac.Verb) bool {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if !a.allowsCluster(bindings, clusterID, resource, verb) {
		RespondRequestError(w, r, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
		return false
	}
	return true
}

func (a *authorizationSupport) bindingsForContext(ctx context.Context) ([]rbac.RoleBinding, bool, error) {
	if a == nil || a.engine == nil || a.querier == nil {
		if _, ok := middleware.GetAuthenticatedUser(ctx); ok {
			return nil, true, errAuthorizationNotConfigured
		}
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
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if !a.allowsGlobal(bindings, resource, verb) {
		RespondRequestError(w, r, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
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
