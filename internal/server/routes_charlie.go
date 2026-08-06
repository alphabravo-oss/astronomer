package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// registerCharlieRoutes exposes only the local onboarding verifier/consumer.
// It lives on the authenticated router, inherits the browser CSRF middleware,
// requires an admin-scoped token and charlie:manage, and fails closed unless
// the explicitly opt-in feature.charlie setting is enabled.
func registerCharlieRoutes(r chi.Router, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	gate := appmiddleware.FeatureGateDefault("feature.charlie", deps.SettingsCache, false)
	manage := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbManage)
	admin := requireScope(iauth.ScopeAdmin)
	if deps.CharlieOnboarding != nil {
		r.With(gate, admin, manage).Post("/admin/charlie/onboarding/validate/", deps.CharlieOnboarding.Validate)
		r.With(gate, admin, manage).Post("/admin/charlie/onboarding/consume/", deps.CharlieOnboarding.Import)
	}
	if deps.CharlieAdmin != nil {
		adminLimited := rateLimit(appmiddleware.ClassCharlieChat)
		// Status and fail-safe cleanup remain securely reachable after the
		// feature gate is disabled. They still require admin scope,
		// charlie:manage, CSRF protection, and rate limiting.
		r.With(admin, manage, adminLimited).Get("/admin/charlie/status/", deps.CharlieAdmin.Status)
		r.With(admin, manage, adminLimited).Post("/admin/charlie/disconnect/", deps.CharlieAdmin.Disconnect)
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/agent/install/", deps.CharlieAdmin.Install)
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/agent/upgrade/", deps.CharlieAdmin.ReplacementAction("upgrade"))
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/agent/rollback/", deps.CharlieAdmin.ReplacementAction("rollback"))
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/agent/rotate/", deps.CharlieAdmin.ReplacementAction("rotate"))
		r.With(admin, manage, adminLimited).Post("/admin/charlie/agent/uninstall/", deps.CharlieAdmin.Uninstall)
		r.With(admin, manage, adminLimited).Patch("/admin/charlie/mode/", deps.CharlieAdmin.Mode)
		r.With(gate, admin, manage, adminLimited).Get("/admin/charlie/trigger-rules/", deps.CharlieAdmin.ListTriggers)
		r.With(gate, admin, manage, adminLimited).Put("/admin/charlie/action-policies/{capability}/", deps.CharlieAdmin.UpdateActionPolicy)
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/trigger-rules/", deps.CharlieAdmin.CreateTrigger)
		r.With(gate, admin, manage, adminLimited).Patch("/admin/charlie/trigger-rules/{rule_id}/", deps.CharlieAdmin.UpdateTrigger)
		r.With(gate, admin, manage, adminLimited).Delete("/admin/charlie/trigger-rules/{rule_id}/", deps.CharlieAdmin.DeleteTrigger)
		r.With(gate, admin, manage, adminLimited).Get("/admin/charlie/trigger-events/", deps.CharlieAdmin.ListTriggerEvents)
		r.With(gate, admin, manage, adminLimited).Post("/admin/charlie/trigger-events/{event_id}/retry/", deps.CharlieAdmin.RetryTriggerEvent)
		r.With(gate, admin, manage, adminLimited).Get("/admin/charlie/access/", deps.CharlieAdmin.Access)
		r.With(gate, admin, manage, adminLimited).Put("/admin/charlie/access/", deps.CharlieAdmin.UpdateAccess)
		// Diagnostics remains reachable after disable, but its handler is forced
		// onto the database-only projection and cannot contact the agent/central.
		r.With(admin, manage, adminLimited).Post("/admin/charlie/diagnostics/run/", deps.CharlieAdmin.Diagnostics)
	}

	read := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbRead)
	create := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbCreate)
	approve := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbApprove)
	limited := rateLimit(appmiddleware.ClassCharlieChat)
	sessionLimits := appmiddleware.CharlieSessionLimits()
	if deps.CharlieSessions != nil {
		r.With(gate, limited, sessionLimits, create).Post("/charlie/sessions/", deps.CharlieSessions.Create)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/sessions/", deps.CharlieSessions.List)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/sessions/{session_id}/", deps.CharlieSessions.Get)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/sessions/{session_id}/history/", deps.CharlieSessions.History)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/sessions/{session_id}/events/", deps.CharlieSessions.Events)
		r.With(gate, limited, sessionLimits, create).Post("/charlie/sessions/{session_id}/messages/", deps.CharlieSessions.Message)
		r.With(gate, limited, sessionLimits, create).Post("/charlie/sessions/{session_id}/abort/", deps.CharlieSessions.Abort)
	}
	if deps.CharlieContext != nil {
		r.With(gate, limited, sessionLimits, read).Get("/charlie/context/search/", deps.CharlieContext.Search)
	}
	if deps.CharlieApprovals != nil {
		r.With(gate, limited, sessionLimits, read).Get("/charlie/approvals/", deps.CharlieApprovals.List)
		r.With(gate, limited, sessionLimits, approve).Post("/charlie/approvals/{approval_id}/decision/", deps.CharlieApprovals.Decide)
	}
	if deps.CharlieFindings != nil {
		update := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCharlie, rbac.VerbUpdate)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/findings/", deps.CharlieFindings.List)
		r.With(gate, limited, sessionLimits, read).Get("/charlie/findings/{finding_id}/", deps.CharlieFindings.Get)
		r.With(gate, limited, sessionLimits, update).Post("/charlie/findings/{finding_id}/acknowledge/", deps.CharlieFindings.Acknowledge)
		r.With(gate, limited, sessionLimits, update).Post("/charlie/findings/{finding_id}/start-remediation/", deps.CharlieFindings.StartRemediation)
		r.With(gate, limited, sessionLimits, update).Post("/charlie/findings/{finding_id}/request-verification/", deps.CharlieFindings.RequestVerification)
		r.With(gate, limited, sessionLimits, update).Post("/charlie/findings/{finding_id}/dismiss/", deps.CharlieFindings.Dismiss)
		r.With(gate, limited, sessionLimits, update).Post("/charlie/findings/{finding_id}/resolve/", deps.CharlieFindings.Resolve)
	}
	if deps.CharlieOperations != nil {
		r.With(gate, limited, sessionLimits, read).Get("/charlie/operations/{operation_id}", deps.CharlieOperations.Get)
	}
}
