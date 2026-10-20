package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerRBACAuditAgentRoutes(r chi.Router, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	writeRBAC := requireScope(iauth.ScopeWriteRBAC)

	if deps.CoreAuth.RBAC != nil {
		r.With(writeRBAC).Post("/projects/{id}/apply-rbac-template/", deps.CoreAuth.RBAC.ApplyProjectTemplate)
		r.Route("/rbac", func(r chi.Router) {
			r.Get("/global-roles/", deps.CoreAuth.RBAC.ListGlobalRoles)
			r.With(writeRBAC).Post("/global-roles/", deps.CoreAuth.RBAC.CreateGlobalRole)
			r.Get("/global-roles/{id}/", deps.CoreAuth.RBAC.GetGlobalRole)
			r.With(writeRBAC).Put("/global-roles/{id}/", deps.CoreAuth.RBAC.UpdateGlobalRole)
			r.With(writeRBAC).Delete("/global-roles/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRole)
			r.Get("/cluster-roles/", deps.CoreAuth.RBAC.ListClusterRoles)
			r.With(writeRBAC).Post("/cluster-roles/", deps.CoreAuth.RBAC.CreateClusterRole)
			r.Get("/cluster-roles/{id}/", deps.CoreAuth.RBAC.GetClusterRole)
			r.With(writeRBAC).Put("/cluster-roles/{id}/", deps.CoreAuth.RBAC.UpdateClusterRole)
			r.With(writeRBAC).Delete("/cluster-roles/{id}/", deps.CoreAuth.RBAC.DeleteClusterRole)
			r.Get("/project-roles/", deps.CoreAuth.RBAC.ListProjectRoles)
			r.With(writeRBAC).Post("/project-roles/", deps.CoreAuth.RBAC.CreateProjectRole)
			r.Get("/project-roles/{id}/", deps.CoreAuth.RBAC.GetProjectRole)
			r.With(writeRBAC).Put("/project-roles/{id}/", deps.CoreAuth.RBAC.UpdateProjectRole)
			r.With(writeRBAC).Delete("/project-roles/{id}/", deps.CoreAuth.RBAC.DeleteProjectRole)
			r.Get("/global-bindings/", deps.CoreAuth.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC).Post("/global-bindings/", deps.CoreAuth.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC).Delete("/global-bindings/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRoleBinding)
			r.Get("/cluster-bindings/", deps.CoreAuth.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC).Post("/cluster-bindings/", deps.CoreAuth.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC).Delete("/cluster-bindings/{id}/", deps.CoreAuth.RBAC.DeleteClusterRoleBinding)
			r.Get("/project-bindings/", deps.CoreAuth.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC).Post("/project-bindings/", deps.CoreAuth.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC).Delete("/project-bindings/{id}/", deps.CoreAuth.RBAC.DeleteProjectRoleBinding)
			// Python-named binding path aliases (so both old and new clients work).
			r.Get("/global-role-bindings/", deps.CoreAuth.RBAC.ListGlobalRoleBindings)
			r.With(writeRBAC).Post("/global-role-bindings/", deps.CoreAuth.RBAC.CreateGlobalRoleBinding)
			r.With(writeRBAC).Delete("/global-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteGlobalRoleBinding)
			r.Get("/cluster-role-bindings/", deps.CoreAuth.RBAC.ListClusterRoleBindings)
			r.With(writeRBAC).Post("/cluster-role-bindings/", deps.CoreAuth.RBAC.CreateClusterRoleBinding)
			r.With(writeRBAC).Delete("/cluster-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteClusterRoleBinding)
			r.Get("/project-role-bindings/", deps.CoreAuth.RBAC.ListProjectRoleBindings)
			r.With(writeRBAC).Post("/project-role-bindings/", deps.CoreAuth.RBAC.CreateProjectRoleBinding)
			r.With(writeRBAC).Delete("/project-role-bindings/{id}/", deps.CoreAuth.RBAC.DeleteProjectRoleBinding)
			// Current user's effective roles + permission check.
			r.Get("/my-roles/", deps.CoreAuth.RBAC.MyRoles)
			r.Get("/my-roles/check/", deps.CoreAuth.RBAC.CheckMyRole)
			r.Get("/my-permissions/", deps.CoreAuth.RBAC.MyEffectivePermissions)
			r.
				Get("/effective-permissions/{user_id}/", deps.CoreAuth.RBAC.EffectivePermissionsForUser)
			r.
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
		r.Route("/rbac/principals", func(r chi.Router) {
			r.With(rateLimit(appmiddleware.ClassSearch)).Get("/", deps.CoreAuth.Principals.Search)
			r.With(rateLimit(appmiddleware.ClassSearch), writeRBAC).Post("/materialize/", deps.CoreAuth.Principals.Materialize)
		})
	}

	// Native per-CRD RBAC rules (migration 126). Nil unless native_rbac_enabled.
	// Authoring is an RBAC-management action, so it's gated on the same
	// ResourceRBAC permission + write-RBAC scope as roles/bindings above.
	if deps.ClusterResources.NativeRBAC != nil {
		r.Route("/native-rbac-rules", func(r chi.Router) {
			r.Get("/", deps.ClusterResources.NativeRBAC.List)
			r.With(writeRBAC).Post("/", deps.ClusterResources.NativeRBAC.Create)
			r.With(writeRBAC).Delete("/{id}/", deps.ClusterResources.NativeRBAC.Delete)
		})
	}

	if deps.ClusterResources.ClusterAgent != nil {
		r.
			Get("/cluster-agents/", deps.ClusterResources.ClusterAgent.List)
		r.
			Get("/cluster-agents/{cluster_id}/", deps.ClusterResources.ClusterAgent.Get)
		r.
			Get("/cluster-agents/{cluster_id}/diagnostics/", deps.ClusterResources.ClusterAgent.Diagnostics)
		r.
			Get("/cluster-agents/{cluster_id}/diagnostics/bundle/", deps.ClusterResources.ClusterAgent.DiagnosticsBundle)
		r.
			Get("/cluster-agents/{cluster_id}/operations/", deps.ClusterResources.ClusterAgent.Operations)
		r.
			Post("/cluster-agents/{cluster_id}/self-test/", deps.ClusterResources.ClusterAgent.SelfTest)
		r.
			Post("/cluster-agents/{cluster_id}/upgrade-plan/", deps.ClusterResources.ClusterAgent.UpgradePlan)
		r.
			Post("/cluster-agents/{cluster_id}/upgrade/", deps.ClusterResources.ClusterAgent.Upgrade)
		// E3 (C2 rollout aid): cluster-admin agent posture report. Lists
		// every managed cluster whose agent still resolves to the
		// cluster-admin `admin` profile so operators can re-profile after
		// the GATE-0 fail-closed default flip. Superuser-gated inside the
		// handler (clean 401/403), same pattern as the other /admin/* reads.
		r.With(requireAuth(deps)).
			Get("/admin/agents/cluster-admin-posture/", deps.ClusterResources.ClusterAgent.ClusterAdminPosture)
	}

	if deps.AdminPlatform.Audit != nil {
		r.Route("/audit", func(r chi.Router) {
			r.Get("/", deps.AdminPlatform.Audit.List)
			r.Get("/export/", deps.AdminPlatform.Audit.Export)
			r.Post("/exports/", deps.AdminPlatform.Audit.CreateExport)
			r.Get("/exports/{id}/", deps.AdminPlatform.Audit.GetExportOperation)
			r.Get("/exports/{id}/download/", deps.AdminPlatform.Audit.DownloadExport)
			r.Get("/{id}/", deps.AdminPlatform.Audit.Get)
		})
	}

	if deps.AdminPlatform.Alerting != nil {
		// Alerting was previously mounted with no authorization at all: any
		// authenticated user could read channel delivery secrets (Slack webhook
		// URLs, PagerDuty keys), tamper with rules, and silence alerts. Gate every
		// verb on ResourceAlerts, matching the audit/rbac sibling blocks. Reads
		// need alerts:read (or :list); state changes need :update; creates :create;
		// deletes :delete.
		r.Route("/alerting", func(r chi.Router) {
			r.Get("/channels/", deps.AdminPlatform.Alerting.ListChannels)
			r.Post("/channels/", deps.AdminPlatform.Alerting.CreateChannel)
			r.Get("/channels/{id}/", deps.AdminPlatform.Alerting.GetChannel)
			r.Put("/channels/{id}/", deps.AdminPlatform.Alerting.UpdateChannel)
			r.Delete("/channels/{id}/", deps.AdminPlatform.Alerting.DeleteChannel)
			r.Post("/channels/{id}/test/", deps.AdminPlatform.Alerting.TestChannel)
			r.Get("/rules/", deps.AdminPlatform.Alerting.ListRules)
			r.Post("/rules/", deps.AdminPlatform.Alerting.CreateRule)
			r.Get("/rules/{id}/", deps.AdminPlatform.Alerting.GetRule)
			r.Put("/rules/{id}/", deps.AdminPlatform.Alerting.UpdateRule)
			r.Delete("/rules/{id}/", deps.AdminPlatform.Alerting.DeleteRule)
			r.Post("/rules/{id}/enable/", deps.AdminPlatform.Alerting.EnableRule)
			r.Post("/rules/{id}/disable/", deps.AdminPlatform.Alerting.DisableRule)
			r.Get("/events/", deps.AdminPlatform.Alerting.ListEvents)
			r.Get("/events/summary/", deps.AdminPlatform.Alerting.EventSummary)
			r.Get("/events/{id}/", deps.AdminPlatform.Alerting.GetEvent)
			r.Post("/events/{id}/acknowledge/", deps.AdminPlatform.Alerting.AcknowledgeEvent)
			r.Post("/events/{id}/resolve/", deps.AdminPlatform.Alerting.ResolveEvent)
			r.Get("/silences/", deps.AdminPlatform.Alerting.ListSilences)
			r.Post("/silences/", deps.AdminPlatform.Alerting.CreateSilence)
			r.Delete("/silences/{id}/", deps.AdminPlatform.Alerting.DeleteSilence)
			r.Post("/silences/{id}/expire/", deps.AdminPlatform.Alerting.ExpireSilence)
		})
	}

}
