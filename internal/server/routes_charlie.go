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
		gate := appmiddleware.FeatureGateDefault("feature.charlie", deps.SettingsCache, false)
		manage := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbManage)
		admin := requireScope(iauth.ScopeAdmin)
		if deps.CharlieOnboarding != nil {
			r.With(gate, admin, manage).Post("/admin/charlie/onboarding/validate/", deps.CharlieOnboarding.Validate)
			r.With(gate, admin, manage).Post("/admin/charlie/onboarding/consume/", deps.CharlieOnboarding.Import)
		}
		if deps.CharlieAdmin != nil {
			// Status remains securely reachable after the feature gate is disabled.
			// It still requires admin scope,
			// charlie:manage, and CSRF protection.
			r.With(admin, manage).Get("/admin/charlie/status/", deps.CharlieAdmin.Status)
			r.With(admin, manage).Patch("/admin/charlie/mode/", deps.CharlieAdmin.Mode)
			r.With(gate, admin, manage).Get("/admin/charlie/kubernetes-visibility/", deps.CharlieAdmin.KubernetesVisibility)
			r.With(gate, admin, manage).Put("/admin/charlie/kubernetes-visibility/", deps.CharlieAdmin.UpdateKubernetesVisibility)
			r.With(gate, admin, manage).Get("/admin/charlie/trigger-rules/", deps.CharlieAdmin.ListTriggers)
			r.With(gate, admin, manage).Get("/admin/charlie/alert-policy/", deps.CharlieAdmin.AlertPolicy)
			r.With(gate, admin, manage).Put("/admin/charlie/alert-policy/", deps.CharlieAdmin.UpdateAlertPolicy)
			r.With(gate, admin, manage).Get("/admin/charlie/alert-deliveries/", deps.CharlieAdmin.AlertDeliveryProofs)
			r.With(gate, admin, manage).Post("/admin/charlie/qualification/discovery/", deps.CharlieAdmin.DiscoveryQualification)
			r.With(gate, admin, manage).Put("/admin/charlie/action-policies/{capability}/", deps.CharlieAdmin.UpdateActionPolicy)
			r.With(gate, admin, manage).Post("/admin/charlie/trigger-rules/", deps.CharlieAdmin.CreateTrigger)
			r.With(gate, admin, manage).Patch("/admin/charlie/trigger-rules/{rule_id}/", deps.CharlieAdmin.UpdateTrigger)
			r.With(gate, admin, manage).Delete("/admin/charlie/trigger-rules/{rule_id}/", deps.CharlieAdmin.DeleteTrigger)
			r.With(gate, admin, manage).Get("/admin/charlie/trigger-events/", deps.CharlieAdmin.ListTriggerEvents)
			r.With(gate, admin, manage).Post("/admin/charlie/trigger-events/{event_id}/retry/", deps.CharlieAdmin.RetryTriggerEvent)
			r.With(gate, admin, manage).Get("/admin/charlie/access/", deps.CharlieAdmin.Access)
			r.With(gate, admin, manage).Put("/admin/charlie/access/", deps.CharlieAdmin.UpdateAccess)
			// Diagnostics remains reachable after disable, but its handler is forced
			// onto the database-only projection and cannot contact the agent/central.
			r.With(admin, manage).Post("/admin/charlie/diagnostics/run/", deps.CharlieAdmin.Diagnostics)
		}

		read := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbRead)
		create := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbCreate)
		approve := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbApprove)
		if deps.CharlieSessions != nil {
			r.With(gate, create).Post("/charlie/sessions/", deps.CharlieSessions.Create)
			r.With(gate, read).Get("/charlie/sessions/", deps.CharlieSessions.List)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/", deps.CharlieSessions.Get)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/history/", deps.CharlieSessions.History)
			r.With(gate, read).Get("/charlie/sessions/{session_id}/events/", deps.CharlieSessions.Events)
			r.With(gate, create).Post("/charlie/sessions/{session_id}/messages/", deps.CharlieSessions.Message)
			r.With(gate, create).Post("/charlie/sessions/{session_id}/abort/", deps.CharlieSessions.Abort)
		}
		if deps.CharlieThreads != nil {
			r.With(gate, read).Get("/charlie/commands/", deps.CharlieThreads.Commands)
			r.With(gate, read).Get("/charlie/threads/active/", deps.CharlieThreads.Active)
			r.With(gate, create).Post("/charlie/threads/new/", deps.CharlieThreads.NewChat)
			r.With(gate, read).Get("/charlie/threads/", deps.CharlieThreads.List)
			r.With(gate, create).Post("/charlie/threads/messages/", deps.CharlieThreads.Message)
			r.With(gate, read).Get("/charlie/threads/{thread_id}/history/", deps.CharlieThreads.History)
		}
		if deps.CharlieContext != nil {
			r.With(gate, read).Get("/charlie/context/search/", deps.CharlieContext.Search)
		}
		if deps.CharlieApprovals != nil {
			r.With(gate, read).Get("/charlie/approvals/", deps.CharlieApprovals.List)
			r.With(gate, approve).Post("/charlie/approvals/{approval_id}/decision/", deps.CharlieApprovals.Decide)
		}
		if deps.CharlieFindings != nil {
			update := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbUpdate)
			r.With(gate, read).Get("/charlie/findings/", deps.CharlieFindings.List)
			r.With(gate, read).Get("/charlie/findings/{finding_id}/", deps.CharlieFindings.Get)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/acknowledge/", deps.CharlieFindings.Acknowledge)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/start-remediation/", deps.CharlieFindings.StartRemediation)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/request-verification/", deps.CharlieFindings.RequestVerification)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/dismiss/", deps.CharlieFindings.Dismiss)
			r.With(gate, update).Post("/charlie/findings/{finding_id}/resolve/", deps.CharlieFindings.Resolve)
		}
		if deps.CharlieOperations != nil {
			// All /api/v1 requests are normalized to a trailing slash before
			// chi matches them. Keep this route canonical too; otherwise both
			// the documented slashless URL and an explicitly suffixed request
			// normalize to a path that can never match and return the router's
			// generic 404 instead of operation status.
			r.With(gate, read).Get("/charlie/operations/{operation_id}/", deps.CharlieOperations.Get)
		}
	})
}
