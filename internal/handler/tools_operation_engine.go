package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *ToolHandler) afterToolOperationCommit(op sqlc.ToolOperation) {
	if h == nil || op.ID == uuid.Nil {
		return
	}
	h.publishToolOperationChanged(op)
	h.TriggerReconcile()
}

type toolOperationCreator interface {
	CreateToolOperation(context.Context, sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error)
}

type idempotentToolOperationCreator interface {
	CreateToolOperationIdempotent(context.Context, sqlc.CreateToolOperationIdempotentParams) (sqlc.ToolOperation, error)
}

var errToolOperationIdempotencyConflict = errors.New("tool operation idempotency key identifies a different operation")

func respondToolMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errToolOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different tool operation")
		return
	}
	respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, fallbackCode, fallbackMessage)
}

func (h *ToolHandler) createAuditedToolOperation(r *http.Request, targetType, targetKey, operationType string, env toolOperationEnvelope, userID pgtype.UUID, event mutationAuditEvent) (sqlc.ToolOperation, error) {
	opContext := withOperationIdempotency(r, "tools")
	op, err := executeMutation(r, h.runTx,
		func(q ToolMutationTx) (sqlc.ToolOperation, error) {
			return createToolOperation(opContext, q, targetType, targetKey, operationType, env, userID)
		},
		func(op sqlc.ToolOperation) mutationAuditEvent {
			detail := make(map[string]any, len(event.detail)+1)
			for key, value := range event.detail {
				detail[key] = value
			}
			detail["operation_id"] = op.ID.String()
			event.detail = detail
			return event
		})
	if err == nil {
		h.afterToolOperationCommit(op)
	}
	return op, err
}

func createToolOperation(ctx context.Context, q toolOperationCreator, targetType, targetKey, operationType string, env toolOperationEnvelope, userID pgtype.UUID) (sqlc.ToolOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.ToolOperation{}, err
	}
	params := sqlc.CreateToolOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.ToolOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(idempotentToolOperationCreator); ok {
			op, err = creator.CreateToolOperationIdempotent(ctx, sqlc.CreateToolOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !toolOperationIntentEqual(op.Payload, params.Payload)) {
				return sqlc.ToolOperation{}, errToolOperationIdempotencyConflict
			}
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = q.CreateToolOperation(ctx, params)
	}
	return op, err
}

func toolOperationResponse(op sqlc.ToolOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toolOperationEventsResponse(events []sqlc.ToolOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *ToolHandler) operationPreview(ctx context.Context, op sqlc.ToolOperation) map[string]any {
	resp := toolOperationResponse(op)
	if events, err := h.queries.ListToolOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = toolOperationEventsResponse(lastToolEvents(events, 3))
	}
	return resp
}

func lastToolEvents(events []sqlc.ToolOperationEvent, n int) []sqlc.ToolOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *ToolHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, then release before
	// helm dispatch so unrelated clusters' operations are not stalled
	// behind a stuck install (helmTimeout = 10 minutes).
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingToolOperations(ctx))
}

// claimPendingToolOperations holds h.mu just long enough to supersede
// stale rows and mark this tick's claims "running" in the DB. Returned
// rows are wrapped as claimedOps so dispatchClaimed can run them
// outside the lock via per-row Run/OnComplete/OnFailure closures.
func (h *ToolHandler) claimPendingToolOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingToolOperations(ctx, int32(effectiveHelmConcurrency(h.helmConcurrency)))
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.ToolOperation]{
		ShouldSupersede: func(op sqlc.ToolOperation, _ time.Time) bool { return op.Status != OpStatusRunning },
		ID:              func(op sqlc.ToolOperation) uuid.UUID { return op.ID },
		TargetKey:       func(op sqlc.ToolOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:          func(op sqlc.ToolOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.ToolOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.ToolOperation) {
			h.recordToolOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			if superseded, serr := h.queries.MarkToolOperationSuperseded(ctx, sqlc.MarkToolOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			}); serr == nil {
				h.publishToolOperationChanged(superseded)
			}
		},
		MarkRunning: func(ctx context.Context, op sqlc.ToolOperation) (sqlc.ToolOperation, error) {
			running, err := h.queries.MarkToolOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.ToolOperation{}, err
			}
			h.publishToolOperationChanged(running)
			h.recordToolOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.ToolOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.finishToolOperation(ctx, running, nil)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.finishToolOperation(ctx, running, err)
					if h.log != nil {
						h.log.Warn("tool operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
}

// helmReleaseReady reports whether a Helm release status payload
// describes a release that is actually ready (not merely "the helm
// install command returned"). The agent runs install/upgrade with
// Wait=true, so helm only reports "deployed" once the release's
// workloads have become Ready; a release that is still rolling out,
// failed, or pending sits in another phase. We treat "deployed" as
// ready and everything else as not-ready.
func helmReleaseReady(status *protocol.HelmResultPayload) bool {
	return status != nil && status.Success && status.Status == "deployed"
}

// toolReadinessProbes is how many times we re-probe a not-yet-ready release
// before failing the operation (DIR-11). Each probe after the first waits
// toolReadinessProbeDelay so a brief "pending-install" window can clear.
// Accessed via getters so tests can override without data races.
var (
	toolReadinessProbes     = 3
	toolReadinessProbeDelay = 2 * time.Second
	toolReadinessMu         sync.RWMutex
)

func readinessProbeConfig() (probes int, delay time.Duration) {
	toolReadinessMu.RLock()
	defer toolReadinessMu.RUnlock()
	return toolReadinessProbes, toolReadinessProbeDelay
}

func setReadinessProbeConfig(probes int, delay time.Duration) (restore func()) {
	toolReadinessMu.Lock()
	oldP, oldD := toolReadinessProbes, toolReadinessProbeDelay
	toolReadinessProbes, toolReadinessProbeDelay = probes, delay
	toolReadinessMu.Unlock()
	return func() {
		toolReadinessMu.Lock()
		toolReadinessProbes, toolReadinessProbeDelay = oldP, oldD
		toolReadinessMu.Unlock()
	}
}

// checkToolReleaseReady probes the live Helm release status after an
// install/upgrade. DIR-11: sustained not-ready (or Status RPC failure after
// retries) fails the tool operation instead of warn-and-succeed.
func (h *ToolHandler) checkToolReleaseReady(ctx context.Context, op sqlc.ToolOperation, env toolReleaseExecution) error {
	if h.helm == nil {
		return nil
	}
	probes, delay := readinessProbeConfig()
	var lastStatus *protocol.HelmResultPayload
	var lastErr error
	for attempt := 1; attempt <= probes; attempt++ {
		status, err := h.helm.Status(ctx, env.ClusterID, env.ReleaseName, env.Namespace)
		if err != nil {
			lastErr = err
			h.recordToolOperationEvent(ctx, op.ID, "warn", "readiness", "failed to query Helm release status for readiness", map[string]any{
				"releaseName": env.ReleaseName,
				"namespace":   env.Namespace,
				"error":       err.Error(),
				"attempt":     attempt,
			})
		} else if helmReleaseReady(status) {
			h.recordToolOperationEvent(ctx, op.ID, "info", "readiness", "Helm release Ready", map[string]any{
				"releaseName": env.ReleaseName,
				"namespace":   env.Namespace,
				"status":      status.Status,
				"revision":    status.Revision,
				"attempt":     attempt,
			})
			return nil
		} else {
			lastStatus = status
			lastErr = nil
			h.recordToolOperationEvent(ctx, op.ID, "warn", "readiness", "Helm release not ready after operation", map[string]any{
				"releaseName": env.ReleaseName,
				"namespace":   env.Namespace,
				"status":      status.Status,
				"revision":    status.Revision,
				"attempt":     attempt,
			})
		}
		if attempt < probes {
			select {
			case <-ctx.Done():
				return fmt.Errorf("readiness check canceled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("readiness check failed after %d probes: %w", probes, lastErr)
	}
	st := "unknown"
	if lastStatus != nil {
		st = lastStatus.Status
	}
	return fmt.Errorf("Helm release %q not Ready after %d probes (status=%s)", env.ReleaseName, probes, st)
}

func existingHelmReleaseStatus(ctx context.Context, helm HelmRequester, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, bool, error) {
	if helm == nil {
		return nil, false, errors.New("helm requester not configured")
	}
	status, err := helm.Status(ctx, clusterID, releaseName, namespace)
	if err == nil {
		return status, true, nil
	}
	if isHelmReleaseNotFound(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func adoptExistingToolRelease(ctx context.Context, queries toolInstallPersister, clusterID uuid.UUID, env toolReleaseExecution, status *protocol.HelmResultPayload) error {
	if queries == nil {
		return errors.New("tool queries not configured")
	}
	if status == nil {
		return errors.New("helm status not provided")
	}
	preset := pgtype.Text{String: env.Preset, Valid: env.Preset != ""}
	toolSlug := pgtype.Text{String: env.ToolSlug, Valid: env.ToolSlug != ""}
	params := sqlc.GetInstalledChartByReleaseParams{
		ClusterID:   clusterID,
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
	}
	if _, err := queries.GetInstalledChartByRelease(ctx, params); err == nil {
		_, err = queries.AdoptInstalledChartByRelease(ctx, sqlc.AdoptInstalledChartByReleaseParams{
			ClusterID:      clusterID,
			ReleaseName:    env.ReleaseName,
			Namespace:      env.Namespace,
			ToolSlug:       toolSlug,
			PresetUsed:     preset,
			ValuesOverride: env.ValuesYAML,
			Status:         normalizeToolStatus(status.Status),
			Revision:       int32(status.Revision),
		})
		return err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err := queries.CreateInstalledChart(ctx, sqlc.CreateInstalledChartParams{
		ClusterID:      clusterID,
		ReleaseName:    env.ReleaseName,
		Namespace:      env.Namespace,
		ValuesOverride: env.ValuesYAML,
		Status:         normalizeToolStatus(status.Status),
		Revision:       int32(status.Revision),
		ToolSlug:       toolSlug,
		PresetUsed:     preset,
	})
	return err
}

func isHelmReleaseNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "release: not found") || strings.Contains(msg, "release not found")
}

func operationTargetKey(clusterID uuid.UUID, slug string) string {
	return clusterID.String() + ":" + slug
}

func (h *ToolHandler) recordToolOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateToolOperationEvent(ctx, sqlc.CreateToolOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}
