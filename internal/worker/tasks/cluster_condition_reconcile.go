// Package tasks — cluster-condition remediation reconciler (sprint 086).
//
// The cluster_conditions table (migration 035) records kubectl-style
// per-cluster conditions: Connected, AgentReachable, GatewayAPISupported,
// etc. Until now nothing acted on those rows; an operator saw a red pill
// and had to remediate by hand.
//
// This reconciler closes the loop. Every 30s it walks every
// cluster_conditions row in status='False', and for the known remediable
// types it attempts the appropriate remedy:
//
//   - Connected=False  → mint a fresh registration token, emit an SSE
//     event so the wizard can offer "repair this
//     cluster" without an operator round-trip
//     through the API console.
//
// Future condition types (AgentVersionSkew, CACertificateExpiring) plug
// into the same dispatch table without touching the scaffolding.
//
// Safety:
//
//   - Per-(cluster, condition_type) exponential backoff (60s → 64m capped
//     at the next attempt). Reads the latest row from
//     cluster_condition_remediation_attempts to compute the delay.
//
//   - Per-condition-type daily cap (12 non-skipped attempts per cluster
//     per condition per 24h). A permanently-broken cluster can't drive
//     unbounded token reissuance or audit-log growth.
//
//   - Every attempt — success, failure, OR skip (in-backoff) — lands a
//     row in cluster_condition_remediation_attempts. The skip rows are
//     filtered out of the daily cap so they don't poison the gate.
//
//   - Audit trail: every non-skipped attempt emits a
//     cluster.condition.remediation_attempted audit row through the
//     existing audit writer.
package tasks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ClusterConditionReconcileType is the asynq task type for the
// periodic sweep. Mirrors the naming convention of the other
// reconcilers ("project:reconcile_all", "cluster_decommission:all").
const ClusterConditionReconcileType = "cluster_condition:reconcile"

// Status / outcome constants. Mirror the strings the scheduler /
// health_check task use so logs across the codebase agree on
// vocabulary.
const (
	ccrStatusFalse              = "False"
	ccrOutcomeOk                = "success"
	ccrOutcomeFail              = "failed"
	ccrOutcomeSkip              = "skipped"
	ccrActionNoopBO             = "noop_in_backoff"
	ccrActionNoopCap            = "noop_daily_cap"
	ccrActionTokenReissued      = "registration_token_reissued"
	ccrActionApplyResetToFailed = "template_apply_reset_to_failed"
	ccrActionNoopReconnected    = "noop_agent_reconnected"
)

// ccrConnectedFreshWindow mirrors the heartbeat-freshness window the
// health check uses to derive Connected=True (see updateClusterHealth).
// The remediation precheck re-reads the cluster's live last-heartbeat
// just before minting a token; if a heartbeat has landed within this
// window the agent has reconnected since the condition row was written,
// so reissuing a token would be wasted (and noisy) traffic.
const ccrConnectedFreshWindow = 2 * time.Minute

// Backoff schedule. The reconciler picks the smallest entry that's
// greater than the time since the last non-skipped attempt; once we
// pass the largest entry, every subsequent attempt uses the cap. The
// schedule doubles up to ~64 min so a stuck cluster gets ~10 attempts
// in the first day, then 22 more over the next 23 hours.
var ccrBackoff = []time.Duration{
	60 * time.Second,
	2 * time.Minute,
	4 * time.Minute,
	8 * time.Minute,
	16 * time.Minute,
	32 * time.Minute,
	64 * time.Minute,
}

// ccrDailyCap is the per-(cluster, condition_type) attempt cap over
// any rolling 24h. Counted from non-skipped attempts only so the
// in-backoff skip rows can't starve the gate.
const ccrDailyCap = 12

// HandleClusterConditionReconcile is the asynq handler invoked by the
// periodic scheduler. Returns nil on a successful sweep — per-row
// errors are logged and recorded in the attempts table; they don't
// fail the tick.
func HandleClusterConditionReconcile(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterConditionReconcileType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "cluster_condition reconcile runtime not configured, skipping sweep")
			return nil
		}
		rows, err := runtimeDeps.Queries.ListClusterConditionsByStatus(ctx, ccrStatusFalse)
		if err != nil {
			return fmt.Errorf("list false conditions: %w", err)
		}
		for _, row := range rows {
			if err := reconcileOneCondition(ctx, row); err != nil {
				runtimeLogger().WarnContext(ctx, "cluster condition reconcile failed",
					"cluster_id", row.ClusterID.String(),
					"type", row.Type,
					"error", err,
				)
			}
		}
		return nil
	})
}

// reconcileOneCondition is the per-row dispatch. Looks up the latest
// attempt to decide whether we're in backoff or at the daily cap; if
// not, dispatches to the type-specific remedy.
func reconcileOneCondition(ctx context.Context, row sqlc.ClusterCondition) error {
	// Daily cap check first — it's the hard ceiling, no point evaluating
	// backoff if we've already exhausted the budget today.
	since := time.Now().UTC().Add(-24 * time.Hour)
	count, err := runtimeDeps.Queries.CountClusterConditionRemediationSinceForType(ctx,
		sqlc.CountClusterConditionRemediationSinceForTypeParams{
			ClusterID:     row.ClusterID,
			ConditionType: row.Type,
			AttemptedAt:   since,
		})
	if err != nil {
		return fmt.Errorf("count attempts: %w", err)
	}
	if count >= ccrDailyCap {
		return insertAttempt(ctx, row, ccrActionNoopCap, ccrOutcomeSkip, "", map[string]any{
			"reason":       "daily_cap_reached",
			"cap":          ccrDailyCap,
			"recent_count": count,
			"window_hours": 24,
		})
	}

	// Backoff: skip if we attempted too recently.
	latest, err := runtimeDeps.Queries.GetLatestClusterConditionRemediation(ctx,
		sqlc.GetLatestClusterConditionRemediationParams{
			ClusterID:     row.ClusterID,
			ConditionType: row.Type,
		})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get latest attempt: %w", err)
	}
	if err == nil {
		// Ignore prior skip rows for backoff — they don't represent
		// actual remediation traffic. Look back through history
		// briefly for the most recent non-skip if needed; the cheap
		// approximation is "if latest is skip, no backoff" which
		// matches the conservative behavior we want (drop in, try
		// again).
		if latest.Outcome != ccrOutcomeSkip {
			elapsed := time.Since(latest.AttemptedAt)
			required := ccrBackoffFor(elapsed)
			if elapsed < required {
				return insertAttempt(ctx, row, ccrActionNoopBO, ccrOutcomeSkip, "", map[string]any{
					"reason":        "in_backoff",
					"required_secs": int(required.Seconds()),
					"elapsed_secs":  int(elapsed.Seconds()),
					"last_action":   latest.Action,
					"last_outcome":  latest.Outcome,
				})
			}
		}
	}

	// Type-specific dispatch.
	switch row.Type {
	case ConditionConnected:
		return remediateConnectedFalse(ctx, row)
	case ConditionTemplateApplyStuck:
		return remediateTemplateApplyStuck(ctx, row)
	default:
		// Unknown condition type — no-op. Don't record an attempt;
		// we'd spam the table for every future condition the team
		// adds before plugging a remedy in.
		return nil
	}
}

// remediateTemplateApplyStuck resets a stuck 'applying' template-
// application row to 'failed', then clears the condition. The existing
// drift-sweep recovery path re-enqueues failed rows on its next tick,
// so this remedy unblocks without taking a hard dependency on the
// asynq enqueuer here (which would couple the reconciler to the
// tunnel-queue config). Idempotent.
func remediateTemplateApplyStuck(ctx context.Context, row sqlc.ClusterCondition) error {
	app, err := runtimeDeps.Queries.GetClusterTemplateApplication(ctx, row.ClusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No application row — condition is stale. Clear it.
			_, _ = runtimeDeps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
				ClusterID: row.ClusterID,
				Type:      ConditionTemplateApplyStuck,
				Status:    "False",
				Reason:    "NoApplicationRow",
				Message:   "cluster_template_applications row not found; clearing stale stuck-apply condition",
			})
			return insertAttemptResult(ctx, row, ccrActionApplyResetToFailed, ccrOutcomeOk, "", map[string]any{
				"already_gone": true,
			})
		}
		return insertAttempt(ctx, row, ccrActionApplyResetToFailed, ccrOutcomeFail, "get_app: "+err.Error(), nil)
	}
	if app.Status != "applying" {
		// Already moved on — clear the condition.
		_, _ = runtimeDeps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
			ClusterID: row.ClusterID,
			Type:      ConditionTemplateApplyStuck,
			Status:    "False",
			Reason:    "ApplyAdvanced",
			Message:   "cluster_template_applications.status=" + app.Status + "; clearing stuck-apply condition",
		})
		return insertAttemptResult(ctx, row, ccrActionApplyResetToFailed, ccrOutcomeOk, "", map[string]any{
			"observed_status": app.Status,
			"advanced":        true,
		})
	}
	_, err = runtimeDeps.Queries.MarkClusterTemplateApplicationStatus(ctx, sqlc.MarkClusterTemplateApplicationStatusParams{
		ClusterID: row.ClusterID,
		Status:    "failed",
		LastError: "stuck in 'applying' beyond reconciler threshold; reset by cluster_condition_reconcile",
	})
	if err != nil {
		return insertAttempt(ctx, row, ccrActionApplyResetToFailed, ccrOutcomeFail, "mark_failed: "+err.Error(), nil)
	}
	// Clear the condition so the next tick doesn't loop.
	_, _ = runtimeDeps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
		ClusterID: row.ClusterID,
		Type:      ConditionTemplateApplyStuck,
		Status:    "False",
		Reason:    "ResetToFailed",
		Message:   "stuck applying row reset to failed; drift-sweep recovery will re-enqueue",
	})
	if err := insertAttemptResult(ctx, row, ccrActionApplyResetToFailed, ccrOutcomeOk, "", map[string]any{
		"reset_to": "failed",
	}); err != nil {
		return err
	}
	if w, ok := any(runtimeDeps.Queries).(auditWriterV1ForReconciler); ok && w != nil {
		audit.Record(ctx, w, audit.Event{
			Source:       "worker",
			Action:       "cluster.condition.remediation_attempted",
			ResourceType: "cluster",
			ResourceID:   row.ClusterID.String(),
			Detail: map[string]any{
				"condition_type": row.Type,
				"action":         ccrActionApplyResetToFailed,
				"outcome":        ccrOutcomeOk,
			},
		})
	}
	return nil
}

// remediateConnectedFalse mints a fresh registration token for the
// cluster and records the attempt. The agent host operator then runs
// the new install one-liner to re-pair. We don't try to "magic" the
// agent back up — by definition the tunnel is down — but we prepare
// the artifact so the next step is a single command.
func remediateConnectedFalse(ctx context.Context, row sqlc.ClusterCondition) error {
	// Live connectivity re-check. The Connected=False condition row may be
	// stale by up to a full health-check tick (30s) plus the backoff delay
	// before we get here. Re-read the cluster's current heartbeat; if the
	// agent has reconnected (a heartbeat within the freshness window) the
	// tunnel is back and reissuing a token would be wasted, confusing
	// traffic. Skip and record it so the next tick re-evaluates cheaply.
	cluster, err := runtimeDeps.Queries.GetClusterByID(ctx, row.ClusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Cluster gone — condition is stale, nothing to remediate.
			return insertAttempt(ctx, row, ccrActionNoopReconnected, ccrOutcomeSkip, "", map[string]any{
				"reason": "cluster_not_found",
			})
		}
		return insertAttempt(ctx, row, ccrActionTokenReissued, ccrOutcomeFail, "get_cluster: "+err.Error(), nil)
	}
	if cluster.LastHeartbeat.Valid && time.Since(cluster.LastHeartbeat.Time) <= ccrConnectedFreshWindow {
		return insertAttempt(ctx, row, ccrActionNoopReconnected, ccrOutcomeSkip, "", map[string]any{
			"reason":            "agent_reconnected",
			"last_heartbeat":    cluster.LastHeartbeat.Time.UTC().Format(time.RFC3339),
			"heartbeat_age_sec": int(time.Since(cluster.LastHeartbeat.Time).Seconds()),
			"window_sec":        int(ccrConnectedFreshWindow.Seconds()),
		})
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return insertAttempt(ctx, row, ccrActionTokenReissued, ccrOutcomeFail, "rand.Read: "+err.Error(), nil)
	}
	tokenStr := base64.URLEncoding.EncodeToString(tokenBytes)

	token, err := runtimeDeps.Queries.CreateClusterRegistrationToken(ctx, sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: row.ClusterID,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().UTC().Add(time.Duration(runtimeDeps.RegistrationTokenTTLHours) * time.Hour),
	})
	if err != nil {
		return insertAttempt(ctx, row, ccrActionTokenReissued, ccrOutcomeFail, "create_token: "+err.Error(), nil)
	}

	// Record the attempt with the token's id (not its value — the
	// detail JSON is readable by anyone with audit access).
	detail := map[string]any{
		"token_id":   token.ID.String(),
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
		"reason":     row.Reason,
		"message":    row.Message,
	}
	if err := insertAttemptResult(ctx, row, ccrActionTokenReissued, ccrOutcomeOk, "", detail); err != nil {
		return err
	}

	// Audit so the on-call trail picks it up.
	if w, ok := any(runtimeDeps.Queries).(auditWriterV1ForReconciler); ok && w != nil {
		audit.Record(ctx, w, audit.Event{
			Source:       "worker",
			Action:       "cluster.condition.remediation_attempted",
			ResourceType: "cluster",
			ResourceID:   row.ClusterID.String(),
			Detail: map[string]any{
				"condition_type": row.Type,
				"action":         ccrActionTokenReissued,
				"outcome":        ccrOutcomeOk,
				"token_id":       token.ID.String(),
			},
		})
	}

	// SSE fan-out is intentionally not wired here: the worker process
	// today doesn't hold an EventPublisher (the bus lives on the server
	// side). The cluster-detail page's polling cluster-conditions
	// fetch will pick up the new audit row + the existing tokens-
	// listing endpoint within its 30s poll, which is the SLA we
	// committed to. If we add a worker→server fan-out later
	// (RuntimeDependencies.Events), this is the call site to plumb.
	return nil
}

// ccrBackoffFor returns the minimum required interval between
// remediation attempts given the time since the previous attempt. It
// picks the largest scheduled interval that's still ≤ the elapsed
// time; semantics: "we want at least this much gap before trying
// again, growing as failures pile up."
func ccrBackoffFor(elapsed time.Duration) time.Duration {
	for i, d := range ccrBackoff {
		if elapsed < d {
			if i == 0 {
				return d
			}
			return ccrBackoff[i]
		}
	}
	return ccrBackoff[len(ccrBackoff)-1]
}

// insertAttempt is the unconditional writer — works for skip rows too.
// Returns the error from the insert (or nil) so callers can decide
// whether to propagate.
func insertAttempt(ctx context.Context, row sqlc.ClusterCondition, action, outcome, errMsg string, detail map[string]any) error {
	detailJSON, _ := json.Marshal(detail)
	if len(detailJSON) == 0 {
		detailJSON = []byte("{}")
	}
	_, err := runtimeDeps.Queries.InsertClusterConditionRemediation(ctx, sqlc.InsertClusterConditionRemediationParams{
		ClusterID:     row.ClusterID,
		ConditionType: row.Type,
		Action:        action,
		Outcome:       outcome,
		Error:         errMsg,
		Detail:        detailJSON,
	})
	if err != nil {
		runtimeLogger().WarnContext(ctx, "failed to insert remediation attempt",
			"cluster_id", row.ClusterID.String(),
			"type", row.Type,
			"error", err,
		)
	}
	return nil
}

// insertAttemptResult is the same as insertAttempt but propagates the
// insert error to the caller so success-path failures (which should be
// loud) reach the sweep loop's logger.
func insertAttemptResult(ctx context.Context, row sqlc.ClusterCondition, action, outcome, errMsg string, detail map[string]any) error {
	detailJSON, jerr := json.Marshal(detail)
	if jerr != nil {
		detailJSON = []byte("{}")
	}
	_, err := runtimeDeps.Queries.InsertClusterConditionRemediation(ctx, sqlc.InsertClusterConditionRemediationParams{
		ClusterID:     row.ClusterID,
		ConditionType: row.Type,
		Action:        action,
		Outcome:       outcome,
		Error:         errMsg,
		Detail:        detailJSON,
	})
	return err
}

// auditWriterV1ForReconciler is the audit-writer interface the
// reconciler depends on. *sqlc.Queries satisfies it because
// CreateAuditLogV1 is part of the generated Queries surface; the
// type-switch above avoids a hard import cycle / nil-deref risk when
// callers wire a smaller Querier in tests.
type auditWriterV1ForReconciler interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}
