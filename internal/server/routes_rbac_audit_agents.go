package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerRBACAuditAgentRoutes(r chi.Router, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	writeRBAC := requireScope(iauth.ScopeWriteRBAC)

	if deps.CoreAuth.RBAC != nil {
		r.With(
			writeRBAC,
			requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate),
		).Post("/projects/{id}/apply-rbac-template/", deps.CoreAuth.RBAC.ApplyProjectTemplate)
		r.Route("/rbac", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/", deps.CoreAuth.RBAC.ListGlobalRoles)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-roles/", deps.CoreAuth.RBAC.CreateGlobalRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/{id}/", deps.CoreAuth.RBAC.GetGlobalRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/global-roles/{id}/", deps.CoreAuth.RBAC.UpdateGlobalRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-roles/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/", deps.CoreAuth.RBAC.ListClusterRoles)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-roles/", deps.CoreAuth.RBAC.CreateClusterRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/{id}/", deps.CoreAuth.RBAC.GetClusterRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/cluster-roles/{id}/", deps.CoreAuth.RBAC.UpdateClusterRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-roles/{id}/", deps.CoreAuth.RBAC.DeleteClusterRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/", deps.CoreAuth.RBAC.ListProjectRoles)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-roles/", deps.CoreAuth.RBAC.CreateProjectRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/{id}/", deps.CoreAuth.RBAC.GetProjectRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/project-roles/{id}/", deps.CoreAuth.RBAC.UpdateProjectRole)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-roles/{id}/", deps.CoreAuth.RBAC.DeleteProjectRole)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-bindings/", deps.CoreAuth.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-bindings/", deps.CoreAuth.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-bindings/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-bindings/", deps.CoreAuth.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-bindings/", deps.CoreAuth.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-bindings/{id}/", deps.CoreAuth.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-bindings/", deps.CoreAuth.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-bindings/", deps.CoreAuth.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-bindings/{id}/", deps.CoreAuth.RBAC.DeleteProjectRoleBinding)
			// Python-named binding path aliases (so both old and new clients work).
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-role-bindings/", deps.CoreAuth.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-role-bindings/", deps.CoreAuth.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-role-bindings/", deps.CoreAuth.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-role-bindings/", deps.CoreAuth.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-role-bindings/", deps.CoreAuth.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-role-bindings/", deps.CoreAuth.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteProjectRoleBinding)
			// Current user's effective roles + permission check.
			r.Get("/my-roles/", deps.CoreAuth.RBAC.MyRoles)
			r.Get("/my-roles/check/", deps.CoreAuth.RBAC.CheckMyRole)
			r.Get("/my-permissions/", deps.CoreAuth.RBAC.MyEffectivePermissions)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).
				Get("/effective-permissions/{user_id}/", deps.CoreAuth.RBAC.EffectivePermissionsForUser)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).
				Post("/permission-preview/", deps.CoreAuth.RBAC.PermissionPreview)
			// T1.1 — built-in role-templates catalog. Any authed user
			// can read; no rbac:read needed because the catalog is
			// static metadata about what the platform offers, not who
			// has what.
			r.Get("/templates/", deps.CoreAuth.RBAC.ListTemplates)
			r.Get("/templates/{name}/", deps.CoreAuth.RBAC.GetTemplate)
		})
	}

	if deps.CoreAuth.Principals != nil {
		principalsRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)
		principalsCreate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)
		usersRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbRead)
		usersCreate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbCreate)
		r.Route("/rbac/principals", func(r chi.Router) {
			r.With(rateLimit(appmiddleware.ClassSearch), principalsRead, usersRead).Get("/", deps.CoreAuth.Principals.Search)
			r.With(rateLimit(appmiddleware.ClassSearch), writeRBAC, principalsCreate, usersCreate).Post("/materialize/", deps.CoreAuth.Principals.Materialize)
		})
	}

	// Native per-CRD RBAC rules (migration 126). Nil unless native_rbac_enabled.
	// Authoring is an RBAC-management action, so it's gated on the same
	// ResourceRBAC permission + write-RBAC scope as roles/bindings above.
	if deps.ClusterResources.NativeRBAC != nil {
		r.Route("/native-rbac-rules", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/", deps.ClusterResources.NativeRBAC.List)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/", deps.ClusterResources.NativeRBAC.Create)
			r.With(writeRBAC, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterResources.NativeRBAC.Delete)
		})
	}

	if deps.ClusterResources.ClusterAgent != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/cluster-agents/", deps.ClusterResources.ClusterAgent.List)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/cluster-agents/{cluster_id}/", deps.ClusterResources.ClusterAgent.Get)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/cluster-agents/{cluster_id}/diagnostics/", deps.ClusterResources.ClusterAgent.Diagnostics)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/cluster-agents/{cluster_id}/diagnostics/bundle/", deps.ClusterResources.ClusterAgent.DiagnosticsBundle)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/cluster-agents/{cluster_id}/operations/", deps.ClusterResources.ClusterAgent.Operations)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/cluster-agents/{cluster_id}/self-test/", deps.ClusterResources.ClusterAgent.SelfTest)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/cluster-agents/{cluster_id}/upgrade-plan/", deps.ClusterResources.ClusterAgent.UpgradePlan)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/cluster-agents/{cluster_id}/upgrade/", deps.ClusterResources.ClusterAgent.Upgrade)
		// E3 (C2 rollout aid): cluster-admin agent posture report. Lists
		// every managed cluster whose agent still resolves to the
		// cluster-admin `admin` profile so operators can re-profile after
		// the GATE-0 fail-closed default flip. Superuser-gated inside the
		// handler (clean 401/403), same pattern as the other /admin/* reads.
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).
			Get("/admin/agents/cluster-admin-posture/", deps.ClusterResources.ClusterAgent.ClusterAdminPosture)
	}

	if deps.AdminPlatform.Audit != nil {
		auditReadOrList := requireAnyPermission(
			deps.CoreAuth.RBACEngine,
			deps.CoreAuth.RBACQueries,
			permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
		)
		r.Route("/audit", func(r chi.Router) {
			r.With(auditReadOrList).Get("/", deps.AdminPlatform.Audit.List)
			r.With(auditReadOrList).Get("/export/", deps.AdminPlatform.Audit.Export)
			r.With(auditReadOrList).Post("/exports/", deps.AdminPlatform.Audit.CreateExport)
			r.With(auditReadOrList).Get("/exports/{id}/", deps.AdminPlatform.Audit.GetExportOperation)
			r.With(auditReadOrList).Get("/exports/{id}/download/", deps.AdminPlatform.Audit.DownloadExport)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAuditLogs, rbac.VerbRead)).Get("/{id}/", deps.AdminPlatform.Audit.Get)
		})
	}

	if deps.AdminPlatform.Alerting != nil {
		// Alerting was previously mounted with no authorization at all: any
		// authenticated user could read channel delivery secrets (Slack webhook
		// URLs, PagerDuty keys), tamper with rules, and silence alerts. Gate every
		// verb on ResourceAlerts, matching the audit/rbac sibling blocks. Reads
		// need alerts:read (or :list); state changes need :update; creates :create;
		// deletes :delete.
		alertsRead := requireAnyPermission(
			deps.CoreAuth.RBACEngine,
			deps.CoreAuth.RBACQueries,
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbList},
		)
		alertsCreate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAlerts, rbac.VerbCreate)
		alertsUpdate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAlerts, rbac.VerbUpdate)
		alertsDelete := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAlerts, rbac.VerbDelete)
		r.Route("/alerting", func(r chi.Router) {
			r.With(alertsRead).Get("/channels/", deps.AdminPlatform.Alerting.ListChannels)
			r.With(alertsCreate).Post("/channels/", deps.AdminPlatform.Alerting.CreateChannel)
			r.With(alertsRead).Get("/channels/{id}/", deps.AdminPlatform.Alerting.GetChannel)
			r.With(alertsUpdate).Put("/channels/{id}/", deps.AdminPlatform.Alerting.UpdateChannel)
			r.With(alertsDelete).Delete("/channels/{id}/", deps.AdminPlatform.Alerting.DeleteChannel)
			r.With(alertsUpdate).Post("/channels/{id}/test/", deps.AdminPlatform.Alerting.TestChannel)
			r.With(alertsRead).Get("/rules/", deps.AdminPlatform.Alerting.ListRules)
			r.With(alertsCreate).Post("/rules/", deps.AdminPlatform.Alerting.CreateRule)
			r.With(alertsRead).Get("/rules/{id}/", deps.AdminPlatform.Alerting.GetRule)
			r.With(alertsUpdate).Put("/rules/{id}/", deps.AdminPlatform.Alerting.UpdateRule)
			r.With(alertsDelete).Delete("/rules/{id}/", deps.AdminPlatform.Alerting.DeleteRule)
			r.With(alertsUpdate).Post("/rules/{id}/enable/", deps.AdminPlatform.Alerting.EnableRule)
			r.With(alertsUpdate).Post("/rules/{id}/disable/", deps.AdminPlatform.Alerting.DisableRule)
			r.With(alertsRead).Get("/events/", deps.AdminPlatform.Alerting.ListEvents)
			r.With(alertsRead).Get("/events/{id}/", deps.AdminPlatform.Alerting.GetEvent)
			r.With(alertsUpdate).Post("/events/{id}/acknowledge/", deps.AdminPlatform.Alerting.AcknowledgeEvent)
			r.With(alertsUpdate).Post("/events/{id}/resolve/", deps.AdminPlatform.Alerting.ResolveEvent)
			r.With(alertsRead).Get("/silences/", deps.AdminPlatform.Alerting.ListSilences)
			r.With(alertsCreate).Post("/silences/", deps.AdminPlatform.Alerting.CreateSilence)
			r.With(alertsDelete).Delete("/silences/{id}/", deps.AdminPlatform.Alerting.DeleteSilence)
			r.With(alertsUpdate).Post("/silences/{id}/expire/", deps.AdminPlatform.Alerting.ExpireSilence)
		})
	}

}
