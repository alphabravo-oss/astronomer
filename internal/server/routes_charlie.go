package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/downstreamboundary"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// registerCharlieRoutes exposes only the local onboarding verifier/consumer.
// It lives on the authenticated router, inherits the browser CSRF middleware,
// requires an admin-scoped token and charlie:manage, and fails closed unless
// the explicitly opt-in feature.charlie setting is enabled.
//
// Authenticated Charlie traffic is not request-rate-limited. Charlie is an
// optional, tightly controlled product integration (feature gate + RBAC + mode
// authority), not a public chatbot. Cluster-agent tunnels never enter these
// routes; Product MCP and the local agent bridge are separate private paths.
func registerCharlieRoutes(r chi.Router, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	_ = rateLimit // shared router signature; Charlie deliberately does not use it
	r.Group(func(r chi.Router) {
		r.Use(downstreamboundary.MarkCharlieOrigin)
		gate := appmiddleware.FeatureGateDefault("feature.charlie", deps.CoreAuth.SettingsCache, false)
		manage := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceCharlie, rbac.VerbManage)
		admin := requireScope(iauth.ScopeAdmin)
		if deps.AdminPlatform.CharlieOnboarding != nil {
			r.With(gate, admin, manage).Post("/admin/charlie/onboarding/validate/", deps.AdminPlatform.CharlieOnboarding.Validate)
			r.With(gate, admin, manage).Post("/admin/charlie/onboarding/consume/", deps.AdminPlatform.CharlieOnboarding.Import)
		}
		if deps.AdminPlatform.CharlieAdmin != nil {
			// Status remains securely reachable after the feature gate is disabled.
			// It still requires admin scope,
			// charlie:manage, and CSRF protection.
			r.With(admin, manage).Get("/admin/charlie/status/", deps.AdminPlatform.CharlieAdmin.Status)
			r.With(admin, manage).Post("/admin/charlie/disconnect/", deps.AdminPlatform.CharlieAdmin.Disconnect)
			r.With(admin, manage).Patch("/admin/charlie/mode/", deps.AdminPlatform.CharlieAdmin.Mode)
			r.With(gate, admin, manage).Get("/admin/charlie/kubernetes-visibility/", deps.AdminPlatform.CharlieAdmin.KubernetesVisibility)
			r.With(gate, admin, manage).Put("/admin/charlie/kubernetes-visibility/", deps.AdminPlatform.CharlieAdmin.UpdateKubernetesVisibility)
			r.With(gate, admin, manage).Get("/admin/charlie/trigger-rules/", deps.AdminPlatform.CharlieAdmin.ListTriggers)
			r.With(gate, admin, manage).Get("/admin/charlie/alert-policy/", deps.AdminPlatform.CharlieAdmin.AlertPolicy)
			r.With(gate, admin, manage).Put("/admin/charlie/alert-policy/", deps.AdminPlatform.CharlieAdmin.UpdateAlertPolicy)
			r.With(gate, admin, manage).Get("/admin/charlie/alert-deliveries/", deps.AdminPlatform.CharlieAdmin.AlertDeliveryProofs)
			r.With(gate, admin, manage).Post("/admin/charlie/qualification/discovery/", deps.AdminPlatform.CharlieAdmin.DiscoveryQualification)
			r.With(gate, admin, manage).Put("/admin/charlie/action-policies/{capability}/", deps.AdminPlatform.CharlieAdmin.UpdateActionPolicy)
			r.With(gate, admin, manage).Post("/admin/charlie/trigger-rules/", deps.AdminPlatform.CharlieAdmin.CreateTrigger)
			r.With(gate, admin, manage).Patch("/admin/charlie/trigger-rules/{rule_id}/", deps.AdminPlatform.CharlieAdmin.UpdateTrigger)
			r.With(gate, admin, manage).Delete("/admin/charlie/trigger-rules/{rule_id}/", deps.AdminPlatform.CharlieAdmin.DeleteTrigger)
			r.With(gate, admin, manage).Get("/admin/charlie/trigger-events/", deps.AdminPlatform.CharlieAdmin.ListTriggerEvents)
			r.With(gate, admin, manage).Get("/admin/charlie/trigger-events/{event_id}/", deps.AdminPlatform.CharlieAdmin.GetTriggerEvent)
			r.With(gate, admin, manage).Post("/admin/charlie/trigger-events/{event_id}/retry/", deps.AdminPlatform.CharlieAdmin.RetryTriggerEvent)
			r.With(gate, admin, manage).Get("/admin/charlie/access/", deps.AdminPlatform.CharlieAdmin.Access)
			r.With(gate, admin, manage).Put("/admin/charlie/access/", deps.AdminPlatform.CharlieAdmin.UpdateAccess)
			// Diagnostics remains reachable after disable, but its handler is forced
			// onto the database-only projection and cannot contact the agent/central.
			r.With(admin, manage).Post("/admin/charlie/diagnostics/run/", deps.AdminPlatform.CharlieAdmin.Diagnostics)
		}

		read := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceCharlie, rbac.VerbRead)
		if deps.AdminPlatform.CharlieAdmin != nil {
			r.With(gate, read).Get("/charlie/activation/", deps.AdminPlatform.CharlieAdmin.Activation)
		}
		create := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceCharlie, rbac.VerbCreate)
		approve := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceCharlie, rbac.VerbApprove)
		if deps.AdminPlatform.CharlieSessions != nil {
			r.With(gate, create).Post("/charlie/sessions/", deps.AdminPlatform.CharlieSessions.Create)
			r.With(gate, read).Get("/charlie/sessions/", deps.AdminPlatform.CharlieSessions.List)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/", deps.AdminPlatform.CharlieSessions.Get)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/history/", deps.AdminPlatform.CharlieSessions.History)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/events/", deps.AdminPlatform.CharlieSessions.Events)
			r.With(gate, create).Post("/charlie/sessions/{session_id}/messages/", deps.AdminPlatform.CharlieSessions.Message)
			r.With(gate, create).Post("/charlie/sessions/{session_id}/abort/", deps.AdminPlatform.CharlieSessions.Abort)
		}
		if deps.AdminPlatform.CharlieThreads != nil {
			r.With(gate, read).Get("/charlie/commands/", deps.AdminPlatform.CharlieThreads.Commands)
			r.With(gate, read).Get("/charlie/threads/active/", deps.AdminPlatform.CharlieThreads.Active)
			r.With(gate, create).Post("/charlie/threads/new/", deps.AdminPlatform.CharlieThreads.NewChat)
			r.With(gate, read).Get("/charlie/threads/", deps.AdminPlatform.CharlieThreads.List)
			r.With(gate, create).Post("/charlie/threads/messages/", deps.AdminPlatform.CharlieThreads.Message)
			r.With(gate, read).Get("/charlie/threads/{thread_id}/history/", deps.AdminPlatform.CharlieThreads.History)
		}
		if deps.AdminPlatform.CharlieContext != nil {
			r.With(gate, read).Get("/charlie/context/search/", deps.AdminPlatform.CharlieContext.Search)
		}
		if deps.AdminPlatform.CharlieApprovals != nil {
			r.With(gate, read).Get("/charlie/approvals/", deps.AdminPlatform.CharlieApprovals.List)
			r.With(gate, approve).Post("/charlie/approvals/{approval_id}/decision/", deps.AdminPlatform.CharlieApprovals.Decide)
		}
		if deps.AdminPlatform.CharlieFindings != nil {
			update := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceCharlie, rbac.VerbUpdate)
			r.With(gate, read).Get("/charlie/findings/", deps.AdminPlatform.CharlieFindings.List)
			r.With(gate, read).Get("/charlie/findings/{finding_id}/", deps.AdminPlatform.CharlieFindings.Get)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/acknowledge/", deps.AdminPlatform.CharlieFindings.Acknowledge)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/start-remediation/", deps.AdminPlatform.CharlieFindings.StartRemediation)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/request-verification/", deps.AdminPlatform.CharlieFindings.RequestVerification)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/dismiss/", deps.AdminPlatform.CharlieFindings.Dismiss)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/resolve/", deps.AdminPlatform.CharlieFindings.Resolve)
		}
		if deps.AdminPlatform.CharlieOperations != nil {
			// All /api/v1 requests are normalized to a trailing slash before
			// chi matches them. Keep this route canonical too; otherwise both
			// the documented slashless URL and an explicitly suffixed request
			// normalize to a path that can never match and return the router's
			// generic 404 instead of operation status.
			r.With(gate, read).Get("/charlie/operations/{operation_id}/", deps.AdminPlatform.CharlieOperations.Get)
		}
	})
}
