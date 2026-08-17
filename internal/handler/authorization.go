package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type authorizationSupport struct {
	engine  *rbac.Engine
	querier middleware.RBACQuerier
	// namespaceScoped enables per-namespace result filtering on list handlers.
	// Default false: authorizedNamespaces returns all=true immediately (no DB
	// call), so list responses are byte-identical to the pre-feature behavior.
	namespaceScoped bool
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
	return superuserGateConfig(cfg)
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
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
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Caller not found")
		return sqlc.User{}, false
	}
	if !user.IsSuperuser {
		message := cfg.ForbiddenMessage
		if message == "" {
			message = "Superuser required"
		}
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, message)
		return sqlc.User{}, false
	}
	return user, true
}

func (a *authorizationSupport) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	a.engine = engine
	a.querier = querier
}

// SetNamespaceScoped toggles per-namespace list filtering. Wired from the
// namespace_scoped_rbac_enabled config flag. Off (default) = no filtering.
func (a *authorizationSupport) SetNamespaceScoped(enabled bool) {
	a.namespaceScoped = enabled
}

// authorizedNamespaces reports the namespace visibility the caller has for
// (resource, verb) at clusterID.
//
//   - all==true: the caller may see everything. Returned when the feature flag
//     is off (fast path, no DB call), when authorization is not configured, or
//     when the caller holds a superuser / cluster-wide grant. names is nil.
//   - all==false: the caller is namespace-restricted; names is the exact
//     allow-set of visible namespaces (possibly empty → see nothing).
//
// A non-nil error means the binding lookup failed; callers must fail closed
// (surface a 500) rather than fall back to showing everything.
func (a *authorizationSupport) authorizedNamespaces(ctx context.Context, clusterID uuid.UUID, resource rbac.Resource, verb rbac.Verb) (bool, map[string]struct{}, error) {
	if a == nil || !a.namespaceScoped {
		return true, nil, nil
	}
	bindings, restricted, err := a.bindingsForContext(ctx)
	if err != nil {
		return false, nil, err
	}
	if !restricted || a.engine == nil {
		return true, nil, nil
	}
	all, names := a.engine.AuthorizedNamespaces(bindings, resource, verb, clusterID)
	return all, names, nil
}

// authorizedScopeIDs reports the cluster/project visibility the caller has for
// (resource, verb) on a COLLECTION request — a list with no scope in the URL.
// Pairs with middleware.RequireCollectionPermission, which admits scope-bound
// callers the plain gate would 403; this is the filter that keeps that safe.
//
//   - all==true: the caller may see everything — a superuser, a platform-wide
//     grant, or an unauthenticated context (which never reaches these routes;
//     the auth middleware 401s first). Both slices are nil.
//   - all==false: clusterIDs / projectIDs are the exact allow-sets, sorted so
//     the query argument is stable. Both empty means "see nothing".
//
// narrowed is passed straight through to rbac.AuthorizedScopeIDs and must match
// what the collection lists: NarrowedClustersWiden for /clusters/ (the objects
// ARE the clusters), NarrowedClustersExcluded for /projects/ (the objects live
// inside one, so widening would disclose a neighbouring tenant's rows).
//
// Unlike authorizedNamespaces this is NOT gated on the namespaceScoped flag:
// cluster_role_bindings are always live, so an unfiltered page would leak the
// whole fleet to a single-cluster user the moment the gate admits them.
//
// A non-nil error means the binding lookup failed OR authorization was never
// wired for an authenticated caller (bindingsForContext fails closed the same
// way authorizeClusterAction does) — callers must surface a 500, never fall
// back to an unfiltered page. SetAuthorization is therefore mandatory on any
// handler serving a gated collection route.
func (a *authorizationSupport) authorizedScopeIDs(ctx context.Context, resource rbac.Resource, verb rbac.Verb, narrowed rbac.NarrowedClusterPolicy) (bool, []uuid.UUID, []uuid.UUID, error) {
	bindings, restricted, err := a.bindingsForContext(ctx)
	if err != nil {
		return false, nil, nil, err
	}
	if !restricted || a.engine == nil {
		return true, nil, nil, nil
	}
	all, clusterIDs, projectIDs := a.engine.AuthorizedScopeIDs(bindings, resource, verb, narrowed)
	if all {
		return true, nil, nil, nil
	}
	return false, sortedUUIDs(clusterIDs), sortedUUIDs(projectIDs), nil
}

// sortedUUIDs flattens an id set into a deterministically ordered slice. Always
// returns a non-nil slice: a nil uuid[] argument makes `= ANY(...)` yield NULL
// rather than false, which would drop the predicate's fail-closed behaviour.
func sortedUUIDs(set map[uuid.UUID]struct{}) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

func (a *authorizationSupport) authorizeClusterAction(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, resource rbac.Resource, verb rbac.Verb) bool {
	return a.authorizeClusterActionAny(w, r, clusterID, clusterPermission{resource, verb})
}

func (a *authorizationSupport) authorizeProjectAction(w http.ResponseWriter, r *http.Request, projectID uuid.UUID, resource rbac.Resource, verb rbac.Verb) bool {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if a.engine != nil && a.engine.CheckPermission(bindings, resource, verb, uuid.Nil, projectID) {
		return true
	}
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
	return false
}

// clusterPermission is one (resource, verb) alternative for
// authorizeClusterActionAny.
type clusterPermission struct {
	Resource rbac.Resource
	Verb     rbac.Verb
}

// authorizeClusterActionAny is authorizeClusterAction for an endpoint whose
// admission is a DISJUNCTION: any one of perms suffices. It exists for the
// stream-ticket mint path, where a single ticket kind is redeemed by more than
// one route and issuance must therefore match the WEAKEST redeemer — a ticket
// stricter than the stream it opens is a silent feature outage, and a ticket
// looser than one is only safe because every redeemer re-checks its own gate.
// Do not reach for it to soften a single-redeemer gate.
func (a *authorizationSupport) authorizeClusterActionAny(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, perms ...clusterPermission) bool {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	for _, p := range perms {
		if a.allowsCluster(bindings, clusterID, p.Resource, p.Verb) {
			return true
		}
	}
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
	return false
}

// resourceScopeFilter returns a predicate for filtering a list of resources the
// caller may see for (resource, verb). Each item is checked by its cluster: a
// cluster-scoped item (valid==true) needs a matching cluster grant; an unscoped
// (global) item needs a global grant. restricted==false means the caller is
// unrestricted and the predicate always returns true (the returned total should
// stay exact). On a binding-lookup failure it writes a 500 and returns ok=false.
func (a *authorizationSupport) resourceScopeFilter(w http.ResponseWriter, r *http.Request, resource rbac.Resource, verb rbac.Verb) (allow func(clusterID uuid.UUID, valid bool) bool, restricted bool, ok bool) {
	bindings, restricted, err := a.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return nil, false, false
	}
	if !restricted {
		return func(uuid.UUID, bool) bool { return true }, false, true
	}
	return func(clusterID uuid.UUID, valid bool) bool {
		if valid {
			return a.allowsCluster(bindings, clusterID, resource, verb)
		}
		return a.allowsGlobal(bindings, resource, verb)
	}, true, true
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	if !a.allowsGlobal(bindings, resource, verb) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
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
