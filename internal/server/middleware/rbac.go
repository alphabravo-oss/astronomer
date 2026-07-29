package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// RBACQuerier looks up role bindings for a user.
type RBACQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// RBACCacheInvalidator is implemented by RBACQuerier implementations that
// front their lookups with a cache. Mutation handlers (CreateBinding /
// DeleteBinding / UpdateRole / DeleteRole) call Invalidate after a successful
// DB write so the next authenticated request sees the change immediately
// instead of waiting for the cache TTL. InvalidateAll is used when a role
// definition changes — its rules are denormalised into every cached binding
// holding that role, and we don't keep a reverse index from role → users.
type RBACCacheInvalidator interface {
	Invalidate(userID string)
	InvalidateAll()
}

// RequirePermission creates middleware that checks if the authenticated user
// has the required permission (resource + verb) at the appropriate scope.
// Scope is determined from URL params: {cluster_id} and {project_id}; the
// namespace comes from a route param only (see namespaceContext).
func RequirePermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	return RequirePermissionForNamespace(engine, querier, resource, verb, namespaceContext)
}

// RequireQueryNamespacePermission is RequirePermission for a route whose
// handler scopes its own work to ?namespace= — it evaluates the gate against
// that same query value.
//
// Only mount it where the handler provably uses the identical value (e.g.
// ResourceHandler.ListNamedResources builds /api/v1/namespaces/<ns>/<type> from
// it). If the handler derives its target anywhere else, the query becomes a
// forgeable key to every namespace-scoped binding the caller holds — use plain
// RequirePermission, which fails closed.
func RequireQueryNamespacePermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	return RequirePermissionForNamespace(engine, querier, resource, verb, namespaceContextWithQuery)
}

// RequirePermissionForNamespace is RequirePermission with a caller-supplied
// namespace resolver, for gates whose target namespace is neither a route param
// nor the query string — notably a create-from-body route, which must be
// authorized against the namespace inside the manifest it is about to apply.
func RequirePermissionForNamespace(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb, resolveNamespace func(*http.Request) string) func(http.Handler) http.Handler {
	if resolveNamespace == nil {
		resolveNamespace = namespaceContext
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			var clusterID, projectID uuid.UUID
			clusterParam := chi.URLParam(r, "cluster_id")
			if clusterParam == "" && resource == rbac.ResourceClusters {
				clusterParam = chi.URLParam(r, "id")
			}
			if cid := clusterParam; cid != "" {
				parsed, err := uuid.Parse(cid)
				if err == nil {
					clusterID = parsed
				}
			}
			projectParam := chi.URLParam(r, "project_id")
			if projectParam == "" && resource == rbac.ResourceProjects {
				projectParam = chi.URLParam(r, "id")
			}
			if pid := projectParam; pid != "" {
				parsed, err := uuid.Parse(pid)
				if err == nil {
					projectID = parsed
				}
			}
			namespace := resolveNamespace(r)

			if !engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireListPermission gates a cluster-scoped LIST route. It behaves EXACTLY
// like RequirePermission except that, when namespaceScoped is true and the
// caller cannot pass the plain scope check for a BARE list (no ?namespace=), it
// falls back to HasAnyNamespaceAccess: a namespace- or project-scoped reader is
// allowed through to the handler, which then filters the results down to their
// authorized namespaces. When namespaceScoped is false this is byte-identical to
// RequirePermission.
//
// A request that names a specific ?namespace= is NOT given the broad fallback:
// it is evaluated by the ordinary scope check, so a crafted namespace the caller
// is not authorized for still yields a clean 403 here.
func RequireListPermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb, namespaceScoped bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			var clusterID, projectID uuid.UUID
			clusterParam := chi.URLParam(r, "cluster_id")
			if clusterParam == "" && resource == rbac.ResourceClusters {
				clusterParam = chi.URLParam(r, "id")
			}
			if cid := clusterParam; cid != "" {
				parsed, err := uuid.Parse(cid)
				if err == nil {
					clusterID = parsed
				}
			}
			projectParam := chi.URLParam(r, "project_id")
			if projectParam == "" && resource == rbac.ResourceProjects {
				projectParam = chi.URLParam(r, "id")
			}
			if pid := projectParam; pid != "" {
				parsed, err := uuid.Parse(pid)
				if err == nil {
					projectID = parsed
				}
			}
			namespace := namespaceContextWithQuery(r)

			allowed := engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace)
			// Bare-list fallback: only when the flag is on and the request did
			// not pin a specific namespace. A pinned ?namespace= is judged solely
			// by CheckPermission above so an unauthorized namespace 403s here.
			if !allowed && namespaceScoped && namespace == "" {
				allowed = engine.HasAnyNamespaceAccess(bindings, resource, verb, clusterID)
			}

			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireCollectionPermission gates a top-level COLLECTION route (GET
// /clusters/, GET /projects/) — one whose URL carries no cluster or project id.
// It behaves EXACTLY like RequirePermission except that a caller who fails the
// plain check is given a second chance through HasAnyScopedAccess: holding the
// permission on ANY cluster or project admits them to the handler, which then
// filters the page down to the objects they may actually see.
//
// Without this, the scope check runs at uuid.Nil and only a GLOBAL binding can
// match, so a user whose sole grant is "Cluster Owner on one cluster" is 403'd
// off the fleet landing page entirely. The fallback grants no visibility on its
// own: every handler behind it MUST filter (see authorizedScopeIDs).
func RequireCollectionPermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			// A collection URL has no {id}/{cluster_id}/{project_id} to bind, so
			// both scopes stay uuid.Nil and only a global binding can satisfy
			// CheckPermission. A pinned ?namespace= is still honoured, same as
			// RequireListPermission, so a crafted namespace cannot widen anything.
			namespace := namespaceContextWithQuery(r)
			allowed := engine.CheckPermission(bindings, resource, verb, uuid.UUID{}, uuid.UUID{}, namespace)
			if !allowed && namespace == "" {
				allowed = engine.HasAnyScopedAccess(bindings, resource, verb)
			}

			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// namespaceContext resolves the namespace a permission check is evaluated
// against from the ROUTE only.
//
// SECURITY: it deliberately does NOT fall back to ?namespace=. A synthetic
// namespace-scoped cluster binding (the shape every project member now has —
// see expandProjectBindings) matches only when the request's namespace equals
// the binding's, so a query parameter that reaches the gate is an
// attacker-supplied key to it. On a route whose handler derives its real target
// somewhere else — the request BODY (ResourceHandler.CreateNamedResource) or
// nowhere at all (ProjectHandler.ListByCluster, the metrics handlers) — that
// key opens the whole cluster. A route param cannot be forged this way: it is
// the path the handler itself acts on.
//
// Routes whose handler genuinely scopes itself to ?namespace= opt back in via
// namespaceContextWithQuery below; the invariant they must satisfy is
// "gate namespace == handler namespace".
func namespaceContext(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{"namespace", "namespace_name", "ns"} {
		if value := strings.TrimSpace(chi.URLParam(r, key)); value != "" {
			return value
		}
	}
	return ""
}

// namespaceContextWithQuery is namespaceContext plus the ?namespace= fallback.
//
// Only for gates paired with a handler that either narrows its own query to
// that exact namespace (RequireQueryNamespacePermission's callers) or filters
// its result set against the caller's authorized-namespace allow-set
// (RequireListPermission / RequireCollectionPermission, whose handlers call
// authorizedNamespaces / authorizedScopeIDs). In both cases naming a namespace
// can only narrow what comes back, never widen it.
func namespaceContextWithQuery(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ns := namespaceContext(r); ns != "" {
		return ns
	}
	return strings.TrimSpace(r.URL.Query().Get("namespace"))
}
