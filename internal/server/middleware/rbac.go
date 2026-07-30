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
// Scope is determined from URL params: {cluster_id} and {project_id} (see
// permissionScope for the bare-{id} rule); the namespace comes from a route
// param only (see namespaceContext).
func RequirePermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	return RequirePermissionForNamespace(engine, querier, resource, verb, namespaceContext)
}

// scopeContextKey is an unexported type for scope-declaration context keys.
type scopeContextKey string

// idParamIsClusterKey marks a route subtree in which the chi param {id} names a
// CLUSTER. Set by ClusterScopeFromIDParam, read by permissionScope.
const idParamIsClusterKey scopeContextKey = "id_param_is_cluster"

// ClusterScopeFromIDParam declares that, everywhere inside the route subtree it
// is mounted on, the chi URL param {id} names a CLUSTER — so a permission gate
// on one of those routes must be evaluated at THAT cluster's scope, whatever
// resource the gate happens to check.
//
// Mount it with r.Use at the top of the subtree (registerClusterRoutes does);
// it needs no URL params of its own, so it is safe on a chi.Route group whose
// params are not bound until the inner route matches.
//
// WHY THIS EXISTS. permissionScope has to decide whether a bare {id} is a
// cluster id, and it cannot ask the router. Its only signal used to be the
// RESOURCE being gated: fall back to {id} for rbac.ResourceClusters, and for
// rbac.ResourceProjects. That is a proxy for the real fact, and the proxy is
// wrong for any route under /clusters/{id} that gates on something else. Every
// such route today gates on rbac.ResourceMonitoring: GET /{id}/health/, the
// eight /{id}/monitoring/... config and stack-lifecycle routes, and the
// /{id}/metrics/ pair that routes_monitoring.go shadows with a {cluster_id}
// registration on the parent router. Every one of them resolved to uuid.Nil,
// i.e. a GLOBAL monitoring check, which refused a caller whose monitoring
// grant is scoped to exactly that cluster (Cluster Owner, Cluster Operator,
// Cluster Viewer, Cluster Member, Service Mesh Operator) on their OWN cluster:
// rbac.bindingApplies will not match a cluster-scoped binding against the nil
// scope. That is the whole of what this declaration changes.
//
// WHAT IT DELIBERATELY DOES NOT CHANGE. A GLOBAL monitoring grant still reaches
// every cluster's monitoring routes, exactly as it did before. That is the
// intended meaning of a global binding, not a hole this closes:
// rbac.bindingApplies (internal/rbac/engine.go) returns true for a binding with
// no cluster and no project at ANY scope, monitoring-admin.yaml is declared
// `scope: global` and "across the fleet", and every ResourceClusters route
// behaves the same way. TestClusterMonitoringRoutesStillAdmitGlobalBinding
// (internal/server/routes_monitoring_scope_test.go) passes with and without
// this declaration on purpose — it is the fence that keeps fleet-wide
// operators working, not evidence of a fix. Constraining a global grant to a
// cluster list is a change to the BINDING MODEL and belongs nowhere near a
// scope resolver.
//
// The declaration is additive: it only ever supplies a cluster id where the
// resource-based fallback left uuid.Nil, so mounting it can widen a check from
// global to cluster-scoped but can never take a scope away. Routes outside the
// subtree are untouched, which matters for the shared, genuinely fleet-wide
// /settings/monitoring/* family — those authorize in-handler against
// uuid.Nil (authorizeGlobalAction) and must keep global semantics, so a
// cluster-scoped grant must not reach them.
func ClusterScopeFromIDParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), idParamIsClusterKey, true)))
	})
}

// permissionScope resolves the (cluster, project) scope a permission gate is
// evaluated at, from the request's route params.
//
// {cluster_id} and {project_id} are unambiguous. A bare {id} is not: it names a
// cluster under /clusters/{id}, a project under /projects/{id}, and some
// unrelated object almost everywhere else (/rbac/global-roles/{id},
// /monitoring/endpoints/{id}, /audit/{id}, /users/{id}, /cluster-groups/{id},
// ...). Binding one of THOSE into a cluster scope would be nonsense, so the
// fallback is conditional on either
//
//   - the subtree having declared it (ClusterScopeFromIDParam), which is the
//     accurate signal and the one /clusters/{id} uses; or
//   - the gated resource being clusters/projects, which is the older
//     inference and stays because it also covers the handful of cluster and
//     project routes registered OUTSIDE those subtrees —
//     /clusters/{id}/gatekeeper/constraints/*, /clusters/{id}/v2/pods/,
//     /dashboards/clusters/{id}/ and /projects/{id}/default-vault-connection/.
//
// A gate on a route that satisfies neither resolves to uuid.Nil and is a global
// check, which is what a route with no scope in its URL should be.
//
// THERE IS A SECOND RESOLVER, and it does not follow this rule:
// server.permissionScopeIDs (internal/server/routes.go), used by
// requireAnyPermission, requireK8sProxyPermission and the workloads list gate,
// falls back to a bare {id} unconditionally and binds it as both the cluster
// and the project scope. It fails closed today for the reasons written at its
// definition. Any change here should be mirrored there, or better, should
// finish collapsing the two.
func permissionScope(r *http.Request, resource rbac.Resource) (uuid.UUID, uuid.UUID) {
	var clusterID, projectID uuid.UUID
	clusterParam := chi.URLParam(r, "cluster_id")
	if clusterParam == "" && (idParamNamesCluster(r) || resource == rbac.ResourceClusters) {
		clusterParam = chi.URLParam(r, "id")
	}
	if clusterParam != "" {
		if parsed, err := uuid.Parse(clusterParam); err == nil {
			clusterID = parsed
		}
	}
	projectParam := chi.URLParam(r, "project_id")
	if projectParam == "" && resource == rbac.ResourceProjects {
		projectParam = chi.URLParam(r, "id")
	}
	if projectParam != "" {
		if parsed, err := uuid.Parse(projectParam); err == nil {
			projectID = parsed
		}
	}
	return clusterID, projectID
}

func idParamNamesCluster(r *http.Request) bool {
	if r == nil {
		return false
	}
	declared, _ := r.Context().Value(idParamIsClusterKey).(bool)
	return declared
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

			clusterID, projectID := permissionScope(r, resource)
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

			clusterID, projectID := permissionScope(r, resource)
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
