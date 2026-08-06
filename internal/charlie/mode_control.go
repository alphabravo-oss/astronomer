package charlie

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ModeState struct {
	ConnectionID      string
	Active            bool
	EmergencyDisabled bool
	Requested         Mode
	Verified          Mode
	Revision          int64
	DisclosureDigest  string
	UpdatedAt         time.Time
}

type ModePrerequisites struct {
	DisclosureAcknowledged   bool
	AutomationAllowlistReady bool
	AutomationIdentityReady  bool
	AutomationTargetReady    bool
}

type ModeStore interface {
	LoadModeState(context.Context) (ModeState, error)
	SetRequestedMode(context.Context, string, Mode, int64) (ModeState, error)
	SetVerifiedMode(context.Context, string, Mode, int64, int64, string) (ModeState, error)
	SetEmergencyDisabled(context.Context, string, string) (ModeState, error)
	ClearEmergencyDisabled(context.Context, string, string) (ModeState, error)
}

type AgentModeBridge interface {
	SetMode(context.Context, Mode, int64) (ModeState, error)
	Status(context.Context) (ModeState, error)
}

type activationChangeNotifier interface {
	activationChanged(context.Context)
}

func notifyActivationChanged(ctx context.Context, bridge AgentModeBridge) {
	if notifier, ok := bridge.(activationChangeNotifier); ok {
		notifier.activationChanged(ctx)
	}
}

type ModeController struct {
	store  ModeStore
	bridge AgentModeBridge
	writes *WriteFence
	audit  AuthorityMutationAuditor
	ticker func(time.Duration) runtimeTicker
}

func NewModeController(store ModeStore, bridge AgentModeBridge, auditor AuthorityMutationAuditor) (*ModeController, error) {
	if store == nil || bridge == nil || auditor == nil {
		return nil, fmt.Errorf("Charlie mode control requires local state, the product bridge, and durable audit")
	}
	return &ModeController{store: store, bridge: bridge, writes: NewWriteFence(), audit: auditor, ticker: newRuntimeTicker}, nil
}

func (c *ModeController) SetWriteFence(fence *WriteFence) {
	if c != nil && fence != nil {
		c.writes = fence
	}
}

// EffectiveMode returns the least authority reported by the product-local and
// Charlie-authoritative states. Drift can only reduce authority.
func EffectiveMode(requested, verified Mode, emergency bool) Mode {
	if emergency || !validMode(requested) || !validMode(verified) {
		return ModeDisabled
	}
	if modeRank(requested) < modeRank(verified) {
		return requested
	}
	return verified
}

func (c *ModeController) Request(ctx context.Context, desired Mode, expectedRevision int64, prerequisites ModePrerequisites) (ModeState, error) {
	if !validMode(desired) || expectedRevision < 0 {
		logModeTransitionFailure(ctx, "mode.request_invalid")
		return ModeState{}, fmt.Errorf("Charlie mode request is invalid")
	}
	current, err := c.store.LoadModeState(ctx)
	if err != nil || !current.Active || current.EmergencyDisabled {
		logModeTransitionFailure(ctx, "mode.integration_inactive")
		return ModeState{}, fmt.Errorf("Charlie integration is inactive")
	}
	if current.Revision != expectedRevision {
		logModeTransitionFailure(ctx, "mode.local_revision_conflict")
		return ModeState{}, fmt.Errorf("Charlie mode revision changed")
	}
	if desired == ModeAuto && (!prerequisites.DisclosureAcknowledged || !prerequisites.AutomationAllowlistReady || !prerequisites.AutomationIdentityReady || !prerequisites.AutomationTargetReady) {
		logModeTransitionFailure(ctx, "mode.auto_prerequisites_incomplete")
		return ModeState{}, fmt.Errorf("Charlie auto mode prerequisites are incomplete")
	}
	if err := requireAuthorityMutationAudit(ctx, c.audit, AuthorityMutationAudit{
		Action: "charlie.mode.requested", ResourceType: "charlie_mode", ResourceID: current.ConnectionID,
		Fields: map[string]any{"mode": string(desired), "revision": expectedRevision},
	}); err != nil {
		logModeTransitionFailure(ctx, "mode.audit_persist_failed")
		return ModeState{}, fmt.Errorf("Charlie mode audit is unavailable")
	}
	requested := current
	// A prior attempt may have committed the least-authority local request and
	// then lost its authoritative readback. Retry that same revision instead of
	// advancing the local CAS again; this lets an idempotent central transition
	// be reconciled without weakening the stale-revision check.
	if current.Requested != desired {
		requested, err = c.store.SetRequestedMode(ctx, current.ConnectionID, desired, expectedRevision)
		if err != nil {
			logModeTransitionFailure(ctx, "mode.local_request_persist_failed")
			return ModeState{}, fmt.Errorf("persist Charlie requested mode: %w", err)
		}
	}
	// The local requested ceiling is already authoritative for reductions. Wake
	// the runtime owner before any remote call so a disable cannot remain live
	// while signed confirmation is slow or unavailable.
	notifyActivationChanged(ctx, c.bridge)
	remote, err := c.bridge.SetMode(ctx, desired, requested.Revision)
	if err != nil {
		// The requested/verified intersection prevents authority escalation while
		// the agent or central is unavailable. A later reconciliation may retry.
		logModeTransitionFailure(ctx, "mode.remote_readback_unavailable")
		return requested, fmt.Errorf("Charlie mode readback unavailable")
	}
	if remote.ConnectionID != "" && remote.ConnectionID != current.ConnectionID {
		logModeTransitionFailure(ctx, "mode.remote_installation_changed")
		return requested, fmt.Errorf("Charlie mode readback installation changed")
	}
	if remote.Verified != desired {
		logModeTransitionFailure(ctx, "mode.remote_mode_mismatch")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request")
	}
	// The product-local CAS and Charlie can advance to the same revision for a
	// single transition. Equality is therefore an authoritative confirmation,
	// while any lower revision is stale and must remain fail-closed.
	if remote.Revision < requested.Revision {
		logModeTransitionFailure(ctx, "mode.remote_revision_stale")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request")
	}
	if remote.DisclosureDigest == "" {
		logModeTransitionFailure(ctx, "mode.remote_disclosure_missing")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request")
	}
	verified, err := c.store.SetVerifiedMode(ctx, current.ConnectionID, remote.Verified, requested.Revision, remote.Revision, remote.DisclosureDigest)
	if err != nil {
		logModeTransitionFailure(ctx, "mode.local_verification_persist_failed")
		return requested, fmt.Errorf("persist Charlie verified mode: %w", err)
	}
	notifyActivationChanged(ctx, c.bridge)
	return verified, nil
}

// Reconcile imports a newer Charlie-authoritative mode snapshot without ever
// changing the product-local requested ceiling. A central policy or disclosure
// change can therefore suspend or reduce authority immediately, while a more
// permissive remote mode remains bounded by EffectiveMode.
func (c *ModeController) Reconcile(ctx context.Context) (ModeState, error) {
	current, err := c.store.LoadModeState(ctx)
	if err != nil {
		return ModeState{}, fmt.Errorf("load Charlie mode state: %w", err)
	}
	if !current.Active || current.EmergencyDisabled {
		return current, nil
	}
	remote, err := c.bridge.Status(ctx)
	if err != nil {
		return current, fmt.Errorf("Charlie mode status unavailable")
	}
	if remote.ConnectionID != "" && remote.ConnectionID != current.ConnectionID {
		return current, fmt.Errorf("Charlie mode status installation changed")
	}
	if remote.Revision < 1 || remote.Revision < current.Revision {
		return current, fmt.Errorf("Charlie mode status revision is stale")
	}

	verified := remote.Verified
	digest := remote.DisclosureDigest
	if !remote.Active {
		// An inactive central integration has no executable disclosure. Never
		// retain a previously acknowledged digest across that boundary.
		verified = ModeDisabled
		digest = ""
	} else if !validMode(verified) || verified == ModeDisabled || digest == "" {
		return current, fmt.Errorf("Charlie mode status is incomplete")
	}

	if remote.Revision == current.Revision {
		if current.Verified != verified || current.DisclosureDigest != digest {
			return current, fmt.Errorf("Charlie mode status conflicts at the current revision")
		}
		return current, nil
	}
	event := AuthorityMutationAudit{
		Action: "charlie.mode.verified", ResourceType: "charlie_mode", ResourceID: current.ConnectionID,
		Fields: map[string]any{"mode": string(verified), "revision": remote.Revision},
	}
	oldEffective := EffectiveMode(current.Requested, current.Verified, current.EmergencyDisabled)
	newEffective := EffectiveMode(current.Requested, verified, current.EmergencyDisabled)
	if auditErr := requireAuthorityMutationAudit(ctx, c.audit, event); auditErr != nil {
		logModeTransitionFailure(ctx, "mode.audit_persist_failed")
		if modeRank(newEffective) > modeRank(oldEffective) {
			return current, fmt.Errorf("Charlie mode audit is unavailable")
		}
	}

	reconciled, err := c.store.SetVerifiedMode(ctx, current.ConnectionID, verified, current.Revision, remote.Revision, digest)
	if err != nil {
		return current, fmt.Errorf("persist reconciled Charlie mode: %w", err)
	}
	notifyActivationChanged(ctx, c.bridge)
	return reconciled, nil
}

// Run periodically reconciles Charlie's signed integration revision into the
// local least-authority state. Errors never reopen authority and are logged as
// bounded failure codes without remote payloads or credentials.
func (c *ModeController) Run(ctx context.Context, interval time.Duration) {
	if c == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := c.ticker(interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := c.Reconcile(ctx); err != nil {
			failures++
			if failures == 1 || failures%20 == 0 {
				logModeTransitionFailure(ctx, "mode.reconcile_failed")
			}
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
		}
	}
}

func logModeTransitionFailure(ctx context.Context, code string) {
	LogOperationalFailure(ctx, nil, code, "")
}

// EmergencyDisable closes the product-local latch first. Failure to contact the
// agent cannot keep sessions, triggers, findings, approvals, claims, or MCP
// calls active. Remote disable is attempted only after the local commit.
func (c *ModeController) EmergencyDisable(ctx context.Context, actorID string) (ModeState, error) {
	// Close admission before reading or mutating mode state. Any write that won
	// the admission race is registered and will be cancelled and drained below;
	// every later write is rejected.
	c.writes.Close()
	current, err := c.store.LoadModeState(ctx)
	if err != nil {
		return ModeState{}, fmt.Errorf("Charlie integration is inactive")
	}
	if !current.Active {
		if current.EmergencyDisabled {
			if _, drainErr := c.writes.CloseAndWait(ctx); drainErr != nil {
				return current, drainErr
			}
			return current, nil
		}
		return ModeState{}, fmt.Errorf("Charlie integration is inactive")
	}
	actor, _ := uuid.Parse(actorID)
	_ = requireAuthorityMutationAudit(ctx, c.audit, AuthorityMutationAudit{
		Action: "charlie.mode.emergency_disabled", ResourceType: "charlie_mode", ResourceID: current.ConnectionID, ActorID: actor,
		Fields: map[string]any{"mode": string(ModeDisabled), "revision": current.Revision},
	})
	disabled, err := c.store.SetEmergencyDisabled(ctx, current.ConnectionID, actorID)
	if err != nil {
		return ModeState{}, fmt.Errorf("persist Charlie emergency disable: %w", err)
	}
	notifyActivationChanged(ctx, c.bridge)
	if _, err := c.writes.CloseAndWait(ctx); err != nil {
		return disabled, err
	}
	_, bridgeErr := c.bridge.SetMode(ctx, ModeDisabled, disabled.Revision)
	if bridgeErr != nil {
		return disabled, fmt.Errorf("Charlie is locally disabled; remote confirmation is pending")
	}
	return disabled, nil
}

// ClearEmergencyDisable is intentionally two-step: the agent must report
// authoritative disabled mode before the product-local latch can be cleared.
// If an earlier remote-disable attempt was interrupted, this explicit recovery
// operation retries only that authority-reducing transition and verifies its
// readback. It never restores the prior authority mode.
func (c *ModeController) ClearEmergencyDisable(ctx context.Context, actorID string) (ModeState, error) {
	current, err := c.store.LoadModeState(ctx)
	if err != nil || !current.Active || !current.EmergencyDisabled {
		return ModeState{}, fmt.Errorf("Charlie emergency disable is not active")
	}
	remote, err := c.bridge.Status(ctx)
	if err != nil {
		return current, fmt.Errorf("Charlie agent has not confirmed disabled mode")
	}
	if EffectiveMode(remote.Requested, remote.Verified, remote.EmergencyDisabled) != ModeDisabled {
		remote, err = c.bridge.SetMode(ctx, ModeDisabled, current.Revision)
		if err != nil || EffectiveMode(remote.Requested, remote.Verified, remote.EmergencyDisabled) != ModeDisabled {
			return current, fmt.Errorf("Charlie agent has not confirmed disabled mode")
		}
	}
	actor, _ := uuid.Parse(actorID)
	if err := requireAuthorityMutationAudit(ctx, c.audit, AuthorityMutationAudit{
		Action: "charlie.mode.requested", ResourceType: "charlie_mode", ResourceID: current.ConnectionID, ActorID: actor,
		Fields: map[string]any{"mode": string(ModeDisabled), "revision": current.Revision},
	}); err != nil {
		logModeTransitionFailure(ctx, "mode.audit_persist_failed")
		return current, fmt.Errorf("Charlie mode audit is unavailable")
	}
	cleared, err := c.store.ClearEmergencyDisabled(ctx, current.ConnectionID, actorID)
	if err != nil {
		return current, fmt.Errorf("clear Charlie emergency disable: %w", err)
	}
	notifyActivationChanged(ctx, c.bridge)
	if cleared.Requested != ModeDisabled || cleared.Verified != ModeDisabled {
		return current, fmt.Errorf("cleared Charlie state attempted to restore authority")
	}
	c.writes.Open()
	return cleared, nil
}

func validMode(mode Mode) bool {
	return mode == ModeDisabled || mode == ModeReadOnly || mode == ModeApproval || mode == ModeAuto
}

func modeRank(mode Mode) int {
	switch mode {
	case ModeReadOnly:
		return 1
	case ModeApproval:
		return 2
	case ModeAuto:
		return 3
	default:
		return 0
	}
}
