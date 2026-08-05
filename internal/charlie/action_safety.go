package charlie

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/jackc/pgx/v5"
)

const approvalWriteCooldown = 30 * time.Second

type productActionSafetyQueries interface {
	GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error)
	GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error)
	GetCharlieAutomationPolicy(context.Context, sqlc.GetCharlieAutomationPolicyParams) (sqlc.CharlieAutomationPolicy, error)
	GetCharlieActionSafetySnapshot(context.Context, sqlc.GetCharlieActionSafetySnapshotParams) (sqlc.GetCharlieActionSafetySnapshotRow, error)
	ReserveCharlieAutoBudget(context.Context, sqlc.ReserveCharlieAutoBudgetParams) (sqlc.CharlieActionReceipt, error)
	CreateCharlieActionDeferral(context.Context, sqlc.CreateCharlieActionDeferralParams) (sqlc.CharlieActionDeferral, error)
}

// ProductActionSafety applies Astronomer-owned maintenance windows and durable
// receipt-backed concurrency, cooldown, circuit-breaker, and auto-budget rules.
// An absent automation policy is a deliberate deny, never an implicit default.
type ProductActionSafety struct {
	queries productActionSafetyQueries
	windows maintenance.WindowEvaluator
	now     func() time.Time
}

func NewProductActionSafety(queries productActionSafetyQueries, windows maintenance.WindowEvaluator) (*ProductActionSafety, error) {
	if queries == nil || windows == nil {
		return nil, fmt.Errorf("Charlie action safety requires durable product state and maintenance windows")
	}
	return &ProductActionSafety{queries: queries, windows: windows, now: time.Now}, nil
}

type ActionDeferredError struct {
	OperationID   string
	DeferredUntil time.Time
	ExpiresAt     time.Time
}

func (e *ActionDeferredError) Error() string { return "Charlie action deferred by maintenance policy" }

func (e *ActionDeferredError) Result() json.RawMessage {
	result, _ := json.Marshal(map[string]string{
		"operation_id":   e.OperationID,
		"status_url":     "/api/v1/charlie/operations/" + e.OperationID,
		"deferred_until": e.DeferredUntil.UTC().Format(time.RFC3339),
		"expires_at":     e.ExpiresAt.UTC().Format(time.RFC3339),
	})
	return result
}

func (s *ProductActionSafety) Evaluate(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (SafetyFacts, error) {
	facts := SafetyFacts{ScopeAllowed: true, CooldownClear: true, CircuitClosed: true, PreconditionsMet: true}
	if capability.Effect != EffectWrite {
		return facts, nil
	}
	connection, session, err := s.binding(ctx, action)
	if err != nil {
		return SafetyFacts{}, err
	}
	blocked, window, err := maintenance.IsBlocked(ctx, s.windows, maintenance.OpCharlieAction, nil, s.now().UTC())
	if err != nil {
		return SafetyFacts{}, fmt.Errorf("evaluate Charlie maintenance policy: %w", err)
	}
	if blocked && window != nil && window.OnBlock == maintenance.OnBlockRefuse {
		maintenance.RecordBlocked(maintenance.OpCharlieAction, window.Mode)
		facts.PreconditionsMet = false
	}

	policy, policyErr := s.queries.GetCharlieAutomationPolicy(ctx, sqlc.GetCharlieAutomationPolicyParams{
		ConnectionID: connection.ID, Capability: capability.Name,
	})
	policyFound := policyErr == nil && policy.Enabled
	if policyErr != nil && !errors.Is(policyErr, pgx.ErrNoRows) {
		return SafetyFacts{}, policyErr
	}
	cooldown := int32(approvalWriteCooldown / time.Second)
	maxIncident, maxWindow, windowSeconds := int32(1), int32(1), int32(60)
	if policyFound {
		cooldown = policy.CooldownSeconds
		maxIncident, maxWindow, windowSeconds = policy.MaxActionsPerIncident, policy.MaxActionsPerWindow, policy.BudgetWindowSeconds
	}
	snapshot, err := s.queries.GetCharlieActionSafetySnapshot(ctx, sqlc.GetCharlieActionSafetySnapshotParams{
		ActionIDArg: action.ActionID, SessionIDArg: session.ID, CapabilityArg: capability.Name,
		ResourceDigestArg: resourceDigest(capability.Name, arguments), CooldownSeconds: cooldown,
		MaxActionsPerIncident: maxIncident, MaxActionsPerWindow: maxWindow, BudgetWindowSeconds: windowSeconds,
	})
	if err != nil {
		return SafetyFacts{}, err
	}
	facts.Allowlisted = policyFound
	facts.BudgetAvailable = policyFound && snapshot.IncidentBudgetAvailable && snapshot.WindowBudgetAvailable
	facts.CooldownClear = snapshot.CooldownClear
	facts.CircuitClosed = snapshot.CircuitClosed
	facts.PreconditionsMet = facts.PreconditionsMet && snapshot.IncidentClear && snapshot.CooldownClear && snapshot.CircuitClosed
	return facts, nil
}

func (s *ProductActionSafety) ConsumeAutoBudget(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage) error {
	return s.CommitWrite(ctx, action, capability, arguments, ModeAuto)
}

func (s *ProductActionSafety) CommitWrite(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor, arguments map[string]json.RawMessage, mode Mode) error {
	if capability.Effect != EffectWrite {
		return nil
	}
	connection, session, err := s.binding(ctx, action)
	if err != nil {
		return err
	}
	blocked, window, err := maintenance.IsBlocked(ctx, s.windows, maintenance.OpCharlieAction, nil, s.now().UTC())
	if err != nil {
		return fmt.Errorf("re-evaluate Charlie maintenance policy: %w", err)
	}
	if blocked && window != nil {
		if window.OnBlock != maintenance.OnBlockDefer {
			return fmt.Errorf("Charlie action blocked by maintenance policy")
		}
		maintenance.RecordBlocked(maintenance.OpCharlieAction, window.Mode)
		deferredUntil := maintenance.NextOpen(*window, s.now().UTC())
		if window.Mode == maintenance.ModeBlackout {
			deferredUntil = maintenance.NextClose(*window, s.now().UTC())
		}
		if deferredUntil.IsZero() {
			return fmt.Errorf("Charlie maintenance deferral has no bounded retry time")
		}
		expiresAt := deferredUntil.Add(24 * time.Hour)
		row, createErr := s.queries.CreateCharlieActionDeferral(ctx, sqlc.CreateCharlieActionDeferralParams{
			CharlieActionID: action.ActionID, WindowID: window.ID,
			DeferredUntil: deferredUntil.UTC(), ExpiresAt: expiresAt.UTC(),
		})
		if createErr != nil {
			return fmt.Errorf("persist Charlie maintenance deferral: %w", createErr)
		}
		maintenance.RecordDeferred(maintenance.OpCharlieAction)
		return &ActionDeferredError{OperationID: row.CharlieActionID, DeferredUntil: row.DeferredUntil, ExpiresAt: row.ExpiresAt}
	}
	if mode != ModeAuto {
		return nil
	}
	_, err = s.queries.ReserveCharlieAutoBudget(ctx, sqlc.ReserveCharlieAutoBudgetParams{
		SessionIDArg: session.ID, ConnectionIDArg: connection.ID, CapabilityArg: capability.Name,
		ActionIDArg: action.ActionID, ResourceDigestArg: resourceDigest(capability.Name, arguments),
	})
	if err != nil {
		return fmt.Errorf("Charlie auto safety claim denied: %w", err)
	}
	return nil
}

func (s *ProductActionSafety) binding(ctx context.Context, action ActionEnvelope) (sqlc.CharlieConnection, sqlc.CharlieSession, error) {
	connection, err := s.queries.GetCharlieConnectionByDeploymentID(ctx, action.DeploymentID)
	if err != nil || !connection.Active || connection.EmergencyDisabled {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, fmt.Errorf("Charlie safety connection is inactive")
	}
	session, err := s.queries.GetCharlieSessionByCentralID(ctx, action.SessionID)
	if err != nil || session.ConnectionID != connection.ID || (session.State != "active" && session.State != "waiting_approval") {
		return sqlc.CharlieConnection{}, sqlc.CharlieSession{}, fmt.Errorf("Charlie safety session is inactive")
	}
	return connection, session, nil
}

func argumentsForResourceDigest(raw json.RawMessage) map[string]json.RawMessage {
	var arguments map[string]json.RawMessage
	if json.Unmarshal(raw, &arguments) != nil {
		return nil
	}
	return arguments
}

func resourceDigest(capability string, arguments map[string]json.RawMessage) string {
	// Write cooldowns and automatic budgets are scoped to the product-disclosed
	// resource, not to mutable adapter operation details. ArgumentDigest still
	// binds the full exact action independently.
	if resourceID, ok := arguments["resource_id"]; ok {
		hash := sha256.New()
		_, _ = hash.Write([]byte(capability))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte("resource_id"))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(resourceID)
		return hex.EncodeToString(hash.Sum(nil))
	}
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		if key != "operation_id" && key != "replicas" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	hash := sha256.New()
	_, _ = hash.Write([]byte(capability))
	for _, key := range keys {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(arguments[key])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var _ LiveActionSafety = (*ProductActionSafety)(nil)
var _ liveWriteSafetyCommitter = (*ProductActionSafety)(nil)
