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
)

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
	FeatureEnabled        bool
	ConnectionActive      bool
	EmergencyDisabled     bool
	Mode                  Mode
	Effect                Effect
	Destructive           bool
	DisclosureCurrent     bool
	LiveAuthorized        bool
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
	if decision.Allowed || decision.Code == DeniedFeatureDisabled || decision.Code == DeniedConnectionInactive || decision.Code == DeniedEmergencyDisabled || decision.Code == DeniedModeDisabled {
		return FindingRecommendation{}
	}
	return FindingRecommendation{
		Title:              title,
		Summary:            summary,
		RecommendedAction:  action,
		Verification:       verification,
		ExecutionBlockCode: decision.Code,
		Actionable:         true,
	}
}
