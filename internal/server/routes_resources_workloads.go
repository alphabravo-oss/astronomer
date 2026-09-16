package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// requireListPermission wraps appmiddleware.RequireListPermission with the same
// nil-guard pass-through as requirePermission (routes.go). When the
// namespace_scoped_rbac_enabled flag (deps.ClusterResources.NamespaceScopedRBAC) is off it is
// byte-identical to requirePermission for the same resource/verb.
func requireListPermission(engine *rbac.Engine, querier rbac.BindingQuerier, resource rbac.Resource, verb rbac.Verb, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return appmiddleware.RequireListPermission(engine, querier, resource, verb, namespaceScoped)
}

// requireNamespacePickerListPermission gates the cluster Namespaces and Events
// list routes. These back the namespace picker and the events view that the
// namespace-scoped (project) persona depends on — but that persona is granted
// workloads/pods, NOT clusters:read. Gating them on clusters:read the way the
// coarse path does (requireListPermission with ResourceClusters) locks the
// exact users this feature serves out with a hard 403: CheckPermission(clusters,
// read) fails, and HasAnyNamespaceAccess(clusters, read) is empty because the
// synthetic per-namespace bindings carry the project role's rules (workloads/
// pods/…), never clusters:read.
//
// This wrapper keeps clusters:read as the PRIMARY check, so with the flag OFF it
// is byte-identical to requirePermission(clusters, read) — no behavior change
// for the pre-feature path. Only when namespaceScoped is on does it additionally
// admit a caller who holds any namespace-scoped read on workloads OR pods (the
// resources project roles actually grant, and exactly what the sibling Pods and
// Workloads list pages key off). The handler then filters the returned list down
// to the caller's authorized namespaces, so admission never widens what they see.
func requireNamespacePickerListPermission(engine *rbac.Engine, querier rbac.BindingQuerier, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := reqctx.AuthenticatedUser(r.Context())
			if !ok || user == nil {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			allowed := engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbRead, clusterID, projectID)
			if !allowed && namespaceScoped {
				allowed = engine.HasAnyNamespaceAccess(bindings, rbac.ResourceWorkloads, rbac.VerbList, clusterID) ||
					engine.HasAnyNamespaceAccess(bindings, rbac.ResourcePods, rbac.VerbList, clusterID)
			}
			if !allowed {
				writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerResourcesWorkloadsRoutes(r chi.Router, deps RouterDependencies) {
	mutationWriteScope := appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteClusters)
	// In-memory short-TTL idempotency guard for the resource/workload/node
	// mutations below. Self-skips reads, so applying it to these groups
	// covers every POST/PUT/PATCH/DELETE without per-route tagging. These
	// The durable operation-backed handlers also use the DB ledger; this local
	// layer absorbs fast same-replica retries while the DB layer provides the
	// cross-replica/restart guarantee. Janitor lifetime is process-scoped.
	idem := appmiddleware.Idempotency(context.Background())

	if deps.ClusterResources.Resources != nil {
		r.Group(func(r chi.Router) {
			r.Use(mutationWriteScope)
			r.Use(idem)
			r.With(requireNamedResourceListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries)).
				Get("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumes|persistentvolumeclaims|storageclasses|gateways|httproutes|gatewayclasses|grpcroutes|tcproutes|udproutes|tlsroutes|referencegrants)}/", deps.ClusterResources.Resources.ListNamedResources)
			// Create is gated on the BODY's metadata.namespace, not the URL —
			// see requireNamedResourceCreatePermission.
			r.With(requireNamedResourceCreatePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries)).
				Post("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/", deps.ClusterResources.Resources.CreateNamedResource)
			r.With(requireNamedResourcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "resource_type", rbac.VerbDelete)).
				Delete("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/{namespace}/{name}/", deps.ClusterResources.Resources.DeleteNamedResource)
			r.With(requireNamedResourcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "resource_type", rbac.VerbDelete)).
				Delete("/clusters/{cluster_id}/resources/{resource_type:(?:persistentvolumes)}/{name}/", deps.ClusterResources.Resources.DeleteNamedResource)
			r.With(
				requireGenericResourceListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries),
				auditGenericSecretList(deps.CoreAuth.AuditWriter),
			).Get("/clusters/{cluster_id}/resources/generic/{resource_type}/", deps.ClusterResources.Resources.ListGenericResources)
			// The count handler performs per-resource and per-namespace RBAC in one
			// binding lookup, omitting unauthorized types from its response.
			r.Get("/clusters/{cluster_id}/resource-counts/", deps.ClusterResources.Resources.CountResources)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).
				Get("/clusters/{cluster_id}/resources/discovery/", deps.ClusterResources.Resources.GetResourceDiscovery)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).
				Get("/clusters/{cluster_id}/resources/schema/", deps.ClusterResources.Resources.GetResourceSchema)
			// Authorization is operation-dependent: the handler binds the opaque ID
			// to this cluster, then rechecks the stored originating resource verb or
			// cluster:read for support access.
			r.Get("/clusters/{cluster_id}/resources/operations/{id}/", deps.ClusterResources.Resources.GetResourceOperation)
			r.Get("/settings/", deps.ClusterResources.Resources.GetGeneralSettings)
			// Per-resource REST verbs (Python: /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/).
			r.With(requireNamedResourcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "type", rbac.VerbRead)).
				Get("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.ClusterResources.Resources.GetNamedResource)
			r.With(
				requireNamedResourcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "type", rbac.VerbUpdate),
				requireNamedResourceForcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "type"),
			).
				Put("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.ClusterResources.Resources.UpdateNamedResource)
			r.With(requireNamedResourcePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, "type", rbac.VerbDelete)).
				Delete("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.ClusterResources.Resources.DeleteNamedResourceREST)
			// Node action endpoints (cordon/uncordon/drain/metadata/taints).
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/cordon/", deps.ClusterResources.Resources.CordonNode)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/uncordon/", deps.ClusterResources.Resources.UncordonNode)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbManage)).Post("/nodes/{cluster_id}/{node_name}/drain/", deps.ClusterResources.Resources.DrainNode)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/labels/", deps.ClusterResources.Resources.SetNodeLabel)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/labels/remove/", deps.ClusterResources.Resources.RemoveNodeLabel)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/annotations/", deps.ClusterResources.Resources.SetNodeAnnotation)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/annotations/remove/", deps.ClusterResources.Resources.RemoveNodeAnnotation)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/taints/", deps.ClusterResources.Resources.AddNodeTaint)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/taints/remove/", deps.ClusterResources.Resources.RemoveNodeTaint)
			// The handler performs an action-aware current-permission check after
			// loading and route-binding the receipt (read OR its mutation verb).
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/nodes/{cluster_id}/{node_name}/operations/{id}/", deps.ClusterResources.Resources.GetNodeOperation)
			// User CRUD (List/Get already wired above; add Create/Update/Delete + reset-password).
			// These identity-plane mutations require both users:* RBAC and an
			// admin-scoped API token when token auth is used. Browser sessions rely
			// on the RBAC gate.
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbCreate)).Post("/users/", deps.ClusterResources.Resources.CreateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Put("/users/{id}/", deps.ClusterResources.Resources.UpdateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Patch("/users/{id}/", deps.ClusterResources.Resources.UpdateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbDelete)).Delete("/users/{id}/", deps.ClusterResources.Resources.DeleteUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Post("/users/{id}/reset-password/", deps.ClusterResources.Resources.ResetUserPassword)
			// Admin-only auth hardening endpoints (migration 039).
			//
			// Superuser gating lives inside the handler — same pattern as the
			// other /admin/* routes here (keyStatusHandler, AdminQueues etc.).
			// We deliberately keep the auth requirement on the wrapper so a
			// non-superuser hits a clean 403 instead of falling through.
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/unlock/", deps.ClusterResources.Resources.UnlockUser)
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/force-logout/", deps.ClusterResources.Resources.ForceLogoutUser)
			// 2FA admin override. Superuser-only inside the handler.
			if deps.CoreAuth.TOTP != nil {
				r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/disable-totp/", deps.CoreAuth.TOTP.AdminForceDisable)
			}
		})
	}

	if deps.ClusterResources.Workloads != nil {
		r.Group(func(r chi.Router) {
			r.Use(mutationWriteScope)
			r.Use(idem)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/controller/status/", deps.ClusterResources.Workloads.ControllerStatus)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/", deps.ClusterResources.Workloads.ListOperations)
			// Receipt authorization is row-aware: creators may poll their own
			// mutation receipts, while support readers retain scoped access.
			r.Get("/workloads/operations/{id}/", deps.ClusterResources.Workloads.GetOperation)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbUpdate)).Post("/workloads/operations/{id}/retry/", deps.ClusterResources.Workloads.RetryOperation)
			r.With(requireListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbList, deps.ClusterResources.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/workloads/", deps.ClusterResources.Workloads.List)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.ClusterResources.Workloads.Get)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourcePods, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/pods/", deps.ClusterResources.Workloads.ListWorkloadPods)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbScale)).Patch("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/scale/", deps.ClusterResources.Workloads.Scale)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRestart)).Post("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/restart/", deps.ClusterResources.Workloads.Restart)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceWorkloads, rbac.VerbDelete)).Delete("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.ClusterResources.Workloads.Delete)
			r.With(requireNamespacePickerListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, deps.ClusterResources.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/namespaces/", deps.ClusterResources.Workloads.ListNamespaces)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbList)).Get("/clusters/{cluster_id}/nodes/", deps.ClusterResources.Workloads.ListNodes)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNodes, rbac.VerbRead)).Get("/clusters/{cluster_id}/nodes/{node_name}/", deps.ClusterResources.Workloads.GetNode)
			r.With(requireNamespacePickerListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, deps.ClusterResources.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/events/", deps.ClusterResources.Workloads.ListEvents)
			r.With(requireListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourcePods, rbac.VerbList, deps.ClusterResources.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/pods/", deps.ClusterResources.Workloads.ListPods)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourcePods, rbac.VerbDelete)).Delete("/workloads/pods/{cluster_id}/{namespace}/{pod}/", deps.ClusterResources.Workloads.DeletePod)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourcePods, rbac.VerbLogs)).Get("/workloads/pods/{cluster_id}/{namespace}/{pod}/logs/", deps.ClusterResources.Workloads.PodLogs)
		})
	}

	if deps.ClusterResources.ServiceProxy != nil {
		r.With(
			requireServiceProxyScope(),
			requireServiceProxyPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries),
		).Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/", deps.ClusterResources.ServiceProxy)
		r.With(
			requireServiceProxyScope(),
			requireServiceProxyPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries),
		).Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*", deps.ClusterResources.ServiceProxy)
	}

}

// requireNamedResourceForcePermission adds a second, stronger permission check
// only when a caller explicitly asks server-side apply to take conflicting
// field ownership. Ordinary updates continue to require update.
func requireNamedResourceForcePermission(engine *rbac.Engine, querier rbac.BindingQuerier, routeParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.EqualFold(r.URL.Query().Get("force"), "true") {
				next.ServeHTTP(w, r)
				return
			}
			resource, _ := namedResourcePermission(chi.URLParam(r, routeParam), rbac.VerbManage)
			requirePermission(engine, querier, resource, rbac.VerbManage)(next).ServeHTTP(w, r)
		})
	}
}
