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

type ModeCeilingRollout interface {
	Reconcile(context.Context, ModeCeilingTarget) error
}

// ModeCeilingTarget separates the product-owned local CAS snapshot used to
// authorize a rollout from the least-authority ceiling being deployed. Central
// reconciliation may lower the effective workload ceiling while Requested
// remains the product-owned maximum.
type ModeCeilingTarget struct {
	ConnectionID              string
	ExpectedRequested         Mode
	ExpectedRevision          int64
	ExpectedEmergencyDisabled bool
	Desired                   Mode
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
	store      ModeStore
	bridge     AgentModeBridge
	writes     *WriteFence
	audit      AuthorityMutationAuditor
	rollout    ModeCeilingRollout
	transition chan struct{}
	ticker     func(time.Duration) runtimeTicker
}

func NewModeController(store ModeStore, bridge AgentModeBridge, auditor AuthorityMutationAuditor) (*ModeController, error) {
	if store == nil || bridge == nil || auditor == nil {
		return nil, fmt.Errorf("Charlie mode control requires local state, the product bridge, and durable audit")
	}
	controller := &ModeController{store: store, bridge: bridge, writes: NewWriteFence(), audit: auditor, transition: make(chan struct{}, 1), ticker: newRuntimeTicker}
	// The production product bridge owns exact ceiling application and readback;
	// test bridges may provide the same bounded contract directly.
	if rollout, ok := bridge.(ModeCeilingRollout); ok {
		controller.rollout = rollout
	}
	return controller, nil
}

func (c *ModeController) SetWriteFence(fence *WriteFence) {
	if c != nil && fence != nil {
		c.writes = fence
	}
}

func (c *ModeController) SetModeCeilingRollout(rollout ModeCeilingRollout) {
	if c != nil {
		c.rollout = rollout
	}
}

func (c *ModeController) acquireTransition(ctx context.Context) bool {
	if c == nil || c.transition == nil {
		return false
	}
	select {
	case c.transition <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *ModeController) releaseTransition() {
	<-c.transition
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
	if !c.acquireTransition(ctx) {
		return ModeState{}, fmt.Errorf("Charlie mode request was cancelled before admission")
	}
	defer c.releaseTransition()
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
	// Retain the cross-replica exclusive drain from before the audit through the
	// final CAS/readback. No product write can enter between intent persistence
	// and the workload/central transition.
	_, releaseDrain, drainErr := c.writes.CloseAndHold(ctx)
	if drainErr != nil {
		return current, drainErr
	}
	defer releaseDrain()
	if err := requireAuthorityMutationAudit(ctx, c.audit, AuthorityMutationAudit{
		Action: "charlie.mode.requested", ResourceType: "charlie_mode", ResourceID: current.ConnectionID,
		Fields: map[string]any{"mode": string(desired), "revision": expectedRevision},
	}); err != nil {
		logModeTransitionFailure(ctx, "mode.audit_persist_failed")
		return ModeState{}, fmt.Errorf("Charlie mode audit is unavailable")
	}
	requested := current
	// No product write may straddle a mixed-ceiling rollout. For reductions this
	// closes admission before the lower durable ceiling is committed; for
	// increases it keeps central at the prior lower authority until readback.
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
	if c.rollout == nil {
		logModeTransitionFailure(ctx, "mode.ceiling_rollout_unavailable")
		return requested, fmt.Errorf("Charlie mode-ceiling rollout was not verified")
	}
	if err := c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: desired, ExpectedRevision: requested.Revision, Desired: desired}); err != nil {
		logModeTransitionFailure(ctx, "mode.ceiling_rollout_unavailable")
		return requested, fmt.Errorf("Charlie mode-ceiling rollout was not verified: %w", err)
	}
	latest, loadErr := c.store.LoadModeState(ctx)
	if loadErr != nil || latest.ConnectionID != current.ConnectionID || latest.Requested != desired || latest.Revision != requested.Revision || latest.EmergencyDisabled {
		return requested, fmt.Errorf("Charlie mode state changed during agent rollout")
	}
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
	if !remote.Active {
		logModeTransitionFailure(ctx, "mode.remote_inactive")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request: product is not enabled")
	}
	if remote.Verified != desired {
		logModeTransitionFailure(ctx, "mode.remote_mode_mismatch")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request: central mode is %s", remote.Verified)
	}
	// The product-local CAS and Charlie can advance to the same revision for a
	// single transition. Equality is therefore an authoritative confirmation,
	// while any lower revision is stale and must remain fail-closed.
	if remote.Revision < requested.Revision {
		logModeTransitionFailure(ctx, "mode.remote_revision_stale")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request: central revision %d is behind %d", remote.Revision, requested.Revision)
	}
	if remote.DisclosureDigest == "" {
		logModeTransitionFailure(ctx, "mode.remote_disclosure_missing")
		return requested, fmt.Errorf("Charlie mode readback did not confirm the request: disclosure digest is empty")
	}
	verified, err := c.store.SetVerifiedMode(ctx, current.ConnectionID, remote.Verified, requested.Revision, remote.Revision, remote.DisclosureDigest)
	if err != nil {
		logModeTransitionFailure(ctx, "mode.local_verification_persist_failed")
		return requested, fmt.Errorf("persist Charlie verified mode: %w", err)
	}
	// Disclosure digests are not patched onto agent env after mode raise.
	// Agents already re-read disclosure from central heartbeat/bridge status;
	// a second STS env rewrite would force another full pod rollout for no
	// authority gain (localModeCeiling already rolled once above).
	notifyActivationChanged(ctx, c.bridge)
	final, loadErr := c.store.LoadModeState(ctx)
	if loadErr != nil || final.ConnectionID != current.ConnectionID || !final.Active || final.EmergencyDisabled ||
		final.Requested != desired || final.Verified != remote.Verified || final.Revision != remote.Revision ||
		final.DisclosureDigest != remote.DisclosureDigest {
		return verified, fmt.Errorf("Charlie mode state changed after authoritative readback")
	}
	if EffectiveMode(final.Requested, final.Verified, final.EmergencyDisabled) == ModeApproval ||
		EffectiveMode(final.Requested, final.Verified, final.EmergencyDisabled) == ModeAuto {
		releaseDrain()
		c.writes.Open()
	}
	return final, nil
}

// Reconcile imports a newer Charlie-authoritative mode snapshot without ever
// changing the product-local requested ceiling. A central policy or disclosure
// change can therefore suspend or reduce authority immediately, while a more
// permissive remote mode remains bounded by EffectiveMode.
func (c *ModeController) Reconcile(ctx context.Context) (ModeState, error) {
	if !c.acquireTransition(ctx) {
		return ModeState{}, fmt.Errorf("Charlie mode reconciliation was cancelled before admission")
	}
	defer c.releaseTransition()
	current, err := c.store.LoadModeState(ctx)
	if err != nil {
		return ModeState{}, fmt.Errorf("load Charlie mode state: %w", err)
	}
	if !current.Active || current.EmergencyDisabled {
		if _, drainErr := c.writes.CloseAndWait(ctx); drainErr != nil {
			return current, drainErr
		}
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

	verified, digest, normalizeErr := normalizedCentralMode(remote)
	if normalizeErr != nil {
		return current, fmt.Errorf("Charlie mode status is incomplete")
	}

	if remote.Revision == current.Revision {
		if current.Verified != verified || current.DisclosureDigest != digest {
			_, _ = c.writes.CloseAndWait(ctx)
			return current, fmt.Errorf("Charlie mode status conflicts at the current revision")
		}
		effective := EffectiveMode(current.Requested, current.Verified, current.EmergencyDisabled)
		// read_only/disabled must continuously prove the lower workload ceiling;
		// approval/auto repeats this proof only when recovering a closed fence.
		if effective != ModeApproval && effective != ModeAuto || c.writes.State().Closed {
			_, release, drainErr := c.writes.CloseAndHold(ctx)
			if drainErr != nil {
				return current, drainErr
			}
			defer release()
			if c.rollout == nil || c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: current.Requested, ExpectedRevision: current.Revision, Desired: effective}) != nil {
				return current, fmt.Errorf("Charlie mode-ceiling rollout was not verified")
			}
			confirmed, confirmErr := c.bridge.Status(ctx)
			if confirmErr != nil || !sameCentralSnapshot(remote, confirmed, current.ConnectionID) {
				return current, fmt.Errorf("Charlie mode status changed during agent rollout")
			}
			latest, loadErr := c.store.LoadModeState(ctx)
			if loadErr != nil || !sameLocalModeSnapshot(current, latest) {
				return current, fmt.Errorf("Charlie mode state changed during agent rollout")
			}
			if effective == ModeApproval || effective == ModeAuto {
				release()
				c.writes.Open()
			}
		}
		return current, nil
	}

	// A newer central revision may change executable authority. Close local
	// admission, cancel admitted work, and retain the distributed exclusive lock
	// across audit, all-replica ceiling readback, and the local CAS.
	_, release, drainErr := c.writes.CloseAndHold(ctx)
	if drainErr != nil {
		return current, drainErr
	}
	defer release()
	event := AuthorityMutationAudit{
		Action: "charlie.mode.verified", ResourceType: "charlie_mode", ResourceID: current.ConnectionID,
		Fields: map[string]any{"mode": string(verified), "revision": remote.Revision},
	}
	oldEffective := EffectiveMode(current.Requested, current.Verified, current.EmergencyDisabled)
	newEffective := EffectiveMode(current.Requested, verified, current.EmergencyDisabled)
	if auditErr := requireAuthorityMutationAudit(ctx, c.audit, event); auditErr != nil {
		logModeTransitionFailure(ctx, "mode.audit_persist_failed")
		// Never persist an unaudited executable revision. Otherwise a later
		// same-revision reconciliation could treat that snapshot as established
		// authority and reopen the fence without ever admitting the mutation
		// audit. Read-only/disabled downgrades may still be committed fail-closed.
		if newEffective == ModeApproval || newEffective == ModeAuto {
			return current, fmt.Errorf("Charlie mode audit is unavailable")
		}
		if modeRank(newEffective) > modeRank(oldEffective) {
			return current, fmt.Errorf("Charlie mode audit is unavailable")
		}
	}

	safeCeiling := newEffective

	if modeRank(newEffective) <= modeRank(oldEffective) {
		// Downgrades, suspension, and same-effective revision/disclosure changes
		// publish the fail-closed DB authority first while every shared writer is
		// drained. If rollout fails, every replica still denies after lock release.
		reconciled, persistErr := c.store.SetVerifiedMode(ctx, current.ConnectionID, verified, current.Revision, remote.Revision, digest)
		if persistErr != nil {
			return current, fmt.Errorf("persist reconciled Charlie mode: %w", persistErr)
		}
		notifyActivationChanged(ctx, c.bridge)
		final, loadErr := c.store.LoadModeState(ctx)
		if loadErr != nil || !matchesReconciledMode(current, final, verified, remote.Revision, digest, safeCeiling) {
			return reconciled, fmt.Errorf("Charlie mode state changed after central reconciliation")
		}
		if c.rollout == nil || c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: final.Requested, ExpectedRevision: final.Revision, Desired: safeCeiling}) != nil {
			logModeTransitionFailure(ctx, "mode.ceiling_rollout_unavailable")
			return final, fmt.Errorf("Charlie mode-ceiling rollout was not verified")
		}
		confirmed, confirmErr := c.bridge.Status(ctx)
		if confirmErr != nil || !sameCentralSnapshot(remote, confirmed, current.ConnectionID) {
			return final, fmt.Errorf("Charlie mode status changed during agent rollout")
		}
		latest, loadErr := c.store.LoadModeState(ctx)
		if loadErr != nil || !sameLocalModeSnapshot(final, latest) {
			return final, fmt.Errorf("Charlie mode state changed during agent rollout")
		}
		// A newer central revision can preserve an executable effective mode
		// (for example, an audited safety-policy update while approval mode
		// remains active). The reconciliation fence is required while the new
		// revision is persisted and rolled out, but it must be reopened after
		// exact readback succeeds. Lower read-only and disabled ceilings stay
		// closed.
		if safeCeiling == ModeApproval || safeCeiling == ModeAuto {
			release()
			c.writes.Open()
		}
		return final, nil
	}

	// Increases deploy and read back the exact effective ceiling against the
	// still-lower local snapshot. Only then may the higher central revision CAS.
	if c.rollout == nil || c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: current.Requested, ExpectedRevision: current.Revision, Desired: safeCeiling}) != nil {
		logModeTransitionFailure(ctx, "mode.ceiling_rollout_unavailable")
		return current, fmt.Errorf("Charlie mode-ceiling rollout was not verified")
	}
	confirmed, confirmErr := c.bridge.Status(ctx)
	if confirmErr != nil || !sameCentralSnapshot(remote, confirmed, current.ConnectionID) {
		return current, fmt.Errorf("Charlie mode status changed during agent rollout")
	}
	latest, loadErr := c.store.LoadModeState(ctx)
	if loadErr != nil || !sameLocalModeSnapshot(current, latest) {
		return current, fmt.Errorf("Charlie mode state changed during agent rollout")
	}
	reconciled, err := c.store.SetVerifiedMode(ctx, current.ConnectionID, verified, current.Revision, remote.Revision, digest)
	if err != nil {
		return current, fmt.Errorf("persist reconciled Charlie mode: %w", err)
	}
	notifyActivationChanged(ctx, c.bridge)
	final, loadErr := c.store.LoadModeState(ctx)
	if loadErr != nil || !matchesReconciledMode(current, final, verified, remote.Revision, digest, safeCeiling) {
		return reconciled, fmt.Errorf("Charlie mode state changed after central reconciliation")
	}
	release()
	c.writes.Open()
	return final, nil
}

func matchesReconciledMode(previous, final ModeState, verified Mode, revision int64, digest string, effective Mode) bool {
	return final.ConnectionID == previous.ConnectionID && final.Active && !final.EmergencyDisabled &&
		final.Requested == previous.Requested && final.Verified == verified && final.Revision == revision &&
		final.DisclosureDigest == digest && EffectiveMode(final.Requested, final.Verified, final.EmergencyDisabled) == effective
}

func normalizedCentralMode(remote ModeState) (Mode, string, error) {
	if !remote.Active {
		// An inactive central integration has no executable disclosure. Never
		// retain a previously acknowledged digest across that boundary.
		return ModeDisabled, "", nil
	}
	if !validMode(remote.Verified) {
		return ModeDisabled, "", fmt.Errorf("invalid central mode")
	}
	if remote.Verified == ModeDisabled {
		return ModeDisabled, "", nil
	}
	if remote.DisclosureDigest == "" {
		return ModeDisabled, "", fmt.Errorf("missing disclosure")
	}
	return remote.Verified, remote.DisclosureDigest, nil
}

func sameCentralSnapshot(expected, actual ModeState, connectionID string) bool {
	if actual.ConnectionID != "" && actual.ConnectionID != connectionID {
		return false
	}
	expectedMode, expectedDigest, expectedErr := normalizedCentralMode(expected)
	actualMode, actualDigest, actualErr := normalizedCentralMode(actual)
	return expectedErr == nil && actualErr == nil && expected.Active == actual.Active &&
		expected.Revision == actual.Revision && expectedMode == actualMode && expectedDigest == actualDigest
}

func sameLocalModeSnapshot(expected, actual ModeState) bool {
	return expected.ConnectionID == actual.ConnectionID && expected.Active == actual.Active &&
		expected.EmergencyDisabled == actual.EmergencyDisabled && expected.Requested == actual.Requested &&
		expected.Verified == actual.Verified && expected.Revision == actual.Revision &&
		expected.DisclosureDigest == actual.DisclosureDigest
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
	if !c.acquireTransition(ctx) {
		return ModeState{}, fmt.Errorf("Charlie emergency disable was cancelled before admission")
	}
	defer c.releaseTransition()
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
	_, releaseDrain, drainErr := c.writes.CloseAndHold(ctx)
	if drainErr != nil {
		return current, drainErr
	}
	defer releaseDrain()
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
	if c.rollout == nil || c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: ModeDisabled, ExpectedRevision: disabled.Revision, ExpectedEmergencyDisabled: true, Desired: ModeDisabled}) != nil {
		return disabled, fmt.Errorf("Charlie is locally disabled; agent ceiling confirmation is pending")
	}
	latest, loadErr := c.store.LoadModeState(ctx)
	if loadErr != nil || latest.ConnectionID != current.ConnectionID || !latest.EmergencyDisabled || latest.Requested != ModeDisabled || latest.Revision != disabled.Revision {
		return disabled, fmt.Errorf("Charlie is locally disabled; mode state changed during agent rollout")
	}
	remote, bridgeErr := c.bridge.SetMode(ctx, ModeDisabled, disabled.Revision)
	if bridgeErr != nil {
		return disabled, fmt.Errorf("Charlie is locally disabled; remote confirmation is pending")
	}
	if remote.ConnectionID != "" && remote.ConnectionID != current.ConnectionID ||
		remote.Revision < disabled.Revision || EffectiveMode(remote.Requested, remote.Verified, remote.EmergencyDisabled) != ModeDisabled {
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
	if !c.acquireTransition(ctx) {
		return ModeState{}, fmt.Errorf("Charlie emergency disable recovery was cancelled before admission")
	}
	defer c.releaseTransition()
	current, err := c.store.LoadModeState(ctx)
	if err != nil || !current.Active || !current.EmergencyDisabled {
		return ModeState{}, fmt.Errorf("Charlie emergency disable is not active")
	}
	if c.rollout == nil || c.rollout.Reconcile(ctx, ModeCeilingTarget{ConnectionID: current.ConnectionID, ExpectedRequested: current.Requested, ExpectedRevision: current.Revision, ExpectedEmergencyDisabled: true, Desired: ModeDisabled}) != nil {
		return current, fmt.Errorf("Charlie agent has not confirmed disabled mode ceiling")
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
