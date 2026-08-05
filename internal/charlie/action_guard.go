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
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	}
	operationCtx := ctx
	if descriptor.Effect == EffectWrite {
		var releaseWrite func()
		var err error
		operationCtx, releaseWrite, err = g.writeFence.Begin(ctx)
		if err != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution is disabled")
			_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
			return result
		}
		defer releaseWrite()
	}

	facts, err := g.authority.Evaluate(operationCtx, envelope, descriptor, arguments)
	if err != nil {
		if descriptor.Effect == EffectWrite && operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled during authorization")
			_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
			return result
		}
		result := denied(DeniedAuthorization, "Current product authorization could not be verified")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	}
	observedMode = facts.Mode
	decision := DecideAuthority(facts, g.now().UTC())
	if !decision.Allowed {
		result := denied(decision.Code, "Charlie identified an action that current product policy does not permit")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	}

	if descriptor.Effect == EffectRead {
		return g.executeAndVerify(ctx, envelope, descriptor, arguments, false)
	}

	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before receipt claim")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		return result
	}
	claim, err := g.receipts.Claim(operationCtx, envelope, descriptor)
	if err != nil {
		result := denied(DeniedAmbiguousPriorAttempt, "The action receipt could not be claimed safely")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	}
	switch claim.Disposition {
	case ReceiptReplay:
		claim.Result.Replay = true
		_ = g.auditor.Record(ctx, "replayed", envelope, descriptor, claim.Result)
		return claim.Result
	case ReceiptAmbiguous:
		result := denied(DeniedAmbiguousPriorAttempt, "A prior attempt has an ambiguous outcome and requires reconciliation")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	case ReceiptConflict:
		result := denied(DeniedIdempotency, "The action identifier conflicts with different arguments or authority")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	case ReceiptClaimed:
	default:
		result := denied(DeniedAmbiguousPriorAttempt, "The action receipt returned an invalid state")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		return result
	}

	// Disable, revocation, mode/disclosure drift, precondition change, and a
	// leadership change while waiting all win immediately before side effect.
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before dispatch")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "fenced", result)
		return result
	}
	facts, err = g.authority.Evaluate(operationCtx, envelope, descriptor, arguments)
	if err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before dispatch")
			_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
			_ = g.receipts.Transition(ctx, envelope, "fenced", result)
			return result
		}
		result := denied(DeniedAuthorization, "Current product authorization changed before dispatch")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "blocked", result)
		return result
	}
	decision = DecideAuthority(facts, g.now().UTC())
	if !decision.Allowed {
		result := denied(decision.Code, "Product policy changed before dispatch")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "blocked", result)
		return result
	}
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before authorization commit")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "fenced", result)
		return result
	}
	if err := g.authority.Commit(operationCtx, envelope, descriptor, arguments, facts); err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled during authorization commit")
			_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
			_ = g.receipts.Transition(ctx, envelope, "fenced", result)
			return result
		}
		var deferred *ActionDeferredError
		if errors.As(err, &deferred) {
			result := ActionResult{Allowed: true, State: "deferred", Result: deferred.Result()}
			if auditErr := g.auditor.Record(ctx, "deferred", envelope, descriptor, result); auditErr != nil {
				result = denied(DeniedAuthorization, "The required deferral audit record could not be persisted")
				_ = g.receipts.Transition(ctx, envelope, "blocked", result)
				return result
			}
			if transitionErr := g.receipts.Transition(ctx, envelope, "deferred", result); transitionErr != nil {
				return denied(DeniedAmbiguousPriorAttempt, "The deferred action state could not be recorded safely")
			}
			return result
		}
		result := denied(DeniedApprovalInvalid, "The exact approval or automation budget was already consumed or changed")
		_ = g.auditor.Record(ctx, "denied", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "blocked", result)
		return result
	}
	if operationCtx.Err() != nil {
		result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before side effect")
		_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
		_ = g.receipts.Transition(ctx, envelope, "fenced", result)
		return result
	}
	if err := g.auditor.Record(operationCtx, "approved", envelope, descriptor, ActionResult{Allowed: true, State: "approved"}); err != nil {
		if operationCtx.Err() != nil {
			result := denied(DeniedEmergencyDisabled, "Charlie write execution was disabled before side effect")
			_ = g.auditor.Record(ctx, "fenced", envelope, descriptor, result)
			_ = g.receipts.Transition(ctx, envelope, "fenced", result)
			return result
		}
		result := denied(DeniedAuthorization, "The required approval audit record could not be persisted")
		_ = g.receipts.Transition(ctx, envelope, "blocked", result)
		return result
	}
	return g.executeAndVerify(operationCtx, envelope, descriptor, arguments, true)
}

func (g *ActionGuard) executeAndVerify(ctx context.Context, envelope ActionEnvelope, descriptor CapabilityDescriptor, arguments map[string]json.RawMessage, durable bool) ActionResult {
	if durable && ctx.Err() != nil {
		return g.recordFenced(ctx, envelope, descriptor, "Charlie write execution was disabled before dispatch")
	}
	if durable {
		if err := g.auditor.Record(ctx, "dispatched", envelope, descriptor, ActionResult{Allowed: true, State: "dispatched"}); err != nil {
			result := denied(DeniedAuthorization, "The required dispatch audit record could not be persisted")
			_ = g.receipts.Transition(ctx, envelope, "blocked", result)
			return result
		}
		if err := g.receipts.Transition(ctx, envelope, "dispatched", ActionResult{Allowed: true, State: "dispatched"}); err != nil {
			result := denied(DeniedAmbiguousPriorAttempt, "Dispatch state could not be recorded safely")
			_ = g.auditor.Record(ctx, "failed", envelope, descriptor, result)
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
			_ = g.receipts.Transition(ctx, envelope, "ambiguous", result)
		}
		return result
	}
	if len(resultBytes) > min(descriptor.MaxResponseBytes, maxActionResult) || !json.Valid(resultBytes) {
		result := ActionResult{Allowed: true, State: "failed"}
		if g.auditor.Record(ctx, "failed", envelope, descriptor, result) != nil {
			result.State = "ambiguous"
		}
		if durable {
			_ = g.receipts.Transition(ctx, envelope, result.State, result)
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
				_ = g.receipts.Transition(ctx, envelope, result.State, result)
			}
			return result
		}
	}
	result := ActionResult{Allowed: true, State: "succeeded", Result: resultBytes, Verified: verified}
	if err := g.auditor.Record(ctx, "succeeded", envelope, descriptor, result); err != nil {
		if durable {
			_ = g.receipts.Transition(ctx, envelope, "ambiguous", ActionResult{Allowed: true, State: "ambiguous"})
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
	_ = g.auditor.Record(persistCtx, "fenced", envelope, descriptor, result)
	_ = g.receipts.Transition(persistCtx, envelope, "fenced", result)
	return result
}

func (g *ActionGuard) validate(envelope ActionEnvelope) (CapabilityDescriptor, map[string]json.RawMessage, DenialCode) {
	if envelope.Version != "charlie-action/v1" || envelope.DeploymentID == "" || envelope.SessionID == "" || envelope.TurnID == "" || envelope.ActionID == "" || envelope.Capability == "" || envelope.AuthorizationRef == "" || envelope.DisclosureDigest == "" || envelope.ModeRevision < 1 || envelope.PolicyRevision < 1 || envelope.FencingEpoch < 1 || !envelope.ExpiresAt.After(g.now().UTC()) || envelope.ExpiresAt.After(g.now().UTC().Add(15*time.Minute)) {
		return CapabilityDescriptor{}, nil, DeniedAuthorization
	}
	if envelope.IdempotencyKey != envelope.ActionID {
		return CapabilityDescriptor{}, nil, DeniedIdempotency
	}
	canonical, arguments, err := canonicalArguments(envelope.Arguments)
	if err != nil || envelope.ArgumentDigest != digestBytes(canonical) {
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
	finding := BlockedFinding(AuthorityDecision{Code: code}, "Charlie action requires attention", summary, "Review the finding and current product authorization", "Re-read the affected management-plane resource after any approved action")
	result := ActionResult{Code: code, State: "blocked"}
	if finding.Actionable {
		result.Finding = &finding
	}
	return result
}
