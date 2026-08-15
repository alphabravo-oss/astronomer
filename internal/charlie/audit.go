package charlie

const (
	AuditResourceConnection  = "charlie_connection"
	AuditResourceCertificate = "charlie_certificate"
	AuditResourceAgent       = "charlie_agent"
	AuditResourceMode        = "charlie_mode"
	AuditResourceSession     = "charlie_session"
	AuditResourceTrigger     = "charlie_trigger"
	AuditResourceApproval    = "charlie_approval"
	AuditResourceMCPDecision = "charlie_mcp_decision"
	AuditResourceFinding     = "charlie_finding"
	AuditResourceFeature     = "charlie_feature"
	AuditResourceDelegation  = "charlie_delegation"
)

var AuditActions = []string{
	"charlie.connection.onboarding_validated",
	"charlie.connection.activated",
	"charlie.connection.disconnected",
	"charlie.certificate.issued",
	"charlie.certificate.rotated",
	"charlie.agent.installed",
	"charlie.agent.upgraded",
	"charlie.agent.uninstalled",
	"charlie.mode.requested",
	"charlie.mode.verified",
	"charlie.mode.emergency_disabled",
	"charlie.kubernetes_visibility.update",
	"charlie.feature.enabled",
	"charlie.feature.disabled",
	"charlie.delegation.issued",
	"charlie.session.created",
	"charlie.session.aborted",
	"charlie.trigger.dispatched",
	"charlie.trigger.dead_lettered",
	"charlie.approval.approved",
	"charlie.approval.rejected",
	"charlie.approval.expired",
	"charlie.mcp.allowed",
	"charlie.mcp.denied",
	"charlie.finding.created",
	"charlie.finding.acknowledged",
	"charlie.finding.remediation_started",
	"charlie.finding.verification_requested",
	"charlie.finding.dismissed",
	"charlie.finding.resolved",
}
