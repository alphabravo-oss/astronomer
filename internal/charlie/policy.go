package charlie

import "time"

// DenialCode is intentionally content-free and stable for audit, metrics,
// findings, and the Product Bridge/MCP error envelope.
type DenialCode string

const (
	DeniedFeatureDisabled       DenialCode = "feature_disabled"
	DeniedConnectionInactive    DenialCode = "connection_inactive"
	DeniedEmergencyDisabled     DenialCode = "emergency_disabled"
	DeniedModeDisabled          DenialCode = "mode_disabled"
	DeniedDestructive           DenialCode = "destructive_capability"
	DeniedDisclosureChanged     DenialCode = "disclosure_changed"
	DeniedAuthorization         DenialCode = "authorization_denied"
	DeniedAuditUnavailable      DenialCode = "audit_unavailable"
	DeniedReadOnlyWrite         DenialCode = "read_only_write"
	DeniedApprovalRequired      DenialCode = "approval_required"
	DeniedApprovalInvalid       DenialCode = "approval_invalid"
	DeniedNotAutoEligible       DenialCode = "not_auto_eligible"
	DeniedNotAllowlisted        DenialCode = "not_allowlisted"
	DeniedScope                 DenialCode = "scope_denied"
	DeniedBudget                DenialCode = "budget_exhausted"
	DeniedCooldown              DenialCode = "cooldown_active"
	DeniedCircuitOpen           DenialCode = "circuit_open"
	DeniedPrecondition          DenialCode = "precondition_failed"
	DeniedIdempotency           DenialCode = "idempotency_required"
	DeniedVerification          DenialCode = "verification_required"
	DeniedStaleFencing          DenialCode = "stale_fencing_epoch"
	DeniedAmbiguousPriorAttempt DenialCode = "ambiguous_prior_attempt"
	DeniedMaintenance           DenialCode = "maintenance_window"
)

// authorityDenialCodes is the complete policy-decision vocabulary persisted
// by charlie.action.denied and emitted by the bounded operational serializer.
// Keep it synchronized with audit_contract.json; the contract test enforces
// exact set equality so a new denial cannot silently miss audit coverage.
var authorityDenialCodes = [...]DenialCode{
	DeniedFeatureDisabled,
	DeniedConnectionInactive,
	DeniedEmergencyDisabled,
	DeniedModeDisabled,
	DeniedDestructive,
	DeniedDisclosureChanged,
	DeniedAuthorization,
	DeniedAuditUnavailable,
	DeniedReadOnlyWrite,
	DeniedApprovalRequired,
	DeniedApprovalInvalid,
	DeniedNotAutoEligible,
	DeniedNotAllowlisted,
	DeniedScope,
	DeniedBudget,
	DeniedCooldown,
	DeniedCircuitOpen,
	DeniedPrecondition,
	DeniedIdempotency,
	DeniedVerification,
	DeniedStaleFencing,
	DeniedAmbiguousPriorAttempt,
	DeniedMaintenance,
}

func isAuthorityDenialCode(code DenialCode) bool {
	for _, candidate := range authorityDenialCodes {
		if code == candidate {
			return true
		}
	}
	return false
}

const (
	ReasonCapabilityDestructive DenialCode = "capability_destructive"
	ReasonProductDisabled       DenialCode = "product_disabled"
	ReasonDeploymentDisabled    DenialCode = "deployment_disabled"
	ReasonStaleLeadership       DenialCode = "stale_leadership"
	ReasonDisclosureDrift       DenialCode = "disclosure_drift"
	ReasonProductRBACDenied     DenialCode = "product_rbac_denied"
	ReasonScopeDenied           DenialCode = "scope_denied"
	ReasonReadOnly              DenialCode = "read_only"
	ReasonNonAutoEligible       DenialCode = "non_auto_eligible"
	ReasonApprovalRequired      DenialCode = "approval_required"
	ReasonApprovalExpired       DenialCode = "approval_expired"
	ReasonApprovalRejected      DenialCode = "approval_rejected"
	ReasonAllowlistDenied       DenialCode = "allowlist_denied"
	ReasonSafetyBudgetExceeded  DenialCode = "safety_budget_exceeded"
	ReasonCooldownActive        DenialCode = "cooldown_active"
	ReasonMaintenanceWindow     DenialCode = "maintenance_window"
	ReasonPreconditionFailed    DenialCode = "precondition_failed"
	ReasonCircuitBreakerOpen    DenialCode = "circuit_breaker_open"
	ReasonIdempotencyConflict   DenialCode = "idempotency_conflict"
	ReasonAmbiguousPriorAttempt DenialCode = "ambiguous_prior_attempt"
	ReasonExecutionFailed       DenialCode = "execution_failed"
	ReasonCentralUnavailable    DenialCode = "central_unavailable"
	ReasonAuditUnavailable      DenialCode = DeniedAuditUnavailable
	ReasonVerificationFailed    DenialCode = "verification_failed"
	ReasonNoSafeAction          DenialCode = "no_safe_action"
)

// boundedNonExecutionReasons is the complete wire/storage vocabulary. Keep
// policy decisions, central finding validation, alerts, and workflow rendering
// on this one closed set so model or upstream text can never invent controls.
var boundedNonExecutionReasons = [...]DenialCode{
	ReasonCapabilityDestructive,
	ReasonProductDisabled,
	ReasonDeploymentDisabled,
	ReasonStaleLeadership,
	ReasonDisclosureDrift,
	ReasonProductRBACDenied,
	ReasonScopeDenied,
	ReasonReadOnly,
	ReasonNonAutoEligible,
	ReasonApprovalRequired,
	ReasonApprovalExpired,
	ReasonApprovalRejected,
	ReasonAllowlistDenied,
	ReasonSafetyBudgetExceeded,
	ReasonCooldownActive,
	ReasonMaintenanceWindow,
	ReasonPreconditionFailed,
	ReasonCircuitBreakerOpen,
	ReasonIdempotencyConflict,
	ReasonAmbiguousPriorAttempt,
	ReasonExecutionFailed,
	ReasonCentralUnavailable,
	ReasonAuditUnavailable,
	ReasonVerificationFailed,
	ReasonNoSafeAction,
}

func IsBoundedNonExecutionReason(code DenialCode) bool {
	for _, candidate := range boundedNonExecutionReasons {
		if code == candidate {
			return true
		}
	}
	return false
}

func IsActionableNonExecutionReason(code DenialCode) bool {
	return IsBoundedNonExecutionReason(code) && code != ReasonProductDisabled && code != ReasonDeploymentDisabled
}

func NormalizeNonExecutionReason(code DenialCode) (DenialCode, bool) {
	if IsBoundedNonExecutionReason(code) {
		return code, true
	}
	switch code {
	case DeniedDestructive:
		return ReasonCapabilityDestructive, true
	case DeniedDisclosureChanged:
		return ReasonDisclosureDrift, true
	case DeniedAuthorization:
		return ReasonProductRBACDenied, true
	case DeniedReadOnlyWrite:
		return ReasonReadOnly, true
	case DeniedApprovalInvalid:
		return ReasonNoSafeAction, true
	case DeniedNotAutoEligible:
		return ReasonNonAutoEligible, true
	case DeniedNotAllowlisted:
		return ReasonAllowlistDenied, true
	case DeniedBudget:
		return ReasonSafetyBudgetExceeded, true
	case DeniedCircuitOpen:
		return ReasonCircuitBreakerOpen, true
	case DeniedIdempotency:
		return ReasonIdempotencyConflict, true
	case DeniedVerification:
		return ReasonVerificationFailed, true
	case DeniedStaleFencing:
		return ReasonStaleLeadership, true
	case DeniedScope, DeniedApprovalRequired, DeniedCooldown, DeniedPrecondition, DeniedAmbiguousPriorAttempt:
		return code, true
	default:
		return "", false
	}
}

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeReadOnly Mode = "read_only"
	ModeApproval Mode = "approval"
	ModeAuto     Mode = "auto"
)

type Effect string

const (
	EffectRead  Effect = "read"
	EffectWrite Effect = "write"
)

// AuthorityInput contains only already-parsed, bounded policy facts. Product
// context and model output can never set these fields; the MCP guard derives
// them from the current DB, capability disclosure, live RBAC, and receipt.
type AuthorityInput struct {
	FeatureEnabled    bool
	ConnectionActive  bool
	EmergencyDisabled bool
	Mode              Mode
	Effect            Effect
	Destructive       bool
	DisclosureCurrent bool
	LiveAuthorized    bool
	// ApprovalRequested distinguishes an explicit exact-approval path from an
	// automatic path. In automation mode an invalid supplied approval must fail;
	// it may never fall back to service automation.
	ApprovalRequested bool
	// FindingResource is resolved from the exact product-owned session resource
	// matched by arguments.resource_id. It is display/dedupe scope only and never
	// grants authority; the finding access layer rechecks current product RBAC.
	FindingResourceType   string
	FindingResourceID     string
	ApprovalPresent       bool
	ApprovalExact         bool
	ApprovalExpiresAt     time.Time
	AutoEligible          bool
	Allowlisted           bool
	ScopeAllowed          bool
	BudgetAvailable       bool
	CooldownClear         bool
	CircuitClosed         bool
	PreconditionsMet      bool
	IdempotencyKeyPresent bool
	VerificationDeclared  bool
	FencingEpoch          int64
	CurrentFencingEpoch   int64
	AmbiguousPriorAttempt bool
	MaintenanceClear      bool
}

type AuthorityDecision struct {
	Allowed bool
	Code    DenialCode
}

// DecideAuthority is the final local deny-wins gate. Order is security
// significant: tests pin it so a less important policy message cannot obscure
// an emergency stop, feature disable, destructive disclosure, or RBAC denial.
func DecideAuthority(in AuthorityInput, now time.Time) AuthorityDecision {
	deny := func(code DenialCode) AuthorityDecision { return AuthorityDecision{Code: code} }
	if !in.FeatureEnabled {
		return deny(DeniedFeatureDisabled)
	}
	if !in.ConnectionActive {
		return deny(DeniedConnectionInactive)
	}
	if in.EmergencyDisabled {
		return deny(DeniedEmergencyDisabled)
	}
	if in.Mode == ModeDisabled {
		return deny(DeniedModeDisabled)
	}
	if in.Destructive {
		return deny(DeniedDestructive)
	}
	if !in.DisclosureCurrent {
		return deny(DeniedDisclosureChanged)
	}
	if !in.LiveAuthorized {
		return deny(DeniedAuthorization)
	}
	if in.Effect == EffectRead {
		return AuthorityDecision{Allowed: true}
	}
	if in.Mode == ModeReadOnly {
		return deny(DeniedReadOnlyWrite)
	}
	if !in.IdempotencyKeyPresent {
		return deny(DeniedIdempotency)
	}
	if !in.VerificationDeclared {
		return deny(DeniedVerification)
	}
	if in.FencingEpoch <= 0 || in.FencingEpoch != in.CurrentFencingEpoch {
		return deny(DeniedStaleFencing)
	}
	if in.AmbiguousPriorAttempt {
		return deny(DeniedAmbiguousPriorAttempt)
	}
	if !in.MaintenanceClear {
		return deny(DeniedMaintenance)
	}
	if !in.PreconditionsMet {
		return deny(DeniedPrecondition)
	}

	switch in.Mode {
	case ModeApproval:
		if !in.ApprovalPresent {
			return deny(DeniedApprovalRequired)
		}
		if !in.ApprovalExact || in.ApprovalExpiresAt.IsZero() || !in.ApprovalExpiresAt.After(now) {
			return deny(DeniedApprovalInvalid)
		}
	case ModeAuto:
		// Automation is a cumulative ceiling. An explicitly requested approval
		// keeps the exact human-approval path; only requests without an approval
		// may enter the separately authorized automation path.
		if in.ApprovalRequested {
			if !in.ApprovalPresent || !in.ApprovalExact || in.ApprovalExpiresAt.IsZero() || !in.ApprovalExpiresAt.After(now) {
				return deny(DeniedApprovalInvalid)
			}
			break
		}
		if !in.AutoEligible {
			return deny(DeniedNotAutoEligible)
		}
		if !in.Allowlisted {
			return deny(DeniedNotAllowlisted)
		}
		if !in.ScopeAllowed {
			return deny(DeniedScope)
		}
		if !in.BudgetAvailable {
			return deny(DeniedBudget)
		}
		if !in.CooldownClear {
			return deny(DeniedCooldown)
		}
		if !in.CircuitClosed {
			return deny(DeniedCircuitOpen)
		}
	default:
		return deny(DeniedModeDisabled)
	}
	return AuthorityDecision{Allowed: true}
}

// FindingRecommendation is bounded local notification metadata. Full evidence
// and exact arguments stay in Charlie and are fetched through the bridge only
// after live resource authorization.
type FindingRecommendation struct {
	Title              string
	Summary            string
	RecommendedAction  string
	Verification       string
	ExecutionBlockCode DenialCode
	Actionable         bool
}

// BlockedFinding turns a safe diagnosis into an operator-visible next step in
// read-only/approval/auto modes. Disabled/inactive integrations remain inert
// and therefore do not create findings or alerts.
func BlockedFinding(decision AuthorityDecision, title, summary, action, verification string) FindingRecommendation {
	reason, normalized := NormalizeNonExecutionReason(decision.Code)
	if decision.Allowed || !normalized || !IsActionableNonExecutionReason(reason) {
		return FindingRecommendation{}
	}
	return FindingRecommendation{
		Title:              title,
		Summary:            summary,
		RecommendedAction:  action,
		Verification:       verification,
		ExecutionBlockCode: reason,
		Actionable:         true,
	}
}
