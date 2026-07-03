package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerRBACAuditAgentRoutes(r chi.Router, deps RouterDependencies) {
	writeRBAC := requireScope(iauth.ScopeWriteRBAC)

	if deps.RBAC != nil {
		r.Route("/rbac", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/", deps.RBAC.ListGlobalRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-roles/", deps.RBAC.CreateGlobalRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-roles/{id}/", deps.RBAC.GetGlobalRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/global-roles/{id}/", deps.RBAC.UpdateGlobalRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-roles/{id}/", deps.RBAC.DeleteGlobalRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/", deps.RBAC.ListClusterRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-roles/", deps.RBAC.CreateClusterRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-roles/{id}/", deps.RBAC.GetClusterRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/cluster-roles/{id}/", deps.RBAC.UpdateClusterRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-roles/{id}/", deps.RBAC.DeleteClusterRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/", deps.RBAC.ListProjectRoles)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-roles/", deps.RBAC.CreateProjectRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-roles/{id}/", deps.RBAC.GetProjectRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbUpdate)).Put("/project-roles/{id}/", deps.RBAC.UpdateProjectRole)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-roles/{id}/", deps.RBAC.DeleteProjectRole)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-bindings/", deps.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-bindings/", deps.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-bindings/{id}/", deps.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-bindings/", deps.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-bindings/", deps.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-bindings/{id}/", deps.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-bindings/", deps.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-bindings/", deps.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-bindings/{id}/", deps.RBAC.DeleteProjectRoleBinding)
			// Python-named binding path aliases (so both old and new clients work).
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/global-role-bindings/", deps.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/global-role-bindings/", deps.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/global-role-bindings/{id}/", deps.RBAC.DeleteGlobalRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/cluster-role-bindings/", deps.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/cluster-role-bindings/", deps.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/cluster-role-bindings/{id}/", deps.RBAC.DeleteClusterRoleBinding)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/project-role-bindings/", deps.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/project-role-bindings/", deps.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/project-role-bindings/{id}/", deps.RBAC.DeleteProjectRoleBinding)
			// Current user's effective roles + permission check.
			r.Get("/my-roles/", deps.RBAC.MyRoles)
			r.Get("/my-roles/check/", deps.RBAC.CheckMyRole)
			r.Get("/my-permissions/", deps.RBAC.MyEffectivePermissions)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).
				Get("/effective-permissions/{user_id}/", deps.RBAC.EffectivePermissionsForUser)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).
				Post("/permission-preview/", deps.RBAC.PermissionPreview)
			// T1.1 — built-in role-templates catalog. Any authed user
			// can read; no rbac:read needed because the catalog is
			// static metadata about what the platform offers, not who
			// has what.
			r.Get("/templates/", deps.RBAC.ListTemplates)
			r.Get("/templates/{name}/", deps.RBAC.GetTemplate)
		})
	}

	// Native per-CRD RBAC rules (migration 126). Nil unless native_rbac_enabled.
	// Authoring is an RBAC-management action, so it's gated on the same
	// ResourceRBAC permission + write-RBAC scope as roles/bindings above.
	if deps.NativeRBAC != nil {
		r.Route("/native-rbac-rules", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbRead)).Get("/", deps.NativeRBAC.List)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbCreate)).Post("/", deps.NativeRBAC.Create)
			r.With(writeRBAC, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceRBAC, rbac.VerbDelete)).Delete("/{id}/", deps.NativeRBAC.Delete)
		})
	}

	if deps.AgentFleet != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/agents/fleet/", deps.AgentFleet.List)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/agents/fleet/{cluster_id}/diagnostics/", deps.AgentFleet.Diagnostics)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/agents/fleet/{cluster_id}/diagnostics/bundle/", deps.AgentFleet.DiagnosticsBundle)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbRead)).
			Get("/agents/fleet/{cluster_id}/operations/", deps.AgentFleet.Operations)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/agents/fleet/{cluster_id}/self-test/", deps.AgentFleet.SelfTest)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/agents/fleet/{cluster_id}/upgrade-plan/", deps.AgentFleet.UpgradePlan)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAgents, rbac.VerbUpdate)).
			Post("/agents/fleet/{cluster_id}/upgrade/", deps.AgentFleet.Upgrade)
		// E3 (C2 rollout aid): cluster-admin agent posture report. Lists
		// every managed cluster whose agent still resolves to the
		// cluster-admin `admin` profile so operators can re-profile after
		// the GATE-0 fail-closed default flip. Superuser-gated inside the
		// handler (clean 401/403), same pattern as the other /admin/* reads.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).
			Get("/admin/agents/cluster-admin-posture/", deps.AgentFleet.ClusterAdminPosture)
	}

	if deps.Audit != nil {
		auditReadOrList := requireAnyPermission(
			deps.RBACEngine,
			deps.RBACQueries,
			permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
		)
		r.Route("/audit", func(r chi.Router) {
			r.With(auditReadOrList).Get("/", deps.Audit.List)
			r.With(auditReadOrList).Get("/export/", deps.Audit.Export)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAuditLogs, rbac.VerbRead)).Get("/{id}/", deps.Audit.Get)
		})
	}

	if deps.Alerting != nil {
		// Alerting was previously mounted with no authorization at all: any
		// authenticated user could read channel delivery secrets (Slack webhook
		// URLs, PagerDuty keys), tamper with rules, and silence alerts. Gate every
		// verb on ResourceAlerts, matching the audit/rbac sibling blocks. Reads
		// need alerts:read (or :list); state changes need :update; creates :create;
		// deletes :delete.
		alertsRead := requireAnyPermission(
			deps.RBACEngine,
			deps.RBACQueries,
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbList},
		)
		alertsCreate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbCreate)
		alertsUpdate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbUpdate)
		alertsDelete := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbDelete)
		r.Route("/alerting", func(r chi.Router) {
			r.With(alertsRead).Get("/channels/", deps.Alerting.ListChannels)
			r.With(alertsCreate).Post("/channels/", deps.Alerting.CreateChannel)
			r.With(alertsRead).Get("/channels/{id}/", deps.Alerting.GetChannel)
			r.With(alertsUpdate).Put("/channels/{id}/", deps.Alerting.UpdateChannel)
			r.With(alertsDelete).Delete("/channels/{id}/", deps.Alerting.DeleteChannel)
			r.With(alertsUpdate).Post("/channels/{id}/test/", deps.Alerting.TestChannel)
			r.With(alertsRead).Get("/rules/", deps.Alerting.ListRules)
			r.With(alertsCreate).Post("/rules/", deps.Alerting.CreateRule)
			r.With(alertsRead).Get("/rules/{id}/", deps.Alerting.GetRule)
			r.With(alertsUpdate).Put("/rules/{id}/", deps.Alerting.UpdateRule)
			r.With(alertsDelete).Delete("/rules/{id}/", deps.Alerting.DeleteRule)
			r.With(alertsUpdate).Post("/rules/{id}/enable/", deps.Alerting.EnableRule)
			r.With(alertsUpdate).Post("/rules/{id}/disable/", deps.Alerting.DisableRule)
			r.With(alertsRead).Get("/events/", deps.Alerting.ListEvents)
			r.With(alertsRead).Get("/events/{id}/", deps.Alerting.GetEvent)
			r.With(alertsUpdate).Post("/events/{id}/acknowledge/", deps.Alerting.AcknowledgeEvent)
			r.With(alertsUpdate).Post("/events/{id}/resolve/", deps.Alerting.ResolveEvent)
			r.With(alertsRead).Get("/silences/", deps.Alerting.ListSilences)
			r.With(alertsCreate).Post("/silences/", deps.Alerting.CreateSilence)
			r.With(alertsDelete).Delete("/silences/{id}/", deps.Alerting.DeleteSilence)
			r.With(alertsUpdate).Post("/silences/{id}/expire/", deps.Alerting.ExpireSilence)
		})
		// Python-named alerts/* alias paths for the frontend's expected URLs.
		r.Route("/alerts", func(r chi.Router) {
			r.With(alertsUpdate).Post("/rules/{id}/enable/", deps.Alerting.EnableRule)
			r.With(alertsUpdate).Post("/rules/{id}/disable/", deps.Alerting.DisableRule)
			r.With(alertsUpdate).Post("/silences/{id}/expire/", deps.Alerting.ExpireSilence)
		})
	}

}
