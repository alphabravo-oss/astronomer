package server

import (
	"time"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// registerAPIEntryRoutes owns authenticated self-service entry points,
// public login/bootstrap readers, and administrator control-plane routes.
// It runs before the authenticated catch-all subrouter is mounted so its
// deliberately public endpoints cannot accidentally inherit JWT middleware.
func registerAPIEntryRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies) {
	registerAPIIdentityEntryRoutes(r, deps)
	registerAPIUtilityEntryRoutes(r, cfg, deps)
	registerAPIOperationsEntryRoutes(r, deps)
	registerAPIConfigurationEntryRoutes(r, deps)
	registerAPIProvisioningEntryRoutes(r, cfg, deps)
}

func registerAPIIdentityEntryRoutes(r chi.Router, deps RouterDependencies) {
	if deps.CoreAuth.Auth != nil {
		r.With(appmiddleware.AuthFailureRateLimit(5, time.Minute)).Post("/auth/login/", deps.CoreAuth.Auth.Login)
		r.Post("/auth/refresh/", deps.CoreAuth.Auth.Refresh)
		r.Post("/auth/logout/", deps.CoreAuth.Auth.Logout)
		// SLO landing endpoint (migration 054). PUBLIC by design —
		// the IdP bounces here after tearing down its session and
		// the JWT was already revoked before the redirect was
		// issued. Sets a one-shot "logged_out" cookie + 303s to
		// /dashboard/login so the SPA renders the confirmation
		// page.
		r.Get("/auth/logout-done/", deps.CoreAuth.Auth.LogoutDone)
		r.Get("/auth/logout-done", deps.CoreAuth.Auth.LogoutDone)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/auth/change-password/", deps.CoreAuth.Auth.ChangePassword)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/auth/me/", deps.CoreAuth.Auth.CurrentUser)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/auth/me/preferences/", deps.CoreAuth.Auth.GetUserPreferences)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Put("/auth/me/preferences/", deps.CoreAuth.Auth.PutUserPreferences)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/auth/tokens/", deps.CoreAuth.Auth.ListTokens)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/auth/tokens/", deps.CoreAuth.Auth.CreateToken)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Delete("/auth/tokens/{id}/", deps.CoreAuth.Auth.RevokeToken)
		// Password reset (migration 047). Both endpoints are
		// PUBLIC — the request path is rate-limited under the
		// same /auth bucket as login (brute force on the email
		// enumeration vector → rate-limit), and the complete
		// path is gated by the emailed token.
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/password-reset/request/", deps.CoreAuth.Auth.PasswordResetRequest)
		r.With(appmiddleware.LoginRateLimit(10, time.Minute)).Post("/auth/password-reset/complete/", deps.CoreAuth.Auth.PasswordResetComplete)
	}

	// 2FA / TOTP routes (migration 043). Verify is PUBLIC — its proof
	// of identity is the challenge_token issued by Login. The other
	// endpoints require an active session (they're self-service
	// enrollment / management for the logged-in user).
	if deps.CoreAuth.TOTP != nil {
		// Same rate-limit class as /auth/login — a brute-forcer
		// hitting verify with 1m TOTP codes would otherwise have
		// 10s windows of guess room per minute.
		r.With(appmiddleware.AuthFailureRateLimit(5, time.Minute)).Post("/auth/totp/verify/", deps.CoreAuth.TOTP.Verify)
		// Enroll start/confirm accept EITHER a live session OR the
		// PurposeTOTPEnrollOnly challenge Login issues when MFA enrollment is
		// enforced and the user has not yet enrolled. Without the challenge
		// path a forced-enrollment user has no session and could never enroll.
		enrollAuth := enrollChallengeOrAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)
		r.With(enrollAuth).Post("/auth/totp/enroll/start/", deps.CoreAuth.TOTP.EnrollStart)
		r.With(enrollAuth).Post("/auth/totp/enroll/confirm/", deps.CoreAuth.TOTP.EnrollConfirm)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/auth/totp/disable/", deps.CoreAuth.TOTP.Disable)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/auth/totp/status/", deps.CoreAuth.TOTP.Status)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/auth/totp/recovery-codes/regenerate/", deps.CoreAuth.TOTP.RegenerateRecoveryCodes)
	}

	// SSO OAuth handshake. Both routes are public — Login redirects to the
	// provider, Callback validates state + exchanges the code for tokens.
	if deps.CoreAuth.SSO != nil {
		r.Get("/auth/login/{provider}", deps.CoreAuth.SSO.Login)
		r.Get("/auth/login/{provider}/", deps.CoreAuth.SSO.Login)
		r.Get("/auth/callback/{provider}", deps.CoreAuth.SSO.Callback)
		r.Get("/auth/callback/{provider}/", deps.CoreAuth.SSO.Callback)
	}

	if deps.ClusterResources.Resources != nil {
		// The activity feed exposes a rolling stream of operations + named
		// resources drawn from the audit log; gate it like the audit-log read
		// (requireAuth + audit_logs read/list) instead of leaving it public.
		r.With(
			requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries),
			requireAnyPermission(
				deps.CoreAuth.RBACEngine,
				deps.CoreAuth.RBACQueries,
				permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
				permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
			),
		).Get("/activity", deps.ClusterResources.Resources.ListActivity)
		r.Route("/settings", func(r chi.Router) {
			r.Get("/general/", deps.ClusterResources.Resources.GetGeneralSettings)
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).
				Put("/general/", deps.ClusterResources.Resources.UpdateGeneralSettings)
			r.Get("/sso/", deps.ClusterResources.Resources.ListSSOProviders)
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSSO, rbac.VerbCreate)).
				Post("/sso/", deps.ClusterResources.Resources.CreateSSOProvider)
			r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSSO, rbac.VerbDelete)).
				Delete("/sso/{id}/", deps.ClusterResources.Resources.DeleteSSOProvider)
			// Preset catalog (GitHub / Google / Azure AD / GitLab /
			// Okta). Public-readable so the login page can render
			// branded buttons before the user is authenticated.
			if deps.CoreAuth.SSOPresets != nil {
				r.Get("/sso/presets/", deps.CoreAuth.SSOPresets.List)
			}
			r.With(
				requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries),
				requireAnyPermission(
					deps.CoreAuth.RBACEngine,
					deps.CoreAuth.RBACQueries,
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
				),
			).Get("/audit-logs/", deps.ClusterResources.Resources.ListAuditLogs)
			if deps.ClusterResources.Monitoring != nil {
				// NEW-1: the shared Thanos/Alertmanager monitoring-stack
				// install/upgrade/replace/uninstall routes run helm against
				// the management/monitoring cluster but were never wired
				// through the GATE-0 write-scope backstop. A read-scoped API
				// token must not trigger these helm mutations. requireAuth
				// stashes the token so the scope middleware can see it (it
				// would otherwise bypass an unauthenticated request), then
				// monitoringWriteScope enforces clusters:write on the
				// mutating helm verbs (reads/preview/status pass through).
				monitoringMutate := r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteClusters))
				// The remaining monitoring routes were registered on the
				// bare /settings group, which carries no requireAuth — so
				// GET /monitoring/backend/ answered 200 to an anonymous
				// caller with the backend's decoded authConfig, and both
				// preview routes rendered chart values for free. They now
				// authenticate at the router as well as authorizing in the
				// handler.
				monitoringAuthed := r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries))
				monitoringAuthed.Get("/monitoring/backend/", deps.ClusterResources.Monitoring.GetBackendConfig)
				monitoringAuthed.Get("/monitoring/sizer/", deps.ClusterResources.Monitoring.GetMonitoringSizer)
				// PUT backend/ persists operator-supplied backend auth
				// material and POST retry/ re-enqueues helm work, so both
				// belong on the write-scope backstop alongside every other
				// monitoring mutation: a read-scoped API token whose
				// principal holds monitoring:update must not drive them.
				// This narrows those two routes for existing read-scoped
				// tokens — deliberate, and the point of the GATE-0 block.
				monitoringMutate.Put("/monitoring/backend/", deps.ClusterResources.Monitoring.UpdateBackendConfig)
				monitoringAuthed.Get("/monitoring/operations/", deps.ClusterResources.Monitoring.ListOperations)
				monitoringAuthed.Get("/monitoring/operations/{id}/", deps.ClusterResources.Monitoring.GetOperation)
				monitoringMutate.Post("/monitoring/operations/{id}/retry/", deps.ClusterResources.Monitoring.RetryOperation)
				monitoringAuthed.Get("/monitoring/thanos/status/", deps.ClusterResources.Monitoring.GetSharedThanosStatus)
				monitoringAuthed.Post("/monitoring/thanos/preview/", deps.ClusterResources.Monitoring.PreviewSharedThanosStack)
				monitoringMutate.Post("/monitoring/thanos/install/", deps.ClusterResources.Monitoring.InstallSharedThanosStack)
				monitoringMutate.Put("/monitoring/thanos/upgrade/", deps.ClusterResources.Monitoring.UpgradeSharedThanosStack)
				monitoringMutate.Post("/monitoring/thanos/replace/", deps.ClusterResources.Monitoring.ReplaceSharedThanosStack)
				monitoringMutate.Delete("/monitoring/thanos/uninstall/", deps.ClusterResources.Monitoring.UninstallSharedThanosStack)
				monitoringAuthed.Get("/monitoring/alertmanager/status/", deps.ClusterResources.Monitoring.GetSharedAlertmanagerStatus)
				monitoringAuthed.Post("/monitoring/alertmanager/preview/", deps.ClusterResources.Monitoring.PreviewSharedAlertmanager)
				monitoringMutate.Post("/monitoring/alertmanager/install/", deps.ClusterResources.Monitoring.InstallSharedAlertmanager)
				monitoringMutate.Put("/monitoring/alertmanager/upgrade/", deps.ClusterResources.Monitoring.UpgradeSharedAlertmanager)
				monitoringMutate.Post("/monitoring/alertmanager/replace/", deps.ClusterResources.Monitoring.ReplaceSharedAlertmanager)
				monitoringMutate.Delete("/monitoring/alertmanager/uninstall/", deps.ClusterResources.Monitoring.UninstallSharedAlertmanager)
				monitoringAuthed.Get("/monitoring/grafana/status/", deps.ClusterResources.Monitoring.GetSharedGrafanaStatus)
				monitoringAuthed.Post("/monitoring/grafana/preview/", deps.ClusterResources.Monitoring.PreviewSharedGrafanaStack)
				monitoringMutate.Post("/monitoring/grafana/install/", deps.ClusterResources.Monitoring.InstallSharedGrafanaStack)
				monitoringMutate.Put("/monitoring/grafana/upgrade/", deps.ClusterResources.Monitoring.UpgradeSharedGrafanaStack)
				monitoringMutate.Post("/monitoring/grafana/replace/", deps.ClusterResources.Monitoring.ReplaceSharedGrafanaStack)
				monitoringMutate.Delete("/monitoring/grafana/uninstall/", deps.ClusterResources.Monitoring.UninstallSharedGrafanaStack)
				lokiGate := appmiddleware.FeatureGateDefault("feature.hosted_loki", deps.CoreAuth.SettingsCache, false)
				monitoringAuthed.With(lokiGate).Get("/monitoring/loki/status/", deps.ClusterResources.Monitoring.GetSharedLokiStatus)
				monitoringAuthed.With(lokiGate).Post("/monitoring/loki/preview/", deps.ClusterResources.Monitoring.PreviewSharedLokiStack)
				monitoringMutate.With(lokiGate).Post("/monitoring/loki/install/", deps.ClusterResources.Monitoring.InstallSharedLokiStack)
				monitoringMutate.With(lokiGate).Put("/monitoring/loki/upgrade/", deps.ClusterResources.Monitoring.UpgradeSharedLokiStack)
				monitoringMutate.With(lokiGate).Post("/monitoring/loki/replace/", deps.ClusterResources.Monitoring.ReplaceSharedLokiStack)
				monitoringMutate.With(lokiGate).Delete("/monitoring/loki/uninstall/", deps.ClusterResources.Monitoring.UninstallSharedLokiStack)
			}
		})
		// The user directory (usernames, emails, last-login, active state) is
		// operator PII and must not be readable unauthenticated. Reads require
		// auth + users:read/list, mirroring the write routes' users RBAC.
		r.With(
			requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries),
			requireAnyPermission(
				deps.CoreAuth.RBACEngine,
				deps.CoreAuth.RBACQueries,
				permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbRead},
				permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbList},
			),
		).Get("/users/", deps.ClusterResources.Resources.ListUsers)
		r.With(
			requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries),
			requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceUsers, rbac.VerbRead),
		).Get("/users/{id}/", deps.ClusterResources.Resources.GetUser)
	}

}

func registerAPIUtilityEntryRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies) {
	if deps.CoreAuth.Auth != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/settings/tokens/", deps.CoreAuth.Auth.ListTokens)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/settings/tokens/", deps.CoreAuth.Auth.CreateToken)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Delete("/settings/tokens/{id}/", deps.CoreAuth.Auth.RevokeToken)
	}
	if deps.StreamingInternal.StreamTickets != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/streams/tickets/", deps.StreamingInternal.StreamTickets.Create)
	}
	if deps.ClusterResources.Monitoring != nil {
		// Ticket bounce for fleet Grafana. Mint needs the session cookie
		// (Astronomer origin). Redeem is called by grafana-proxy with the
		// ticket as the only credential — no session, no Redis, no secret key.
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/observability/grafana-ticket", deps.ClusterResources.Monitoring.MintGrafanaTicket)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/observability/grafana-ticket/", deps.ClusterResources.Monitoring.MintGrafanaTicket)
		r.Post("/observability/grafana-ticket/redeem", deps.ClusterResources.Monitoring.RedeemGrafanaTicket)
		r.Post("/observability/grafana-ticket/redeem/", deps.ClusterResources.Monitoring.RedeemGrafanaTicket)
	}

	if deps.AdminPlatform.SupportBundle != nil {
		// Authenticated; the handler enforces superuser gating itself. Bundle
		// collection is always a durable operation; there is no request-thread
		// path that enumerates management-cluster logs and events.
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/support-bundles/", deps.AdminPlatform.SupportBundle.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/support-bundles/{id}/", deps.AdminPlatform.SupportBundle.GetOperation)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/support-bundles/{id}/download/", deps.AdminPlatform.SupportBundle.Download)
	}

	// Compliance export bundle. Same auth pattern as support bundles — gated on
	// superuser inside the handler.
	// The /export/ endpoint picks streaming vs async based on
	// the audit-row count; /exports/{id}/ polls the async job.
	if deps.AdminPlatform.CompliancePosture != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/compliance/posture/", deps.AdminPlatform.CompliancePosture.Get)
	}
	if deps.AdminPlatform.License != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/license/", deps.AdminPlatform.License.Get)
	}
	if deps.AdminPlatform.Compliance != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance/export/", deps.AdminPlatform.Compliance.Export)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance/exports/{id}/", deps.AdminPlatform.Compliance.GetExportStatus)
	}

	// Compliance baselines (migration 064 — sprint 17). Four preset
	// profiles (PCI-DSS / HIPAA / FedRAMP-Moderate / SOC2) the
	// operator can apply in one click. Superuser-gated inside the
	// handler. Apply / Revert require the *pgxpool.Pool — the
	// handler is nil when not wired and routes are simply omitted.
	if deps.AdminPlatform.ComplianceBaselines != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance-baselines/", deps.AdminPlatform.ComplianceBaselines.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance-baselines/active/", deps.AdminPlatform.ComplianceBaselines.Active)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance-baselines/{id}/", deps.AdminPlatform.ComplianceBaselines.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance-baselines/{id}/diff/", deps.AdminPlatform.ComplianceBaselines.Diff)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baselines/{id}/apply/", deps.AdminPlatform.ComplianceBaselines.Apply)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/compliance-baseline-applications/", deps.AdminPlatform.ComplianceBaselines.History)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baseline-applications/{id}/revert/", deps.AdminPlatform.ComplianceBaselines.Revert)
	}

	// Key-rotation status — surfaces how many encryption / JWT signing
	// keys are loaded. KeyCount > 1 means a rotation is mid-flight (see
	// docs/secret-rotation-runbook.md). Authenticated; the handler
	// gates on superuser internally rather than via middleware so the
	// failure mode is a clean 403.
	r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/key-status/", keyStatusHandler(cfg, deps))

	// Platform health rollup — single JSON document with cluster +
	// queue health for the top-of-dashboard banner. Authenticated;
	// no superuser gate since the dashboard banner is for everyone.
	if deps.AdminPlatform.PlatformHealth != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/platform/health-summary/", deps.AdminPlatform.PlatformHealth.Summary)
	}

}

func registerAPIOperationsEntryRoutes(r chi.Router, deps RouterDependencies) {
	// Admin queue inspector — depths + DLQ contents for the asynq
	// queues, gated on superuser inside the handler. Used by the
	// Operations tab in the dashboard.
	if deps.AdminPlatform.AdminQueues != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/queues/", deps.AdminPlatform.AdminQueues.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/queues/{queue}/dlq/", deps.AdminPlatform.AdminQueues.DLQ)
		// T28b — DLQ mutators. Retry moves an archived task back to
		// pending; Discard removes it entirely. Both gated by the
		// handler's own superuser check; audited.
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/queues/{queue}/dlq/{id}/retry/", deps.AdminPlatform.AdminQueues.RetryDLQ)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Delete("/admin/queues/{queue}/dlq/{id}/", deps.AdminPlatform.AdminQueues.DiscardDLQ)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/queues/operations/{id}/", deps.AdminPlatform.AdminQueues.GetOperation)
	}

	// Durable task-outbox inspector — committed DB task intents that
	// have not yet made it to Redis/Asynq. Superuser-gated inside the
	// handler; retry moves non-delivered rows back to pending for the
	// dispatcher to send again.
	if deps.AdminPlatform.AdminTaskOutbox != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/task-outbox/", deps.AdminPlatform.AdminTaskOutbox.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/task-outbox/dead/", deps.AdminPlatform.AdminTaskOutbox.ListDead)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/task-outbox/{id}/", deps.AdminPlatform.AdminTaskOutbox.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/task-outbox/{id}/retry/", deps.AdminPlatform.AdminTaskOutbox.Retry)
	}

	// Backup-restore drill viewer — surfaces rows that the weekly
	// management-plane-restore-drill CronJob writes to
	// backup_drill_results. Gates on superuser inside the handler.
	// Used by the Operations tab + the
	// AstronomerBackupRestoreDrillStale alert's runbook.
	if deps.AdminPlatform.AdminDrill != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/backup-drill/", deps.AdminPlatform.AdminDrill.GetLatest)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/backup-drill/history/", deps.AdminPlatform.AdminDrill.ListHistory)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/management-backup/", deps.AdminPlatform.AdminDrill.GetStatus)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/management-backup/destinations/", deps.AdminPlatform.AdminDrill.CreateDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/management-backup/destinations/{id}/", deps.AdminPlatform.AdminDrill.GetDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Put("/admin/management-backup/destinations/{id}/", deps.AdminPlatform.AdminDrill.UpdateDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Delete("/admin/management-backup/destinations/{id}/", deps.AdminPlatform.AdminDrill.DeleteDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/management-backup/destinations/{id}/test/", deps.AdminPlatform.AdminDrill.TestDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/management-backup/destinations/{id}/run/", deps.AdminPlatform.AdminDrill.RunDestination)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/management-backup/operations/{id}/", deps.AdminPlatform.AdminDrill.GetManagementBackupOperation)
	}

	// Management-plane log tail (FEATURES-051226 T03) — the
	// dashboard's "show me what's happening right now" view.
	// The durable long-term path is the chart-side Fluent Bit
	// DaemonSet (deploy/chart/templates/management-logging-*.yaml).
	// Superuser-gated inside the handler.
	if deps.AdminPlatform.ManagementLogs != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/management-logs/", deps.AdminPlatform.ManagementLogs.Tail)
	}

	// Identity-group sync admin endpoints (migration 042). CRUD
	// over identity_group_mappings + admin-triggered re-sync.
	// Superuser-gated inside the handler — same pattern as the
	// other /admin/* routes — so the failure mode is a clean
	// 403 instead of a generic permission rejection.
	// SMTP admin endpoints (migration 047). Superuser-gated
	// inside the handler — same pattern as the other /admin/*
	// routes so the failure mode is a clean 403.
	if deps.AdminPlatform.SMTP != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/smtp/", deps.AdminPlatform.SMTP.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Put("/admin/smtp/", deps.AdminPlatform.SMTP.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/smtp/test/", deps.AdminPlatform.SMTP.Test)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/emails/", deps.AdminPlatform.SMTP.List)
	}

	// Outbound webhook subscriptions (migration 048). Superuser-gated
	// inside each handler so the failure mode is a clean 403. The
	// dispatcher worker is the actual sender; these endpoints only
	// manage the config + view the delivery history.
	if deps.AdminPlatform.Webhooks != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/webhooks/", deps.AdminPlatform.Webhooks.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/", deps.AdminPlatform.Webhooks.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/webhooks/{id}/", deps.AdminPlatform.Webhooks.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/webhooks/{id}/", deps.AdminPlatform.Webhooks.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/webhooks/{id}/", deps.AdminPlatform.Webhooks.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/test/", deps.AdminPlatform.Webhooks.Test)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/webhooks/{id}/deliveries/", deps.AdminPlatform.Webhooks.Deliveries)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/webhooks/{id}/deliveries/{delivery_id}/", deps.AdminPlatform.Webhooks.GetDelivery)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/deliveries/{delivery_id}/retry/", deps.AdminPlatform.Webhooks.RetryDelivery)
	}

	// External SIEM forwarders (migration 055). Superuser-gated
	// inside each handler; the dispatcher worker is the actual
	// sender and these endpoints only manage the config + read
	// status.
	if deps.AdminPlatform.SIEMForwarders != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/siem-forwarders/", deps.AdminPlatform.SIEMForwarders.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/", deps.AdminPlatform.SIEMForwarders.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/siem-forwarders/{id}/", deps.AdminPlatform.SIEMForwarders.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/siem-forwarders/{id}/", deps.AdminPlatform.SIEMForwarders.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/siem-forwarders/{id}/", deps.AdminPlatform.SIEMForwarders.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/{id}/test/", deps.AdminPlatform.SIEMForwarders.Test)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/siem-forwarders/{id}/test-operations/{operation_id}/", deps.AdminPlatform.SIEMForwarders.GetTestOperation)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/siem-forwarders/{id}/status/", deps.AdminPlatform.SIEMForwarders.Status)
	}

	// Notification-template overrides (migration 059). Superuser-
	// gated inside the handler. Same nil-safe wiring pattern as
	// the SMTP routes above — the handler is non-nil whenever the
	// notification_templates table exists (every prod boot post-
	// migration). The dispatchers consume overrides via the
	// OverrideLookup closures wired in cmd/server/main.go.
	if deps.AdminPlatform.NotificationTemplates != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/notification-templates/", deps.AdminPlatform.NotificationTemplates.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/notification-templates/{key}/", deps.AdminPlatform.NotificationTemplates.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/notification-templates/{key}/", deps.AdminPlatform.NotificationTemplates.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/notification-templates/{key}/", deps.AdminPlatform.NotificationTemplates.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/notification-templates/{key}/preview/", deps.AdminPlatform.NotificationTemplates.Preview)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/notification-templates/{key}/variables/", deps.AdminPlatform.NotificationTemplates.Variables)
	}

}

func registerAPIConfigurationEntryRoutes(r chi.Router, deps RouterDependencies) {
	// GitOps cluster registration sources (migration 060). Superuser-gated
	// inside each handler — non-admins get a clean 403. The periodic
	// gitops:sync worker is the actual reconciler; these endpoints
	// manage the source config + expose the manual-sync, dry-run
	// preview, and per-source managed-clusters readers.
	if deps.Delivery.GitOps != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/gitops-sources/", deps.Delivery.GitOps.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/", deps.Delivery.GitOps.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/gitops-sources/{id}/", deps.Delivery.GitOps.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/gitops-sources/{id}/", deps.Delivery.GitOps.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/gitops-sources/{id}/", deps.Delivery.GitOps.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/{id}/sync/", deps.Delivery.GitOps.Sync)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/gitops-sources/{id}/preview/", deps.Delivery.GitOps.Preview)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/gitops-sources/{id}/clusters/", deps.Delivery.GitOps.ListClusters)
		// Push webhook: GitHub authenticates with the source-specific body HMAC.
		// The trusted-real-IP middleware runs before this bounded public bucket.
		r.With(appmiddleware.LoginRateLimit(60, time.Minute)).Post("/gitops/sources/{id}/webhook/", deps.Delivery.GitOps.Webhook)
	}

	if deps.AdminPlatform.GroupMappings != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/group-mappings/", deps.AdminPlatform.GroupMappings.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/group-mappings/", deps.AdminPlatform.GroupMappings.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/group-mappings/{id}/", deps.AdminPlatform.GroupMappings.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/group-mappings/{id}/", deps.AdminPlatform.GroupMappings.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/resync-groups/", deps.AdminPlatform.GroupMappings.ResyncUser)
	}

	// Rancher-style global settings hub (migration 046).
	//
	// /admin/settings/* — superuser-gated inside the handler. The
	// branding + banner /settings/{namespace}/ readers are PUBLIC
	// because the login page renders the branding/banner BEFORE
	// the user has a session; the handler's PublicSubset method
	// gates the allowed namespace through an explicit allowlist so
	// telemetry.endpoint and feature.* never leak pre-auth.
	if deps.AdminPlatform.PlatformSettings != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/settings/", deps.AdminPlatform.PlatformSettings.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/settings/", deps.AdminPlatform.PlatformSettings.BatchUpdate)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/settings/{key}/", deps.AdminPlatform.PlatformSettings.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/settings/{key}/", deps.AdminPlatform.PlatformSettings.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/settings/{key}/", deps.AdminPlatform.PlatformSettings.Delete)
		// Pre-auth readers. Must be registered on `r` (NOT on the
		// `authenticated` subrouter mounted below) so the chi
		// dispatch hits these before falling through to the auth
		// middleware. The handler's PublicSubset enforces the
		// namespace allowlist (`branding`, `banner`,
		// `registration`). Feature flags are authenticated below.
		r.Get("/settings/branding/", deps.AdminPlatform.PlatformSettings.PublicBranding)
		r.Get("/settings/banner/", deps.AdminPlatform.PlatformSettings.PublicBanner)
		r.Get("/settings/registration/", deps.AdminPlatform.PlatformSettings.PublicRegistration)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/settings/features/", deps.AdminPlatform.PlatformSettings.Features)
	}

	// SCIM provisioning-token admin (migration 114). Mints/lists/
	// revokes the static bearer tokens the top-level /scim/v2/* chain
	// authenticates against. INSIDE the JWT auth chain and superuser-
	// gated inside the handler (same pattern as /admin/settings/*).
	// The create response returns the plaintext astro_scim_<random>
	// token exactly once; only the hash is stored.
	if deps.CoreAuth.SCIMTokenAdmin != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Post("/admin/scim-tokens/", deps.CoreAuth.SCIMTokenAdmin.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/scim-tokens/", deps.CoreAuth.SCIMTokenAdmin.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Delete("/admin/scim-tokens/{id}/", deps.CoreAuth.SCIMTokenAdmin.Delete)
	}

}

func registerAPIProvisioningEntryRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies) {
	// Rancher-style one-liner manifest fetch. Unauthenticated by
	// design: the token in the URL IS the credential, exactly
	// like the agent token embedded in the manifest it returns.
	// Lives outside the `authenticated` subrouter for the same
	// reason as the pre-auth readers above. The `.yaml` suffix
	// makes the trailing-slash middleware leave it alone, so
	// `curl -sfL <server>/api/v1/register/<token>.yaml | kubectl
	// apply -f -` works without redirect dance.
	if deps.ClusterResources.Clusters != nil {
		// L3: the bootstrap manifest carries the registration token in
		// plaintext; a per-IP request-rate cap is correct here (stateless,
		// no reconnect, bad token is a 404). Middleware on an existing route
		// does NOT change the route pattern, so routes.json/openapi are
		// unaffected.
		r.With(appmiddleware.LoginRateLimit(cfg.TunnelRegisterRateLimitPerMinute, time.Minute)).
			Get("/register/{token}", deps.ClusterResources.Clusters.GetManifestByToken)
		// Short-TTL HMAC-signed manifest URL — no token in the URL,
		// the signature over (cluster_id, expiry) is the credential.
		// Registered before /register/{token} would otherwise match
		// "signed" as a token; chi's static-vs-param routing prefers
		// the literal segment so order is informational.
		// IP-keyed rate limit: the signed URL is replayable within its
		// 15m TTL and each hit mints a fresh registration token, so cap
		// the request rate the same way the auth routes do.
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Get("/register/signed/{cluster_id}", deps.ClusterResources.Clusters.GetSignedManifest)
		// Companion endpoint for the `curl --cacert ca.crt …`
		// variant; returns operator-uploaded PEM bundle when the
		// platform runs behind a private CA. 404 when unset.
		r.Get("/register/ca.crt", deps.ClusterResources.Clusters.GetCABundle)
	}

	// Sprint 074 — platform-default cluster template. The
	// /admin/platform-settings/default-cluster-template/* surface
	// manages the auto-attach baseline (typically the seeded
	// "Platform baseline" — trivy-operator, kube-state-metrics,
	// node-exporter, fluent-bit, ingress-nginx, cert-manager,
	// gatekeeper) that the cluster
	// Create handler binds to every newly-registered cluster.
	// Superuser-gated inside the handler. Reapply takes a
	// {cluster_id} path param so an operator can back-fill an
	// existing cluster after changing the baseline.
	if deps.AdminPlatform.PlatformDefaultTemplate != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/platform-settings/default-cluster-template/", deps.AdminPlatform.PlatformDefaultTemplate.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/platform-settings/default-cluster-template/", deps.AdminPlatform.PlatformDefaultTemplate.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/platform-settings/default-cluster-template/reapply/{cluster_id}/", deps.AdminPlatform.PlatformDefaultTemplate.Reapply)
	}

	// Sprint 075 — read-only platform-baseline slug-coverage check.
	if deps.AdminPlatform.PlatformBaselineCoverage != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get(
			"/admin/platform-settings/default-cluster-template/coverage/",
			deps.AdminPlatform.PlatformBaselineCoverage.Coverage,
		)
	}

	// Per-tenant resource quotas (migration 051). Plan CRUD +
	// fleet-usage snapshot are superuser-gated inside the handler
	// (same pattern as platform_settings + smtp). The per-tenant
	// /quota/ readers are wired below alongside the projects and
	// auth groups so they inherit those RBAC/auth chains.
	if deps.AdminPlatform.Quotas != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/quota-plans/", deps.AdminPlatform.Quotas.ListPlans)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/quota-plans/", deps.AdminPlatform.Quotas.CreatePlan)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/quota-plans/{name}/", deps.AdminPlatform.Quotas.GetPlan)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/quota-plans/{name}/", deps.AdminPlatform.Quotas.UpdatePlan)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/quota-plans/{name}/", deps.AdminPlatform.Quotas.DeletePlan)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/quota-usage/", deps.AdminPlatform.Quotas.FleetUsage)
		// Per-tenant readers. Authentication is required; the
		// handler degrades gracefully when called for a user/
		// project that doesn't exist.
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/projects/{id}/quota/", deps.AdminPlatform.Quotas.ProjectQuota)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/auth/me/quota/", deps.AdminPlatform.Quotas.MyQuota)
	}

	// Maintenance windows (migration 057). Operator-defined time
	// windows that gate destructive ops. The handler is superuser-
	// gated inside each method so the failure mode is a clean 403.
	// Writers invalidate the in-memory window cache so operator
	// changes apply immediately rather than after the 30s TTL.
	if deps.AdminPlatform.Maintenance != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/maintenance-windows/", deps.AdminPlatform.Maintenance.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/maintenance-windows/", deps.AdminPlatform.Maintenance.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/maintenance-windows/active/", deps.AdminPlatform.Maintenance.ListActive)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/maintenance-windows/{id}/", deps.AdminPlatform.Maintenance.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/maintenance-windows/{id}/", deps.AdminPlatform.Maintenance.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/maintenance-windows/{id}/", deps.AdminPlatform.Maintenance.Delete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/deferred-operations/", deps.AdminPlatform.Maintenance.ListDeferred)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/deferred-operations/{id}/cancel/", deps.AdminPlatform.Maintenance.CancelDeferred)
	}

	// Read-audit policies (migration 063). Superuser-gated CRUD over
	// the read_audit_policies table. Writers invalidate the in-
	// process PolicyEvaluator cache so operator changes apply
	// immediately rather than after the 30s TTL.
	if deps.AdminPlatform.ReadAuditPolicies != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/read-audit-policies/", deps.AdminPlatform.ReadAuditPolicies.List)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/read-audit-policies/", deps.AdminPlatform.ReadAuditPolicies.Create)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/read-audit-policies/{id}/", deps.AdminPlatform.ReadAuditPolicies.Get)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/read-audit-policies/{id}/", deps.AdminPlatform.ReadAuditPolicies.Update)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/read-audit-policies/{id}/", deps.AdminPlatform.ReadAuditPolicies.Delete)
	}

	// Dashboard widgets (migration 058) — admin CRUD over
	// dashboard_widgets + prometheus_datasources. Superuser-gated
	// inside the handler. Writes carry the scope-write API-token
	// gate so a stolen read-only token can't reshape the dashboard
	// surface.
	if deps.AdminPlatform.Dashboards != nil {
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/dashboard-widgets/", deps.AdminPlatform.Dashboards.AdminList)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/dashboard-widgets/", deps.AdminPlatform.Dashboards.AdminCreate)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/dashboard-widgets/{id}/", deps.AdminPlatform.Dashboards.AdminGet)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/dashboard-widgets/{id}/", deps.AdminPlatform.Dashboards.AdminUpdate)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/dashboard-widgets/{id}/", deps.AdminPlatform.Dashboards.AdminDelete)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)).Get("/admin/prometheus-datasources/", deps.AdminPlatform.Dashboards.AdminListDatasources)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/", deps.AdminPlatform.Dashboards.AdminCreateDatasource)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/prometheus-datasources/{id}/", deps.AdminPlatform.Dashboards.AdminUpdateDatasource)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/prometheus-datasources/{id}/", deps.AdminPlatform.Dashboards.AdminDeleteDatasource)
		r.With(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/{id}/test/", deps.AdminPlatform.Dashboards.AdminTestDatasource)
	}

}
