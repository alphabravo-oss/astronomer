package server

import (
	"context"
	"encoding/json"
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// requireListPermission wraps appmiddleware.RequireListPermission with the same
// nil-guard pass-through as requirePermission (routes.go). When the
// namespace_scoped_rbac_enabled flag (deps.NamespaceScopedRBAC) is off it is
// byte-identical to requirePermission for the same resource/verb.
func requireListPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, resource rbac.Resource, verb rbac.Verb, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler { return next }
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
func requireNamespacePickerListPermission(engine *rbac.Engine, querier appmiddleware.RBACQuerier, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := appmiddleware.GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				})
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				})
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			allowed := engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbRead, clusterID, projectID)
			if !allowed && namespaceScoped {
				allowed = engine.HasAnyNamespaceAccess(bindings, rbac.ResourceWorkloads, rbac.VerbList, clusterID) ||
					engine.HasAnyNamespaceAccess(bindings, rbac.ResourcePods, rbac.VerbList, clusterID)
			}
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				})
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
	// typed verbs are NOT on the DB-backed operation-idempotency path (that
	// covers tools/catalog/clusters/fleet/backups), so there is no double
	// dedup. Janitor lifetime is process-scoped, matching the rate limiter.
	idem := appmiddleware.Idempotency(context.Background())

	if deps.Resources != nil {
		r.Group(func(r chi.Router) {
			r.Use(mutationWriteScope)
			r.Use(idem)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).
				Get("/clusters/{cluster_id}/resources/{group}/{version}/{kind}/", deps.Resources.ListResources)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "resource_type", rbac.VerbList)).
				Get("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumes|persistentvolumeclaims|storageclasses|gateways|httproutes|gatewayclasses|grpcroutes|tcproutes|udproutes|tlsroutes|referencegrants)}/", deps.Resources.ListNamedResources)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "resource_type", rbac.VerbCreate)).
				Post("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/", deps.Resources.CreateNamedResource)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "resource_type", rbac.VerbDelete)).
				Delete("/clusters/{cluster_id}/resources/{resource_type:(?:services|ingresses|networkpolicies|persistentvolumeclaims)}/{namespace}/{name}/", deps.Resources.DeleteNamedResource)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "resource_type", rbac.VerbDelete)).
				Delete("/clusters/{cluster_id}/resources/{resource_type:(?:persistentvolumes)}/{name}/", deps.Resources.DeleteNamedResource)
			r.With(
				requireGenericResourceListPermission(deps.RBACEngine, deps.RBACQueries),
				auditGenericSecretList(deps.AuditWriter),
			).Get("/clusters/{cluster_id}/resources/generic/{resource_type}/", deps.Resources.ListGenericResources)
			r.Get("/settings/", deps.Resources.GetGeneralSettings)
			// Per-resource REST verbs (Python: /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/).
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "type", rbac.VerbRead)).
				Get("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.GetNamedResource)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "type", rbac.VerbUpdate)).
				Put("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.UpdateNamedResource)
			r.With(requireNamedResourcePermission(deps.RBACEngine, deps.RBACQueries, "type", rbac.VerbDelete)).
				Delete("/resources/{cluster_id}/{type}/{namespace}/{name}/", deps.Resources.DeleteNamedResourceREST)
			// Node action endpoints (cordon/uncordon/drain/metadata/taints).
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/cordon/", deps.Resources.CordonNode)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/uncordon/", deps.Resources.UncordonNode)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbManage)).Post("/nodes/{cluster_id}/{node_name}/drain/", deps.Resources.DrainNode)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/labels/", deps.Resources.SetNodeLabel)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/labels/remove/", deps.Resources.RemoveNodeLabel)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/annotations/", deps.Resources.SetNodeAnnotation)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/annotations/remove/", deps.Resources.RemoveNodeAnnotation)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/taints/", deps.Resources.AddNodeTaint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbUpdate)).Post("/nodes/{cluster_id}/{node_name}/taints/remove/", deps.Resources.RemoveNodeTaint)
			// User CRUD (List/Get already wired above; add Create/Update/Delete + reset-password).
			// These identity-plane mutations require both users:* RBAC and an
			// admin-scoped API token when token auth is used. Browser sessions rely
			// on the RBAC gate.
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbCreate)).Post("/users/", deps.Resources.CreateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Put("/users/{id}/", deps.Resources.UpdateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Patch("/users/{id}/", deps.Resources.UpdateUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbDelete)).Delete("/users/{id}/", deps.Resources.DeleteUser)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbUpdate)).Post("/users/{id}/reset-password/", deps.Resources.ResetUserPassword)
			// Admin-only auth hardening endpoints (migration 039).
			//
			// Superuser gating lives inside the handler — same pattern as the
			// other /admin/* routes here (keyStatusHandler, AdminQueues etc.).
			// We deliberately keep the auth requirement on the wrapper so a
			// non-superuser hits a clean 403 instead of falling through.
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/unlock/", deps.Resources.UnlockUser)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/force-logout/", deps.Resources.ForceLogoutUser)
			// 2FA admin override. Superuser-only inside the handler.
			if deps.TOTP != nil {
				r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/disable-totp/", deps.TOTP.AdminForceDisable)
			}
		})
	}

	if deps.Workloads != nil {
		r.Group(func(r chi.Router) {
			r.Use(mutationWriteScope)
			r.Use(idem)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/controller/status/", deps.Workloads.ControllerStatus)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/", deps.Workloads.ListOperations)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/{id}/", deps.Workloads.GetOperation)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbUpdate)).Post("/workloads/operations/{id}/retry/", deps.Workloads.RetryOperation)
			r.With(requireListPermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbList, deps.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/workloads/", deps.Workloads.List)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.Workloads.Get)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/pods/", deps.Workloads.ListWorkloadPods)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbScale)).Patch("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/scale/", deps.Workloads.Scale)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRestart)).Post("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/restart/", deps.Workloads.Restart)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbDelete)).Delete("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/", deps.Workloads.Delete)
			r.With(requireNamespacePickerListPermission(deps.RBACEngine, deps.RBACQueries, deps.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/namespaces/", deps.Workloads.ListNamespaces)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbList)).Get("/clusters/{cluster_id}/nodes/", deps.Workloads.ListNodes)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNodes, rbac.VerbRead)).Get("/clusters/{cluster_id}/nodes/{node_name}/", deps.Workloads.GetNode)
			r.With(requireNamespacePickerListPermission(deps.RBACEngine, deps.RBACQueries, deps.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/events/", deps.Workloads.ListEvents)
			r.With(requireListPermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbList, deps.NamespaceScopedRBAC)).Get("/clusters/{cluster_id}/pods/", deps.Workloads.ListPods)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbDelete)).Delete("/workloads/pods/{cluster_id}/{namespace}/{pod}/", deps.Workloads.DeletePod)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbLogs)).Get("/workloads/pods/{cluster_id}/{namespace}/{pod}/logs/", deps.Workloads.PodLogs)
		})
	}

	if deps.ServiceProxy != nil {
		r.With(
			requireServiceProxyScope(),
			requireServiceProxyPermission(deps.RBACEngine, deps.RBACQueries),
		).Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/", deps.ServiceProxy)
		r.With(
			requireServiceProxyScope(),
			requireServiceProxyPermission(deps.RBACEngine, deps.RBACQueries),
		).Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*", deps.ServiceProxy)
	}

}
