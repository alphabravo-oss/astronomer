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
	if deps.Auth != nil {
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/login/", deps.Auth.Login)
		r.Post("/auth/refresh/", deps.Auth.Refresh)
		r.Post("/auth/logout/", deps.Auth.Logout)
		// SLO landing endpoint (migration 054). PUBLIC by design —
		// the IdP bounces here after tearing down its session and
		// the JWT was already revoked before the redirect was
		// issued. Sets a one-shot "logged_out" cookie + 303s to
		// /dashboard/login so the SPA renders the confirmation
		// page.
		r.Get("/auth/logout-done/", deps.Auth.LogoutDone)
		r.Get("/auth/logout-done", deps.Auth.LogoutDone)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/change-password/", deps.Auth.ChangePassword)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/me/", deps.Auth.CurrentUser)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/tokens/", deps.Auth.ListTokens)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/tokens/", deps.Auth.CreateToken)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/auth/tokens/{id}/", deps.Auth.RevokeToken)
		// Password reset (migration 047). Both endpoints are
		// PUBLIC — the request path is rate-limited under the
		// same /auth bucket as login (brute force on the email
		// enumeration vector → rate-limit), and the complete
		// path is gated by the emailed token.
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/password-reset/request/", deps.Auth.PasswordResetRequest)
		r.With(appmiddleware.LoginRateLimit(10, time.Minute)).Post("/auth/password-reset/complete/", deps.Auth.PasswordResetComplete)
	}

	// 2FA / TOTP routes (migration 043). Verify is PUBLIC — its proof
	// of identity is the challenge_token issued by Login. The other
	// endpoints require an active session (they're self-service
	// enrollment / management for the logged-in user).
	if deps.TOTP != nil {
		// Same rate-limit class as /auth/login — a brute-forcer
		// hitting verify with 1m TOTP codes would otherwise have
		// 10s windows of guess room per minute.
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Post("/auth/totp/verify/", deps.TOTP.Verify)
		// Enroll start/confirm accept EITHER a live session OR the
		// PurposeTOTPEnrollOnly challenge Login issues when MFA enrollment is
		// enforced and the user has not yet enrolled. Without the challenge
		// path a forced-enrollment user has no session and could never enroll.
		enrollAuth := enrollChallengeOrAuth(deps.JWT, deps.AuthQueries)
		r.With(enrollAuth).Post("/auth/totp/enroll/start/", deps.TOTP.EnrollStart)
		r.With(enrollAuth).Post("/auth/totp/enroll/confirm/", deps.TOTP.EnrollConfirm)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/disable/", deps.TOTP.Disable)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/totp/status/", deps.TOTP.Status)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/auth/totp/recovery-codes/regenerate/", deps.TOTP.RegenerateRecoveryCodes)
	}

	// SSO OAuth handshake. Both routes are public — Login redirects to the
	// provider, Callback validates state + exchanges the code for tokens.
	if deps.SSO != nil {
		r.Get("/auth/login/{provider}", deps.SSO.Login)
		r.Get("/auth/login/{provider}/", deps.SSO.Login)
		r.Get("/auth/callback/{provider}", deps.SSO.Callback)
		r.Get("/auth/callback/{provider}/", deps.SSO.Callback)
	}

	if deps.Resources != nil {
		// The activity feed exposes a rolling stream of operations + named
		// resources drawn from the audit log; gate it like the audit-log read
		// (requireAuth + audit_logs read/list) instead of leaving it public.
		r.With(
			requireAuth(deps.JWT, deps.AuthQueries),
			requireAnyPermission(
				deps.RBACEngine,
				deps.RBACQueries,
				permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
				permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
			),
			deprecatedAPIAlias("/api/v1/activity"),
		).Get("/activity/", deps.Resources.ListActivity)
		r.Route("/settings", func(r chi.Router) {
			r.Get("/general/", deps.Resources.GetGeneralSettings)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).
				Put("/general/", deps.Resources.UpdateGeneralSettings)
			r.Get("/sso/", deps.Resources.ListSSOProviders)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSSO, rbac.VerbCreate)).
				Post("/sso/", deps.Resources.CreateSSOProvider)
			r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSSO, rbac.VerbDelete)).
				Delete("/sso/{id}/", deps.Resources.DeleteSSOProvider)
			// Preset catalog (GitHub / Google / Azure AD / GitLab /
			// Okta). Public-readable so the login page can render
			// branded buttons before the user is authenticated.
			if deps.SSOPresets != nil {
				r.Get("/sso/presets/", deps.SSOPresets.List)
			}
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requireAnyPermission(
					deps.RBACEngine,
					deps.RBACQueries,
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbRead},
					permissionRequirement{resource: rbac.ResourceAuditLogs, verb: rbac.VerbList},
				),
			).Get("/audit-logs/", deps.Resources.ListAuditLogs)
			if deps.Monitoring != nil {
				// NEW-1: the shared Thanos/Alertmanager monitoring-stack
				// install/upgrade/replace/uninstall routes run helm against
				// the management/monitoring cluster but were never wired
				// through the GATE-0 write-scope backstop. A read-scoped API
				// token must not trigger these helm mutations. requireAuth
				// stashes the token so the scope middleware can see it (it
				// would otherwise bypass an unauthenticated request), then
				// monitoringWriteScope enforces clusters:write on the
				// mutating helm verbs (reads/preview/status pass through).
				monitoringMutate := r.With(requireAuth(deps.JWT, deps.AuthQueries), appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteClusters))
				// The remaining monitoring routes were registered on the
				// bare /settings group, which carries no requireAuth — so
				// GET /monitoring/backend/ answered 200 to an anonymous
				// caller with the backend's decoded authConfig, and both
				// preview routes rendered chart values for free. They now
				// authenticate at the router as well as authorizing in the
				// handler.
				monitoringAuthed := r.With(requireAuth(deps.JWT, deps.AuthQueries))
				monitoringAuthed.Get("/monitoring/backend/", deps.Monitoring.GetBackendConfig)
				monitoringAuthed.Get("/monitoring/sizer/", deps.Monitoring.GetMonitoringSizer)
				// PUT backend/ persists operator-supplied backend auth
				// material and POST retry/ re-enqueues helm work, so both
				// belong on the write-scope backstop alongside every other
				// monitoring mutation: a read-scoped API token whose
				// principal holds monitoring:update must not drive them.
				// This narrows those two routes for existing read-scoped
				// tokens — deliberate, and the point of the GATE-0 block.
				monitoringMutate.Put("/monitoring/backend/", deps.Monitoring.UpdateBackendConfig)
				monitoringAuthed.Get("/monitoring/operations/", deps.Monitoring.ListOperations)
				monitoringAuthed.Get("/monitoring/operations/{id}/", deps.Monitoring.GetOperation)
				monitoringMutate.Post("/monitoring/operations/{id}/retry/", deps.Monitoring.RetryOperation)
				monitoringAuthed.Get("/monitoring/thanos/status/", deps.Monitoring.GetSharedThanosStatus)
				monitoringAuthed.Post("/monitoring/thanos/preview/", deps.Monitoring.PreviewSharedThanosStack)
				monitoringMutate.Post("/monitoring/thanos/install/", deps.Monitoring.InstallSharedThanosStack)
				monitoringMutate.Put("/monitoring/thanos/upgrade/", deps.Monitoring.UpgradeSharedThanosStack)
				monitoringMutate.Post("/monitoring/thanos/replace/", deps.Monitoring.ReplaceSharedThanosStack)
				monitoringMutate.Delete("/monitoring/thanos/uninstall/", deps.Monitoring.UninstallSharedThanosStack)
				monitoringAuthed.Get("/monitoring/alertmanager/status/", deps.Monitoring.GetSharedAlertmanagerStatus)
				monitoringAuthed.Post("/monitoring/alertmanager/preview/", deps.Monitoring.PreviewSharedAlertmanager)
				monitoringMutate.Post("/monitoring/alertmanager/install/", deps.Monitoring.InstallSharedAlertmanager)
				monitoringMutate.Put("/monitoring/alertmanager/upgrade/", deps.Monitoring.UpgradeSharedAlertmanager)
				monitoringMutate.Post("/monitoring/alertmanager/replace/", deps.Monitoring.ReplaceSharedAlertmanager)
				monitoringMutate.Delete("/monitoring/alertmanager/uninstall/", deps.Monitoring.UninstallSharedAlertmanager)
				monitoringAuthed.Get("/monitoring/grafana/status/", deps.Monitoring.GetSharedGrafanaStatus)
				monitoringAuthed.Post("/monitoring/grafana/preview/", deps.Monitoring.PreviewSharedGrafanaStack)
				monitoringMutate.Post("/monitoring/grafana/install/", deps.Monitoring.InstallSharedGrafanaStack)
				monitoringMutate.Put("/monitoring/grafana/upgrade/", deps.Monitoring.UpgradeSharedGrafanaStack)
				monitoringMutate.Post("/monitoring/grafana/replace/", deps.Monitoring.ReplaceSharedGrafanaStack)
				monitoringMutate.Delete("/monitoring/grafana/uninstall/", deps.Monitoring.UninstallSharedGrafanaStack)
				lokiGate := appmiddleware.FeatureGateDefault("feature.hosted_loki", deps.SettingsCache, false)
				monitoringAuthed.With(lokiGate).Get("/monitoring/loki/status/", deps.Monitoring.GetSharedLokiStatus)
				monitoringAuthed.With(lokiGate).Post("/monitoring/loki/preview/", deps.Monitoring.PreviewSharedLokiStack)
				monitoringMutate.With(lokiGate).Post("/monitoring/loki/install/", deps.Monitoring.InstallSharedLokiStack)
				monitoringMutate.With(lokiGate).Put("/monitoring/loki/upgrade/", deps.Monitoring.UpgradeSharedLokiStack)
				monitoringMutate.With(lokiGate).Post("/monitoring/loki/replace/", deps.Monitoring.ReplaceSharedLokiStack)
				monitoringMutate.With(lokiGate).Delete("/monitoring/loki/uninstall/", deps.Monitoring.UninstallSharedLokiStack)
			}
		})
		// The user directory (usernames, emails, last-login, active state) is
		// operator PII and must not be readable unauthenticated. Reads require
		// auth + users:read/list, mirroring the write routes' users RBAC.
		r.With(
			requireAuth(deps.JWT, deps.AuthQueries),
			requireAnyPermission(
				deps.RBACEngine,
				deps.RBACQueries,
				permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbRead},
				permissionRequirement{resource: rbac.ResourceUsers, verb: rbac.VerbList},
			),
		).Get("/users/", deps.Resources.ListUsers)
		r.With(
			requireAuth(deps.JWT, deps.AuthQueries),
			requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceUsers, rbac.VerbRead),
		).Get("/users/{id}/", deps.Resources.GetUser)
	}

}

func registerAPIUtilityEntryRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies) {
	if deps.Auth != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/settings/tokens/", deps.Auth.ListTokens)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/settings/tokens/", deps.Auth.CreateToken)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/settings/tokens/{id}/", deps.Auth.RevokeToken)
	}
	if deps.StreamTickets != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/streams/tickets/", deps.StreamTickets.Create)
	}
	if deps.Monitoring != nil {
		// Ticket bounce for fleet Grafana. Mint needs the session cookie
		// (Astronomer origin). Redeem is called by grafana-proxy with the
		// ticket as the only credential — no session, no Redis, no secret key.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/observability/grafana-ticket", deps.Monitoring.MintGrafanaTicket)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/observability/grafana-ticket/", deps.Monitoring.MintGrafanaTicket)
		r.Post("/observability/grafana-ticket/redeem", deps.Monitoring.RedeemGrafanaTicket)
		r.Post("/observability/grafana-ticket/redeem/", deps.Monitoring.RedeemGrafanaTicket)
	}

	if deps.SupportBundle != nil {
		// Authenticated; the handler enforces superuser gating itself so
		// non-admins get a clean 403 rather than a generic permission
		// middleware rejection.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/support-bundle/", deps.SupportBundle.Download)
	}

	// Compliance export bundle. Same auth pattern as
	// /support-bundle/ — gated on superuser inside the handler.
	// The /export/ endpoint picks streaming vs async based on
	// the audit-row count; /exports/{id}/ polls the async job.
	if deps.CompliancePosture != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/compliance/posture/", deps.CompliancePosture.Get)
	}
	if deps.License != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/license/", deps.License.Get)
	}
	if deps.Compliance != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance/export/", deps.Compliance.Export)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance/exports/{id}/", deps.Compliance.GetExportStatus)
	}

	// Compliance baselines (migration 064 — sprint 17). Four preset
	// profiles (PCI-DSS / HIPAA / FedRAMP-Moderate / SOC2) the
	// operator can apply in one click. Superuser-gated inside the
	// handler. Apply / Revert require the *pgxpool.Pool — the
	// handler is nil when not wired and routes are simply omitted.
	if deps.ComplianceBaselines != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/", deps.ComplianceBaselines.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/active/", deps.ComplianceBaselines.Active)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/{id}/", deps.ComplianceBaselines.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baselines/{id}/diff/", deps.ComplianceBaselines.Diff)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baselines/{id}/apply/", deps.ComplianceBaselines.Apply)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/compliance-baseline-applications/", deps.ComplianceBaselines.History)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/compliance-baseline-applications/{id}/revert/", deps.ComplianceBaselines.Revert)
	}

	// Key-rotation status — surfaces how many encryption / JWT signing
	// keys are loaded. KeyCount > 1 means a rotation is mid-flight (see
	// docs/secret-rotation-runbook.md). Authenticated; the handler
	// gates on superuser internally rather than via middleware so the
	// failure mode is a clean 403.
	r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/key-status/", keyStatusHandler(cfg, deps))

	// Platform health rollup — single JSON document with cluster +
	// queue health for the top-of-dashboard banner. Authenticated;
	// no superuser gate since the dashboard banner is for everyone.
	if deps.PlatformHealth != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/platform/health-summary/", deps.PlatformHealth.Summary)
	}

}

func registerAPIOperationsEntryRoutes(r chi.Router, deps RouterDependencies) {
	// Admin queue inspector — depths + DLQ contents for the asynq
	// queues, gated on superuser inside the handler. Used by the
	// Operations tab in the dashboard.
	if deps.AdminQueues != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/queues/", deps.AdminQueues.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/queues/{queue}/dlq/", deps.AdminQueues.DLQ)
		// T28b — DLQ mutators. Retry moves an archived task back to
		// pending; Discard removes it entirely. Both gated by the
		// handler's own superuser check; audited.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/queues/{queue}/dlq/{id}/retry/", deps.AdminQueues.RetryDLQ)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/admin/queues/{queue}/dlq/{id}/", deps.AdminQueues.DiscardDLQ)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/queues/operations/{id}/", deps.AdminQueues.GetOperation)
	}

	// Durable task-outbox inspector — committed DB task intents that
	// have not yet made it to Redis/Asynq. Superuser-gated inside the
	// handler; retry moves non-delivered rows back to pending for the
	// dispatcher to send again.
	if deps.AdminTaskOutbox != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/task-outbox/", deps.AdminTaskOutbox.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/task-outbox/dead/", deps.AdminTaskOutbox.ListDead)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/task-outbox/{id}/", deps.AdminTaskOutbox.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/task-outbox/{id}/retry/", deps.AdminTaskOutbox.Retry)
	}

	// Backup-restore drill viewer — surfaces rows that the weekly
	// management-plane-restore-drill CronJob writes to
	// backup_drill_results. Gates on superuser inside the handler.
	// Used by the Operations tab + the
	// AstronomerBackupRestoreDrillStale alert's runbook.
	if deps.AdminDrill != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/backup-drill/", deps.AdminDrill.GetLatest)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/backup-drill/history/", deps.AdminDrill.ListHistory)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/management-backup/", deps.AdminDrill.GetStatus)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/management-backup/destinations/", deps.AdminDrill.CreateDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/management-backup/destinations/{id}/", deps.AdminDrill.GetDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Put("/admin/management-backup/destinations/{id}/", deps.AdminDrill.UpdateDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/admin/management-backup/destinations/{id}/", deps.AdminDrill.DeleteDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/management-backup/destinations/{id}/test/", deps.AdminDrill.TestDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/management-backup/destinations/{id}/run/", deps.AdminDrill.RunDestination)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/management-backup/operations/{id}/", deps.AdminDrill.GetManagementBackupOperation)
	}

	// Management-plane log tail (FEATURES-051226 T03) — the
	// dashboard's "show me what's happening right now" view.
	// The durable long-term path is the chart-side Fluent Bit
	// DaemonSet (deploy/chart/templates/management-logging-*.yaml).
	// Superuser-gated inside the handler.
	if deps.ManagementLogs != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/management-logs/", deps.ManagementLogs.Tail)
	}

	// Identity-group sync admin endpoints (migration 042). CRUD
	// over identity_group_mappings + admin-triggered re-sync.
	// Superuser-gated inside the handler — same pattern as the
	// other /admin/* routes — so the failure mode is a clean
	// 403 instead of a generic permission rejection.
	// SMTP admin endpoints (migration 047). Superuser-gated
	// inside the handler — same pattern as the other /admin/*
	// routes so the failure mode is a clean 403.
	if deps.SMTP != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/smtp/", deps.SMTP.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Put("/admin/smtp/", deps.SMTP.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/smtp/test/", deps.SMTP.Test)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/emails/", deps.SMTP.List)
	}

	// Outbound webhook subscriptions (migration 048). Superuser-gated
	// inside each handler so the failure mode is a clean 403. The
	// dispatcher worker is the actual sender; these endpoints only
	// manage the config + view the delivery history.
	if deps.Webhooks != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/", deps.Webhooks.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/", deps.Webhooks.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/{id}/", deps.Webhooks.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/webhooks/{id}/", deps.Webhooks.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/webhooks/{id}/", deps.Webhooks.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/test/", deps.Webhooks.Test)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/{id}/deliveries/", deps.Webhooks.Deliveries)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/webhooks/{id}/deliveries/{delivery_id}/", deps.Webhooks.GetDelivery)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/webhooks/{id}/deliveries/{delivery_id}/retry/", deps.Webhooks.RetryDelivery)
	}

	// External SIEM forwarders (migration 055). Superuser-gated
	// inside each handler; the dispatcher worker is the actual
	// sender and these endpoints only manage the config + read
	// status.
	if deps.SIEMForwarders != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/", deps.SIEMForwarders.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/", deps.SIEMForwarders.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/siem-forwarders/{id}/", deps.SIEMForwarders.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/siem-forwarders/{id}/test/", deps.SIEMForwarders.Test)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/{id}/test-operations/{operation_id}/", deps.SIEMForwarders.GetTestOperation)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/siem-forwarders/{id}/status/", deps.SIEMForwarders.Status)
	}

	// Notification-template overrides (migration 059). Superuser-
	// gated inside the handler. Same nil-safe wiring pattern as
	// the SMTP routes above — the handler is non-nil whenever the
	// notification_templates table exists (every prod boot post-
	// migration). The dispatchers consume overrides via the
	// OverrideLookup closures wired in cmd/server/main.go.
	if deps.NotificationTemplates != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/", deps.NotificationTemplates.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/{key}/", deps.NotificationTemplates.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/notification-templates/{key}/", deps.NotificationTemplates.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/notification-templates/{key}/", deps.NotificationTemplates.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/notification-templates/{key}/preview/", deps.NotificationTemplates.Preview)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/notification-templates/{key}/variables/", deps.NotificationTemplates.Variables)
	}

}

func registerAPIConfigurationEntryRoutes(r chi.Router, deps RouterDependencies) {
	// GitOps cluster registration sources (migration 060). Superuser-gated
	// inside each handler — non-admins get a clean 403. The periodic
	// gitops:sync worker is the actual reconciler; these endpoints
	// manage the source config + expose the manual-sync, dry-run
	// preview, and per-source managed-clusters readers.
	if deps.GitOps != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/", deps.GitOps.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/", deps.GitOps.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/", deps.GitOps.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/gitops-sources/{id}/", deps.GitOps.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/gitops-sources/{id}/", deps.GitOps.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/gitops-sources/{id}/sync/", deps.GitOps.Sync)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/preview/", deps.GitOps.Preview)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/gitops-sources/{id}/clusters/", deps.GitOps.ListClusters)
		// Push webhook: NOT JWT-gated (a git provider can't present a JWT) —
		// the handler authenticates via the X-Astronomer-Webhook-Secret shared
		// secret and 503s when no secret is configured.
		r.Post("/gitops/sources/{id}/webhook/", deps.GitOps.Webhook)
	}

	if deps.GroupMappings != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/group-mappings/", deps.GroupMappings.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/group-mappings/", deps.GroupMappings.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/group-mappings/{id}/", deps.GroupMappings.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/group-mappings/{id}/", deps.GroupMappings.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/users/{id}/resync-groups/", deps.GroupMappings.ResyncUser)
	}

	// Rancher-style global settings hub (migration 046).
	//
	// /admin/settings/* — superuser-gated inside the handler. The
	// branding + banner /settings/{namespace}/ readers are PUBLIC
	// because the login page renders the branding/banner BEFORE
	// the user has a session; the handler's PublicSubset method
	// gates the allowed namespace through an explicit allowlist so
	// telemetry.endpoint and feature.* never leak pre-auth.
	if deps.PlatformSettings != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/settings/", deps.PlatformSettings.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/settings/", deps.PlatformSettings.BatchUpdate)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/settings/{key}/", deps.PlatformSettings.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/settings/{key}/", deps.PlatformSettings.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/settings/{key}/", deps.PlatformSettings.Delete)
		// Pre-auth readers. Must be registered on `r` (NOT on the
		// `authenticated` subrouter mounted below) so the chi
		// dispatch hits these before falling through to the auth
		// middleware. The handler's PublicSubset enforces the
		// namespace allowlist (`branding`, `banner`,
		// `registration`). Feature flags are authenticated below.
		r.Get("/settings/branding/", deps.PlatformSettings.PublicBranding)
		r.Get("/settings/banner/", deps.PlatformSettings.PublicBanner)
		r.Get("/settings/registration/", deps.PlatformSettings.PublicRegistration)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/settings/features/", deps.PlatformSettings.Features)
	}

	// SCIM provisioning-token admin (migration 114). Mints/lists/
	// revokes the static bearer tokens the top-level /scim/v2/* chain
	// authenticates against. INSIDE the JWT auth chain and superuser-
	// gated inside the handler (same pattern as /admin/settings/*).
	// The create response returns the plaintext astro_scim_<random>
	// token exactly once; only the hash is stored.
	if deps.SCIMTokenAdmin != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Post("/admin/scim-tokens/", deps.SCIMTokenAdmin.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/scim-tokens/", deps.SCIMTokenAdmin.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Delete("/admin/scim-tokens/{id}/", deps.SCIMTokenAdmin.Delete)
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
	if deps.Clusters != nil {
		// L3: the bootstrap manifest carries the registration token in
		// plaintext; a per-IP request-rate cap is correct here (stateless,
		// no reconnect, bad token is a 404). Middleware on an existing route
		// does NOT change the route pattern, so routes.json/openapi are
		// unaffected.
		r.With(appmiddleware.LoginRateLimit(cfg.TunnelRegisterRateLimitPerMinute, time.Minute)).
			Get("/register/{token}", deps.Clusters.GetManifestByToken)
		// Short-TTL HMAC-signed manifest URL — no token in the URL,
		// the signature over (cluster_id, expiry) is the credential.
		// Registered before /register/{token} would otherwise match
		// "signed" as a token; chi's static-vs-param routing prefers
		// the literal segment so order is informational.
		// IP-keyed rate limit: the signed URL is replayable within its
		// 15m TTL and each hit mints a fresh registration token, so cap
		// the request rate the same way the auth routes do.
		r.With(appmiddleware.LoginRateLimit(5, time.Minute)).Get("/register/signed/{cluster_id}", deps.Clusters.GetSignedManifest)
		// Companion endpoint for the `curl --cacert ca.crt …`
		// variant; returns operator-uploaded PEM bundle when the
		// platform runs behind a private CA. 404 when unset.
		r.Get("/register/ca.crt", deps.Clusters.GetCABundle)
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
	if deps.PlatformDefaultTemplate != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/platform-settings/default-cluster-template/", deps.PlatformDefaultTemplate.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/platform-settings/default-cluster-template/reapply/{cluster_id}/", deps.PlatformDefaultTemplate.Reapply)
	}

	// Sprint 075 — read-only platform-baseline slug-coverage check.
	if deps.PlatformBaselineCoverage != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get(
			"/admin/platform-settings/default-cluster-template/coverage/",
			deps.PlatformBaselineCoverage.Coverage,
		)
	}

	// Per-tenant resource quotas (migration 051). Plan CRUD +
	// fleet-usage snapshot are superuser-gated inside the handler
	// (same pattern as platform_settings + smtp). The per-tenant
	// /quota/ readers are wired below alongside the projects and
	// auth groups so they inherit those RBAC/auth chains.
	if deps.Quotas != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-plans/", deps.Quotas.ListPlans)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/quota-plans/", deps.Quotas.CreatePlan)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-plans/{name}/", deps.Quotas.GetPlan)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/quota-plans/{name}/", deps.Quotas.UpdatePlan)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/quota-plans/{name}/", deps.Quotas.DeletePlan)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/quota-usage/", deps.Quotas.FleetUsage)
		// Per-tenant readers. Authentication is required; the
		// handler degrades gracefully when called for a user/
		// project that doesn't exist.
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/projects/{id}/quota/", deps.Quotas.ProjectQuota)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/auth/me/quota/", deps.Quotas.MyQuota)
	}

	// Maintenance windows (migration 057). Operator-defined time
	// windows that gate destructive ops. The handler is superuser-
	// gated inside each method so the failure mode is a clean 403.
	// Writers invalidate the in-memory window cache so operator
	// changes apply immediately rather than after the 30s TTL.
	if deps.Maintenance != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/", deps.Maintenance.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/maintenance-windows/", deps.Maintenance.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/active/", deps.Maintenance.ListActive)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/maintenance-windows/{id}/", deps.Maintenance.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/maintenance-windows/{id}/", deps.Maintenance.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/maintenance-windows/{id}/", deps.Maintenance.Delete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/deferred-operations/", deps.Maintenance.ListDeferred)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/deferred-operations/{id}/cancel/", deps.Maintenance.CancelDeferred)
	}

	// Read-audit policies (migration 063). Superuser-gated CRUD over
	// the read_audit_policies table. Writers invalidate the in-
	// process PolicyEvaluator cache so operator changes apply
	// immediately rather than after the 30s TTL.
	if deps.ReadAuditPolicies != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/read-audit-policies/", deps.ReadAuditPolicies.List)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/read-audit-policies/", deps.ReadAuditPolicies.Create)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Get)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Update)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/read-audit-policies/{id}/", deps.ReadAuditPolicies.Delete)
	}

	// Dashboard widgets (migration 058) — admin CRUD over
	// dashboard_widgets + prometheus_datasources. Superuser-gated
	// inside the handler. Writes carry the scope-write API-token
	// gate so a stolen read-only token can't reshape the dashboard
	// surface.
	if deps.Dashboards != nil {
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/dashboard-widgets/", deps.Dashboards.AdminList)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/dashboard-widgets/", deps.Dashboards.AdminCreate)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminGet)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminUpdate)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/dashboard-widgets/{id}/", deps.Dashboards.AdminDelete)
		r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/admin/prometheus-datasources/", deps.Dashboards.AdminListDatasources)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/", deps.Dashboards.AdminCreateDatasource)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Put("/admin/prometheus-datasources/{id}/", deps.Dashboards.AdminUpdateDatasource)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Delete("/admin/prometheus-datasources/{id}/", deps.Dashboards.AdminDeleteDatasource)
		r.With(requireAuth(deps.JWT, deps.AuthQueries), requireScope(iauth.ScopeAdmin)).Post("/admin/prometheus-datasources/{id}/test/", deps.Dashboards.AdminTestDatasource)
	}

}
