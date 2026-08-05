package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

const (
	maxActionArguments = 64 << 10
	maxActionResult    = 64 << 10
)

// ActionEnvelope is Charlie's signed, bounded request to the product-owned MCP
// boundary. All authority-bearing fields are protected by the signature; the
// opaque authorization reference is resolved against live product state.
type ActionEnvelope struct {
	Version          string          `json:"version"`
	DeploymentID     string          `json:"deployment_id"`
	SessionID        string          `json:"session_id"`
	TurnID           string          `json:"turn_id"`
	ActionID         string          `json:"action_id"`
	Capability       string          `json:"capability"`
	Arguments        json.RawMessage `json:"arguments"`
	ArgumentDigest   string          `json:"argument_digest"`
	AuthorizationRef string          `json:"authorization_ref"`
	ApprovalID       string          `json:"approval_id,omitempty"`
	DisclosureDigest string          `json:"disclosure_digest"`
	ModeRevision     int64           `json:"mode_revision"`
	PolicyRevision   int64           `json:"policy_revision"`
	FencingEpoch     int64           `json:"fencing_epoch"`
	ExpiresAt        time.Time       `json:"expires_at"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Signature        string          `json:"signature"`
}

type signedActionEnvelope struct {
	Version          string          `json:"version"`
	DeploymentID     string          `json:"deployment_id"`
	SessionID        string          `json:"session_id"`
	TurnID           string          `json:"turn_id"`
	ActionID         string          `json:"action_id"`
	Capability       string          `json:"capability"`
	Arguments        json.RawMessage `json:"arguments"`
	ArgumentDigest   string          `json:"argument_digest"`
	AuthorizationRef string          `json:"authorization_ref"`
	ApprovalID       string          `json:"approval_id,omitempty"`
	DisclosureDigest string          `json:"disclosure_digest"`
	ModeRevision     int64           `json:"mode_revision"`
	PolicyRevision   int64           `json:"policy_revision"`
	FencingEpoch     int64           `json:"fencing_epoch"`
	ExpiresAt        time.Time       `json:"expires_at"`
	IdempotencyKey   string          `json:"idempotency_key"`
}

func (a ActionEnvelope) signed() signedActionEnvelope {
	return signedActionEnvelope{
		Version: a.Version, DeploymentID: a.DeploymentID, SessionID: a.SessionID,
		TurnID: a.TurnID, ActionID: a.ActionID, Capability: a.Capability,
		Arguments: a.Arguments, ArgumentDigest: a.ArgumentDigest,
		AuthorizationRef: a.AuthorizationRef, ApprovalID: a.ApprovalID, DisclosureDigest: a.DisclosureDigest,
		ModeRevision: a.ModeRevision, PolicyRevision: a.PolicyRevision,
		FencingEpoch: a.FencingEpoch, ExpiresAt: a.ExpiresAt.UTC(), IdempotencyKey: a.IdempotencyKey,
	}
}

type LiveActionAuthority interface {
	// Evaluate must resolve AuthorizationRef, load the current principal/service
	// identity and RBAC bindings, validate exact management-plane scope, and
	// return current mode/disclosure/policy/fencing/safety facts. It may not use
	// a session-time RBAC snapshot.
	Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (AuthorityInput, error)
	// Commit atomically consumes the exact approval or auto budget immediately
	// before dispatch. A successful evaluation is never a reusable authority
	// token; concurrent calls must allow at most one commit.
	Commit(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage, AuthorityInput) error
}

type ReceiptDisposition string

const (
	ReceiptClaimed   ReceiptDisposition = "claimed"
	ReceiptReplay    ReceiptDisposition = "replay"
	ReceiptAmbiguous ReceiptDisposition = "ambiguous"
	ReceiptConflict  ReceiptDisposition = "conflict"
)

type ReceiptClaim struct {
	Disposition ReceiptDisposition
	Result      ActionResult
}

type ActionReceiptStore interface {
	Claim(context.Context, ActionEnvelope, CapabilityDescriptor) (ReceiptClaim, error)
	Transition(context.Context, ActionEnvelope, string, ActionResult) error
}

type CapabilityExecutor interface {
	Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error)
	Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error)
}

type CapabilityAvailability interface {
	SupportsCapability(string) bool
}

type ActionResult struct {
	Allowed  bool                   `json:"allowed"`
	Code     DenialCode             `json:"code,omitempty"`
	State    string                 `json:"state"`
	Replay   bool                   `json:"replay,omitempty"`
	Result   json.RawMessage        `json:"result,omitempty"`
	Verified bool                   `json:"verified,omitempty"`
	Finding  *FindingRecommendation `json:"finding,omitempty"`
}

type ActionGuard struct {
	publicKey  ed25519.PublicKey
	authority  LiveActionAuthority
	receipts   ActionReceiptStore
	executor   CapabilityExecutor
	auditor    ActionAuditor
	now        func() time.Time
	logger     *slog.Logger
	writeFence *WriteFence
	findings   BlockedFindingRecorder
	installID  string
}

// SetFindingRecorder attaches the durable finding/alert path for policy
// denials. The production MCP runtime always supplies this dependency. It is a
// setter so the lower-level guard remains usable in focused cryptographic and
// catalog tests without a database.
func (g *ActionGuard) SetFindingRecorder(recorder BlockedFindingRecorder, installationID string) {
	if g == nil || recorder == nil || strings.TrimSpace(installationID) == "" {
		return
	}
	g.findings = recorder
	g.installID = strings.TrimSpace(installationID)
}

func NewActionGuard(publicKey ed25519.PublicKey, authority LiveActionAuthority, receipts ActionReceiptStore, executor CapabilityExecutor, auditor ActionAuditor) (*ActionGuard, error) {
	if len(publicKey) != ed25519.PublicKeySize || authority == nil || receipts == nil || executor == nil || auditor == nil {
		return nil, fmt.Errorf("Charlie action guard requires signing trust, live authority, receipts, executor, and durable audit")
	}
	return &ActionGuard{publicKey: append(ed25519.PublicKey(nil), publicKey...), authority: authority, receipts: receipts, executor: executor, auditor: auditor, now: time.Now, writeFence: NewWriteFence()}, nil
}

// SetWriteFence attaches the server-wide write admission registry. Every MCP
// guard created by the runtime must share the same instance as feature and
// emergency-disable reconciliation.
func (g *ActionGuard) SetWriteFence(fence *WriteFence) {
	if g != nil && fence != nil {
		g.writeFence = fence
	}
}

// SetLogger attaches the product logger before the guard is exposed to the MCP
// listener. The logger receives only the bounded DecisionLog vocabulary.
func (g *ActionGuard) SetLogger(logger *slog.Logger) {
	if g != nil {
		g.logger = logger
	}
}

func (g *ActionGuard) Execute(ctx context.Context, envelope ActionEnvelope) (result ActionResult) {
	observedMode := ModeDisabled
	defer func() {
		observeAction(envelope, result)
		descriptor, _ := capabilityByName(envelope.Capability)
		LogAuthorityDecision(g.logger, DecisionLog{
			SessionID: envelope.SessionID, ActionID: envelope.ActionID,
			Capability: envelope.Capability, Mode: observedMode, Effect: descriptor.Effect,
			Decision: AuthorityDecision{Allowed: result.Allowed, Code: result.Code},
		})
	}()
	descriptor, arguments, code := g.validate(envelope)
	if err := g.auditor.Record(ctx, "proposed", envelope, descriptor, ActionResult{State: "proposed"}); err != nil {
		return denied(DeniedAuthorization, "The required action audit record could not be persisted")
	}
	if code != "" {
		result := denied(code, "Charlie action was rejected before dispatch")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	}
	operationCtx := ctx
	if descriptor.Effect == EffectWrite {
		var releaseWrite func()
		var err error
		operationCtx, releaseWrite, err = g.writeFence.Begin(ctx)
		if err != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution is disabled")
			g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
			return result
		}
		defer releaseWrite()
	}

	facts, err := g.authority.Evaluate(operationCtx, envelope, descriptor, arguments)
	if err != nil {
		if descriptor.Effect == EffectWrite && operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled during authorization")
			g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
			return result
		}
		result := denied(DeniedAuthorization, "Current product authorization could not be verified")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	}
	observedMode = facts.Mode
	decision := DecideAuthority(facts, g.now().UTC())
	if !decision.Allowed {
		result := actionableDenied(decision.Code, "Charlie identified an action that current product policy does not permit")
		return g.recordPolicyDenial(ctx, envelope, descriptor, facts, result, "denied")
	}

	if descriptor.Effect == EffectRead {
		return g.executeAndVerify(ctx, envelope, descriptor, arguments, false)
	}

	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before receipt claim")
		g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
		return result
	}
	claim, err := g.receipts.Claim(operationCtx, envelope, descriptor)
	if err != nil {
		result := denied(DeniedAmbiguousPriorAttempt, "The action receipt could not be claimed safely")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	}
	switch claim.Disposition {
	case ReceiptReplay:
		claim.Result.Replay = true
		g.recordAuditOutcome(ctx, "replayed", envelope, descriptor, claim.Result)
		return claim.Result
	case ReceiptAmbiguous:
		result := denied(DeniedAmbiguousPriorAttempt, "A prior attempt has an ambiguous outcome and requires reconciliation")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	case ReceiptConflict:
		result := denied(DeniedIdempotency, "The action identifier conflicts with different arguments or authority")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	case ReceiptClaimed:
	default:
		result := denied(DeniedAmbiguousPriorAttempt, "The action receipt returned an invalid state")
		g.recordAuditOutcome(ctx, "denied", envelope, descriptor, result)
		return result
	}

	// Disable, revocation, mode/disclosure drift, precondition change, and a
	// leadership change while waiting all win immediately before side effect.
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before dispatch")
		g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
		g.recordReceiptOutcome(ctx, envelope, "fenced", result)
		return result
	}
	facts, err = g.authority.Evaluate(operationCtx, envelope, descriptor, arguments)
	if err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before dispatch")
			g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
			g.recordReceiptOutcome(ctx, envelope, "fenced", result)
			return result
		}
		result := denied(DeniedAuthorization, "Current product authorization changed before dispatch")
		g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
		g.recordReceiptOutcome(ctx, envelope, "blocked", result)
		return result
	}
	decision = DecideAuthority(facts, g.now().UTC())
	if !decision.Allowed {
		result := actionableDenied(decision.Code, "Product policy changed before dispatch")
		g.recordReceiptOutcome(ctx, envelope, "blocked", result)
		return g.recordPolicyDenial(ctx, envelope, descriptor, facts, result, "fenced")
	}
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before authorization commit")
		g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
		g.recordReceiptOutcome(ctx, envelope, "fenced", result)
		return result
	}
	if err := g.authority.Commit(operationCtx, envelope, descriptor, arguments, facts); err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled during authorization commit")
			g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
			g.recordReceiptOutcome(ctx, envelope, "fenced", result)
			return result
		}
		var deferred *ActionDeferredError
		if errors.As(err, &deferred) {
			result := ActionResult{Allowed: true, State: "deferred", Result: deferred.Result()}
			if auditErr := g.auditor.Record(ctx, "deferred", envelope, descriptor, result); auditErr != nil {
				result = denied(DeniedAuthorization, "The required deferral audit record could not be persisted")
				g.recordReceiptOutcome(ctx, envelope, "blocked", result)
				return result
			}
			if transitionErr := g.receipts.Transition(ctx, envelope, "deferred", result); transitionErr != nil {
				return denied(DeniedAmbiguousPriorAttempt, "The deferred action state could not be recorded safely")
			}
			return result
		}
		result := actionableDenied(DeniedApprovalInvalid, "The exact approval or automation budget was already consumed or changed")
		g.recordReceiptOutcome(ctx, envelope, "blocked", result)
		return g.recordPolicyDenial(ctx, envelope, descriptor, facts, result, "denied")
	}
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before side effect")
		g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
		g.recordReceiptOutcome(ctx, envelope, "fenced", result)
		return result
	}
	if err := g.auditor.Record(operationCtx, "approved", envelope, descriptor, ActionResult{Allowed: true, State: "approved"}); err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before side effect")
			g.recordAuditOutcome(ctx, "fenced", envelope, descriptor, result)
			g.recordReceiptOutcome(ctx, envelope, "fenced", result)
			return result
		}
		result := denied(DeniedAuthorization, "The required approval audit record could not be persisted")
		g.recordReceiptOutcome(ctx, envelope, "blocked", result)
		return result
	}
	return g.executeAndVerify(operationCtx, envelope, descriptor, arguments, true)
}

// recordPolicyDenial writes the required action audit and then creates a
// durable, deduplicated, resource-scoped finding. Both error paths emit only a
// bounded failure code; arguments, model output, authorization references and
// remote payloads are never logged. The action remains denied even if either
// observability dependency is unavailable.
func (g *ActionGuard) recordPolicyDenial(ctx context.Context, envelope ActionEnvelope, descriptor CapabilityDescriptor, facts AuthorityInput, result ActionResult, phase string) ActionResult {
	if err := g.auditor.Record(ctx, phase, envelope, descriptor, result); err != nil {
		g.logPersistenceFailure(ctx, "Charlie action audit persistence failed", "charlie.action_audit_persist_failed")
	}
	if result.Finding == nil || !result.Finding.Actionable {
		return result
	}
	if g.findings == nil || g.installID == "" || descriptor.Name == "" || facts.FindingResourceType == "" || facts.FindingResourceID == "" {
		// Never claim an actionable operator workflow unless the production
		// recorder and exact product-owned resource scope are present.
		result.Finding = nil
		return result
	}
	input := blockedActionFindingInput(g.installID, facts.FindingResourceType, facts.FindingResourceID, descriptor.Name, facts.Mode, result.Code)
	if _, err := g.findings.RecordBlocked(ctx, input); err != nil {
		g.logPersistenceFailure(ctx, "Charlie blocked-action finding persistence failed", "charlie.finding_persist_failed")
		result.Finding = nil
	}
	return result
}

func blockedActionFindingInput(installationID, resourceType, resourceID, capability string, mode Mode, code DenialCode) FindingInput {
	title := "Charlie recommends an operator decision"
	summary := "Charlie identified a management-plane action that current product policy did not permit."
	next := "Review the finding and current Charlie permissions before deciding whether to proceed."
	severity := "warning"
	switch code {
	case DeniedReadOnlyWrite:
		next = "Keep read-only mode, or change to approval-required mode and review the exact proposed action."
	case DeniedApprovalRequired:
		next = "Review the exact proposed action and approve or reject it from the Charlie approval queue."
	case DeniedApprovalInvalid:
		next = "Refresh the approval queue; the prior approval is expired, changed, or already consumed."
	case DeniedNotAutoEligible:
		next = "Use approval-required mode for this action; it is not eligible for unattended execution."
	case DeniedNotAllowlisted:
		next = "Review the central allowlist and local action policy, or use approval-required mode."
	case DeniedBudget, DeniedCooldown, DeniedCircuitOpen:
		next = "Review the local action budget, cooldown, and circuit state before retrying or approving manually."
	case DeniedScope, DeniedAuthorization:
		next = "Review the initiating identity and exact product-owned resource permissions."
	case DeniedPrecondition, DeniedVerification:
		next = "Re-read the affected management-plane resource and verify the required safety preconditions."
	case DeniedStaleFencing, DeniedAmbiguousPriorAttempt, DeniedIdempotency:
		severity = "high"
		next = "Do not retry blindly; reconcile the prior action receipt and current leader epoch first."
	case DeniedDestructive:
		severity = "high"
		next = "Use an operator-owned workflow outside Charlie; destructive actions are prohibited."
	}
	return FindingInput{
		InstallationID: installationID, ResourceType: resourceType, ResourceID: resourceID,
		NormalizedDiagnosis: string(code), RecommendedCapability: capability, Severity: severity, Mode: mode,
		Decision: AuthorityDecision{Allowed: false, Code: code}, Title: title, Summary: summary,
		RecommendedAction: next, RiskImpact: "No action was dispatched; the condition may continue until an operator decides or policy changes.",
		Verification: "Re-read the affected management-plane resource and confirm the finding state after the decision.",
	}
}

func (g *ActionGuard) executeAndVerify(ctx context.Context, envelope ActionEnvelope, descriptor CapabilityDescriptor, arguments map[string]json.RawMessage, durable bool) ActionResult {
	if durable && ctx.Err() != nil {
		return g.recordFenced(ctx, envelope, descriptor, "Charlie write execution was disabled before dispatch")
	}
	if durable {
		if err := g.auditor.Record(ctx, "dispatched", envelope, descriptor, ActionResult{Allowed: true, State: "dispatched"}); err != nil {
			result := denied(DeniedAuthorization, "The required dispatch audit record could not be persisted")
			g.recordReceiptOutcome(ctx, envelope, "blocked", result)
			return result
		}
		if err := g.receipts.Transition(ctx, envelope, "dispatched", ActionResult{Allowed: true, State: "dispatched"}); err != nil {
			result := denied(DeniedAmbiguousPriorAttempt, "Dispatch state could not be recorded safely")
			g.recordAuditOutcome(ctx, "failed", envelope, descriptor, result)
			return result
		}
		if ctx.Err() != nil {
			return g.recordFenced(ctx, envelope, descriptor, "Charlie write execution was disabled before side effect")
		}
	}
	timeout := time.Duration(descriptor.TimeoutSeconds) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resultBytes, err := g.executor.Execute(callCtx, descriptor, arguments)
	if err != nil {
		if durable && errors.Is(callCtx.Err(), context.Canceled) && g.writeFence.State().Closed {
			return g.recordFenced(ctx, envelope, descriptor, "Charlie write executor was cancelled by disable")
		}
		result := ActionResult{Allowed: true, State: "failed"}
		if g.auditor.Record(ctx, "failed", envelope, descriptor, result) != nil {
			result.State = "ambiguous"
		}
		if durable {
			g.recordReceiptOutcome(ctx, envelope, "ambiguous", result)
		}
		return result
	}
	if len(resultBytes) > min(descriptor.MaxResponseBytes, maxActionResult) || !json.Valid(resultBytes) {
		result := ActionResult{Allowed: true, State: "failed"}
		if g.auditor.Record(ctx, "failed", envelope, descriptor, result) != nil {
			result.State = "ambiguous"
		}
		if durable {
			g.recordReceiptOutcome(ctx, envelope, result.State, result)
		}
		return result
	}
	verified := true
	if descriptor.RequiresVerification {
		verified, err = g.executor.Verify(callCtx, descriptor, arguments, resultBytes)
		if err != nil || !verified {
			if durable && errors.Is(callCtx.Err(), context.Canceled) && g.writeFence.State().Closed {
				return g.recordFenced(ctx, envelope, descriptor, "Charlie write verification was cancelled by disable")
			}
			result := ActionResult{Allowed: true, State: "failed", Verified: false}
			if g.auditor.Record(ctx, "failed", envelope, descriptor, result) != nil {
				result.State = "ambiguous"
			}
			if durable {
				g.recordReceiptOutcome(ctx, envelope, result.State, result)
			}
			return result
		}
	}
	result := ActionResult{Allowed: true, State: "succeeded", Result: resultBytes, Verified: verified}
	if err := g.auditor.Record(ctx, "succeeded", envelope, descriptor, result); err != nil {
		if durable {
			g.recordReceiptOutcome(ctx, envelope, "ambiguous", ActionResult{Allowed: true, State: "ambiguous"})
		}
		return ActionResult{Allowed: true, State: "ambiguous"}
	}
	if durable {
		if err := g.receipts.Transition(ctx, envelope, "succeeded", result); err != nil {
			return ActionResult{Allowed: true, State: "ambiguous"}
		}
	}
	return result
}

func (g *ActionGuard) recordFenced(ctx context.Context, envelope ActionEnvelope, descriptor CapabilityDescriptor, summary string) ActionResult {
	result := denied(DeniedEmergencyDisabled, summary)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	g.recordAuditOutcome(persistCtx, "fenced", envelope, descriptor, result)
	g.recordReceiptOutcome(persistCtx, envelope, "fenced", result)
	return result
}

func (g *ActionGuard) recordAuditOutcome(ctx context.Context, phase string, envelope ActionEnvelope, descriptor CapabilityDescriptor, result ActionResult) {
	if err := g.auditor.Record(ctx, phase, envelope, descriptor, result); err != nil {
		g.logPersistenceFailure(ctx, "Charlie action audit persistence failed", "charlie.action_audit_persist_failed")
	}
}

func (g *ActionGuard) recordReceiptOutcome(ctx context.Context, envelope ActionEnvelope, state string, result ActionResult) {
	if err := g.receipts.Transition(ctx, envelope, state, result); err != nil {
		g.logPersistenceFailure(ctx, "Charlie action receipt persistence failed", "charlie.action_receipt_persist_failed")
	}
}

func (g *ActionGuard) logPersistenceFailure(ctx context.Context, message, code string) {
	logger := g.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, message, slog.String("failure_code", code))
}

func (g *ActionGuard) validate(envelope ActionEnvelope) (CapabilityDescriptor, map[string]json.RawMessage, DenialCode) {
	if envelope.Version != "charlie-action/v1" || envelope.DeploymentID == "" || envelope.SessionID == "" || envelope.TurnID == "" || envelope.ActionID == "" || envelope.Capability == "" || envelope.AuthorizationRef == "" || envelope.DisclosureDigest == "" || envelope.ModeRevision < 1 || envelope.PolicyRevision < 1 || envelope.FencingEpoch < 1 || !envelope.ExpiresAt.After(g.now().UTC()) || envelope.ExpiresAt.After(g.now().UTC().Add(15*time.Minute)) {
		return CapabilityDescriptor{}, nil, DeniedAuthorization
	}
	if envelope.IdempotencyKey != envelope.ActionID {
		return CapabilityDescriptor{}, nil, DeniedIdempotency
	}
	canonical, arguments, err := canonicalArguments(envelope.Arguments)
	if err != nil || envelope.ArgumentDigest != capabilityArgumentDigest(envelope.Capability, canonical) {
		return CapabilityDescriptor{}, nil, DeniedIdempotency
	}
	signedBytes, err := json.Marshal(envelope.signed())
	if err != nil {
		return CapabilityDescriptor{}, nil, DeniedAuthorization
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil {
		signature, err = base64.StdEncoding.DecodeString(envelope.Signature)
	}
	if err != nil || !ed25519.Verify(g.publicKey, signedBytes, signature) {
		return CapabilityDescriptor{}, nil, DeniedAuthorization
	}
	descriptor, ok := capabilityByName(envelope.Capability)
	if !ok || descriptor.ManagedTargetAccess {
		return CapabilityDescriptor{}, nil, DeniedScope
	}
	if availability, ok := g.executor.(CapabilityAvailability); ok && !availability.SupportsCapability(envelope.Capability) {
		return CapabilityDescriptor{}, nil, DeniedScope
	}
	accepted := make(map[string]struct{}, len(descriptor.AcceptedFields))
	for _, field := range descriptor.AcceptedFields {
		accepted[field] = struct{}{}
	}
	for field := range arguments {
		if _, ok := accepted[field]; !ok {
			return CapabilityDescriptor{}, nil, DeniedScope
		}
	}
	if validateCapabilityArguments(descriptor, arguments) != nil {
		return CapabilityDescriptor{}, nil, DeniedScope
	}
	if descriptor.Effect == EffectWrite {
		operation, ok := arguments["operation_id"]
		var operationID string
		if !ok || json.Unmarshal(operation, &operationID) != nil || operationID != envelope.ActionID {
			return CapabilityDescriptor{}, nil, DeniedIdempotency
		}
	}
	return descriptor, arguments, ""
}

// capabilityArgumentDigest binds the canonical arguments to the exact
// capability being authorized. This prevents a signed argument payload from
// being replayed against another tool with a compatible schema.
func capabilityArgumentDigest(capability string, canonical []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(capability))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalArguments(raw json.RawMessage) ([]byte, map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxActionArguments {
		return nil, nil, fmt.Errorf("invalid argument size")
	}
	var arguments map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil || arguments == nil {
		return nil, nil, fmt.Errorf("arguments must be an object")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, nil, fmt.Errorf("arguments must contain one object")
	}
	canonical, err := json.Marshal(arguments)
	return canonical, arguments, err
}

func capabilityByName(name string) (CapabilityDescriptor, bool) {
	for _, descriptor := range append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...) {
		if descriptor.Name == name {
			return descriptor, true
		}
	}
	return CapabilityDescriptor{}, false
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func denied(code DenialCode, summary string) ActionResult {
	return ActionResult{Code: code, State: "blocked"}
}

func actionableDenied(code DenialCode, summary string) ActionResult {
	finding := BlockedFinding(AuthorityDecision{Code: code}, "Charlie action requires attention", summary, "Review the finding and current product authorization", "Re-read the affected management-plane resource after any approved action")
	result := denied(code, summary)
	if finding.Actionable {
		result.Finding = &finding
	}
	return result
}
