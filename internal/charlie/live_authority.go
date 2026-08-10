package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type liveAuthorityQueries interface {
	GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error)
	GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error)
	ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error)
	GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error)
	GetActiveCharlieActionApproval(context.Context, sqlc.GetActiveCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error)
	ConsumeCharlieActionApproval(context.Context, sqlc.ConsumeCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error)
}

// triggerAuthorityQueries binds event-created service sessions back to the
// product-owned trigger policy that created them. Keeping this separate from
// liveAuthorityQueries preserves the small core interface while production
// SQL queries must provide the additional ceiling for event sessions.
type triggerAuthorityQueries interface {
	GetCharlieTriggerEvent(context.Context, uuid.UUID) (sqlc.CharlieTriggerEvent, error)
	GetCharlieTriggerRule(context.Context, uuid.UUID) (sqlc.CharlieTriggerRule, error)
}

type LiveBindingResolver interface {
	CurrentBindings(context.Context, uuid.UUID) ([]rbac.RoleBinding, bool, error)
}

type SafetyFacts struct {
	Allowlisted           bool
	ScopeAllowed          bool
	BudgetAvailable       bool
	CooldownClear         bool
	CircuitClosed         bool
	PreconditionsMet      bool
	AmbiguousPriorAttempt bool
	MaintenanceClear      bool
}

type LiveActionSafety interface {
	Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (SafetyFacts, error)
	ConsumeAutoBudget(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) error
}

type liveWriteSafetyCommitter interface {
	CommitWrite(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage, Mode) error
}

// DenyAutoSafety is the production-safe baseline before an administrator has
// configured a durable allowlist/budget implementation. It permits authority
// evaluation to proceed for reads and exact approvals while auto always fails
// closed and cannot consume a budget.
type DenyAutoSafety struct{}

func (DenyAutoSafety) Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (SafetyFacts, error) {
	return SafetyFacts{
		Allowlisted: false, ScopeAllowed: true, BudgetAvailable: false,
		CooldownClear: true, CircuitClosed: true, PreconditionsMet: true, MaintenanceClear: true,
	}, nil
}

func (DenyAutoSafety) ConsumeAutoBudget(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) error {
	return fmt.Errorf("Charlie auto policy is not configured")
}

type ProductLiveAuthority struct {
	queries      liveAuthorityQueries
	bindings     LiveBindingResolver
	safety       LiveActionSafety
	engine       *rbac.Engine
	automationID uuid.UUID
	features     featureReader
	now          func() time.Time
}

func NewProductLiveAuthority(queries liveAuthorityQueries, bindings LiveBindingResolver, safety LiveActionSafety, automationID uuid.UUID) (*ProductLiveAuthority, error) {
	return newProductLiveAuthority(queries, bindings, safety, automationID, gateFeatureReader(true))
}

// NewProductLiveAuthorityWithFeatures is the production constructor. Unlike
// the compatibility constructor used by focused policy tests, it requires a
// live feature reader so a toggle or read failure wins at every MCP call.
func NewProductLiveAuthorityWithFeatures(queries liveAuthorityQueries, bindings LiveBindingResolver, safety LiveActionSafety, automationID uuid.UUID, features featureReader) (*ProductLiveAuthority, error) {
	if features == nil {
		return nil, fmt.Errorf("Charlie live authority requires a feature reader")
	}
	return newProductLiveAuthority(queries, bindings, safety, automationID, features)
}

type gateFeatureReader bool

func (f gateFeatureReader) BoolValue(context.Context, string, bool) bool { return bool(f) }

func newProductLiveAuthority(queries liveAuthorityQueries, bindings LiveBindingResolver, safety LiveActionSafety, automationID uuid.UUID, features featureReader) (*ProductLiveAuthority, error) {
	if queries == nil || bindings == nil || safety == nil || automationID == uuid.Nil {
		return nil, fmt.Errorf("Charlie live authority requires product state, RBAC, safety policy, and automation identity")
	}
	return &ProductLiveAuthority{queries: queries, bindings: bindings, safety: safety, engine: rbac.NewEngine(), automationID: automationID, features: features, now: time.Now}, nil
}

func (a *ProductLiveAuthority) Evaluate(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (AuthorityInput, error) {
	connection, session, delegation, bindings, err := a.loadIdentity(ctx, action)
	if err != nil {
		return AuthorityInput{}, err
	}
	targetResource, err := a.requireWriteResource(ctx, session.ID, capability, arguments)
	if err != nil {
		return AuthorityInput{}, err
	}
	clusterID, err := targetCluster(arguments)
	if err != nil {
		return AuthorityInput{}, err
	}
	targetAllowed := a.engine.CheckPermission(bindings, rbac.Resource(capability.RBACResource), rbac.Verb(capability.RBACVerb), clusterID, uuid.Nil)
	charlieAllowed := a.engine.CheckPermission(bindings, rbac.ResourceCharlie, rbac.VerbCreate, clusterID, uuid.Nil) ||
		a.engine.CheckPermission(bindings, rbac.ResourceCharlie, rbac.VerbRead, clusterID, uuid.Nil)

	mode := EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled)
	mode, err = a.applyTriggerModeCeiling(ctx, connection, session, delegation, mode)
	if err != nil {
		return AuthorityInput{}, err
	}
	safety, err := a.safety.Evaluate(ctx, action, capability, arguments)
	if err != nil {
		return AuthorityInput{}, err
	}
	input := AuthorityInput{
		FeatureEnabled: a.features != nil && a.features.BoolValue(ctx, "feature.charlie", false), ConnectionActive: connection.Active,
		EmergencyDisabled: connection.EmergencyDisabled, Mode: mode,
		Effect: capability.Effect, Destructive: capability.Destructive,
		DisclosureCurrent: normalizeDigest(connection.DisclosureDigest) != "" && normalizeDigest(connection.DisclosureDigest) == normalizeDigest(action.DisclosureDigest) &&
			connection.VerifiedModeRevision == action.ModeRevision && action.PolicyRevision == action.ModeRevision,
		LiveAuthorized:      targetAllowed && charlieAllowed,
		ApprovalRequested:   action.ApprovalID != "",
		FindingResourceType: targetResource.ResourceType,
		FindingResourceID:   targetResource.ResourceID,
		AutoEligible:        capability.AutoEligible,
		Allowlisted:         safety.Allowlisted, ScopeAllowed: safety.ScopeAllowed && !capability.ManagedTargetAccess,
		BudgetAvailable: safety.BudgetAvailable, CooldownClear: safety.CooldownClear,
		CircuitClosed: safety.CircuitClosed, PreconditionsMet: safety.PreconditionsMet,
		MaintenanceClear:      safety.MaintenanceClear,
		IdempotencyKeyPresent: action.IdempotencyKey == action.ActionID,
		VerificationDeclared:  capability.Effect == EffectRead || capability.RequiresVerification,
		FencingEpoch:          action.FencingEpoch, CurrentFencingEpoch: connection.FencingEpoch,
		AmbiguousPriorAttempt: safety.AmbiguousPriorAttempt,
	}

	// Auto mode's unattended path is service-principal only for writes. Human
	// interactive sessions still evaluate live RBAC (so a real permission miss
	// stays product_rbac_denied) but must use the exact-approval path for
	// writes rather than unattended automation. Clearing LiveAuthorized here
	// previously mis-labeled every human auto write as product_rbac_denied.
	if mode == ModeAuto && !input.ApprovalRequested && capability.Effect == EffectWrite {
		isAutomationService := delegation.PrincipalType == "service" && delegation.PrincipalID == a.automationID
		if !isAutomationService {
			input.InteractiveApprovalRequired = true
		}
	}
	if (mode == ModeApproval || mode == ModeAuto) && input.ApprovalRequested {
		approval, approvalErr := a.queries.GetActiveCharlieActionApproval(ctx, sqlc.GetActiveCharlieActionApprovalParams{CharlieActionID: action.ActionID, ApprovalID: action.ApprovalID})
		if approvalErr == nil && exactApproval(approval, connection, session, action, capability, arguments) {
			approverBindings, active, bindingErr := a.bindings.CurrentBindings(ctx, approval.ApproverID)
			if bindingErr == nil && active {
				approves := a.engine.CheckPermission(approverBindings, rbac.ResourceCharlie, rbac.VerbApprove, clusterID, uuid.Nil)
				target := a.engine.CheckPermission(approverBindings, rbac.Resource(capability.RBACResource), rbac.Verb(capability.RBACVerb), clusterID, uuid.Nil)
				input.ApprovalPresent = true
				input.ApprovalExact = approves && target
				input.ApprovalExpiresAt = approval.ExpiresAt
			}
		}
	}
	return input, nil
}

func (a *ProductLiveAuthority) applyTriggerModeCeiling(ctx context.Context, connection sqlc.CharlieConnection, session sqlc.CharlieSession, delegation sqlc.CharlieDelegation, mode Mode) (Mode, error) {
	if session.Source != "event" || delegation.PrincipalType != "service" {
		return mode, nil
	}
	queries, ok := a.queries.(triggerAuthorityQueries)
	if !ok || session.ClientSessionID == uuid.Nil {
		return ModeDisabled, fmt.Errorf("Charlie trigger authority ceiling is unavailable")
	}
	event, err := queries.GetCharlieTriggerEvent(ctx, session.ClientSessionID)
	if err != nil || !event.SessionID.Valid || event.SessionID.Bytes != session.ID || event.State != "dispatched" {
		return ModeDisabled, fmt.Errorf("Charlie trigger authority binding is inactive")
	}
	rule, err := queries.GetCharlieTriggerRule(ctx, event.RuleID)
	if err != nil || !rule.Enabled || rule.ConnectionID != connection.ID || rule.ServiceIdentityID != delegation.PrincipalID || !validMode(Mode(rule.ModeCeiling)) {
		return ModeDisabled, fmt.Errorf("Charlie trigger authority policy is inactive")
	}
	return EffectiveMode(mode, Mode(rule.ModeCeiling), false), nil
}

func (a *ProductLiveAuthority) Commit(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage, facts AuthorityInput) error {
	// Evaluation facts are deliberately not reusable authority. Re-resolve the
	// live identity and exact ProductContext resource immediately before any
	// approval/budget consumption or adapter side effect.
	_, session, _, _, err := a.loadIdentity(ctx, action)
	if err != nil {
		return err
	}
	if _, err := a.requireWriteResource(ctx, session.ID, capability, arguments); err != nil {
		return err
	}
	approvalPath := facts.Mode == ModeApproval || facts.Mode == ModeAuto && facts.ApprovalRequested
	commitMode := facts.Mode
	if approvalPath {
		commitMode = ModeApproval
	}
	if committer, ok := a.safety.(liveWriteSafetyCommitter); ok {
		if err := committer.CommitWrite(ctx, action, capability, arguments, commitMode); err != nil {
			return err
		}
	}
	if approvalPath {
		if action.ApprovalID == "" {
			return fmt.Errorf("exact Charlie approval is required")
		}
		resourceID, err := requiredWriteResourceID(arguments)
		if err != nil {
			return err
		}
		_, err = a.queries.ConsumeCharlieActionApproval(ctx, sqlc.ConsumeCharlieActionApprovalParams{
			CharlieActionID: action.ActionID, ApprovalID: action.ApprovalID,
			ArgumentDigest: action.ArgumentDigest, DisclosureDigest: action.DisclosureDigest,
			ModeRevision: action.ModeRevision, PolicyRevision: action.PolicyRevision,
			FencingEpoch: action.FencingEpoch, ResourceID: resourceID,
		})
		return err
	}
	switch facts.Mode {
	case ModeAuto:
		if _, ok := a.safety.(liveWriteSafetyCommitter); ok {
			return nil
		}
		return a.safety.ConsumeAutoBudget(ctx, action, capability, arguments)
	default:
		return fmt.Errorf("Charlie write mode cannot commit authority")
	}
}

func (a *ProductLiveAuthority) requireWriteResource(ctx context.Context, sessionID uuid.UUID, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (sqlc.CharlieSessionResource, error) {
	if capability.Effect != EffectWrite {
		return sqlc.CharlieSessionResource{}, nil
	}
	resourceID, err := requiredWriteResourceID(arguments)
	if err != nil {
		return sqlc.CharlieSessionResource{}, err
	}
	resources, err := a.queries.ListCharlieSessionResources(ctx, sessionID)
	if err != nil {
		return sqlc.CharlieSessionResource{}, fmt.Errorf("Charlie session resource scope is unavailable")
	}
	for _, resource := range resources {
		if resource.SessionID == sessionID && resource.ResourceID == resourceID && resource.RequiredVerb == "read" {
			return resource, nil
		}
	}
	return sqlc.CharlieSessionResource{}, fmt.Errorf("Charlie write resource is outside the session scope")
}

func requiredWriteResourceID(arguments map[string]json.RawMessage) (string, error) {
	raw, ok := arguments["resource_id"]
	if !ok {
		return "", fmt.Errorf("Charlie write resource is required")
	}
	var resourceID string
	if json.Unmarshal(raw, &resourceID) != nil || !regexp.MustCompile(opaqueIDPattern).MatchString(resourceID) {
		return "", fmt.Errorf("Charlie write resource is invalid")
	}
	return resourceID, nil
}

func (a *ProductLiveAuthority) loadIdentity(ctx context.Context, action ActionEnvelope) (sqlc.CharlieConnection, sqlc.CharlieSession, sqlc.CharlieDelegation, []rbac.RoleBinding, error) {
	connection, err := a.queries.GetCharlieConnectionByDeploymentID(ctx, action.DeploymentID)
	if err != nil || connection.ProductSlug != "astronomer" {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, sqlc.CharlieDelegation{}, nil, fmt.Errorf("Charlie deployment binding is inactive")
	}
	session, err := a.queries.GetCharlieSessionByCentralID(ctx, action.SessionID)
	if err != nil || session.ConnectionID != connection.ID || (session.State != "active" && session.State != "waiting_approval") {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, sqlc.CharlieDelegation{}, nil, fmt.Errorf("Charlie session binding is inactive")
	}
	delegation, err := a.queries.GetActiveCharlieDelegationByHash(ctx, HashDelegation(action.AuthorizationRef))
	if err != nil || delegation.SessionID != session.ID {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, sqlc.CharlieDelegation{}, nil, fmt.Errorf("Charlie delegation is inactive")
	}
	bindings, active, err := a.bindings.CurrentBindings(ctx, delegation.PrincipalID)
	if err != nil || !active {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, sqlc.CharlieDelegation{}, nil, fmt.Errorf("Charlie principal is inactive")
	}
	return connection, session, delegation, bindings, nil
}

func targetCluster(arguments map[string]json.RawMessage) (uuid.UUID, error) {
	raw, ok := arguments["cluster_id"]
	if !ok {
		return uuid.Nil, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return uuid.Nil, fmt.Errorf("Charlie cluster scope is invalid")
	}
	clusterID, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("Charlie cluster scope is invalid")
	}
	return clusterID, nil
}

func exactApproval(approval sqlc.CharlieActionApproval, connection sqlc.CharlieConnection, session sqlc.CharlieSession, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage) bool {
	resourceID, err := requiredWriteResourceID(arguments)
	if err != nil || approval.ResourceID != resourceID {
		return false
	}
	return approval.ConnectionID == connection.ID && approval.SessionID == session.ID &&
		approval.CharlieActionID == action.ActionID && approval.TurnID == action.TurnID &&
		approval.Capability == capability.Name && approval.ArgumentDigest == action.ArgumentDigest &&
		approval.DisclosureDigest == action.DisclosureDigest && approval.ModeRevision == action.ModeRevision &&
		approval.PolicyRevision == action.PolicyRevision && approval.FencingEpoch == action.FencingEpoch && approval.State == "approved"
}
