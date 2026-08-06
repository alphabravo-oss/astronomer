package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
)

type CharlieAlertDispatchQuerier interface {
	ClaimCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error)
	GetCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error)
	CharlieAlertDeliveryAllowed(context.Context, uuid.UUID) (bool, error)
	GetCharlieFinding(context.Context, uuid.UUID) (sqlc.CharlieFinding, error)
	GetNotificationChannelByID(context.Context, uuid.UUID) (sqlc.NotificationChannel, error)
	MarkCharlieAlertDeliveryDelivered(context.Context, uuid.UUID) error
	MarkCharlieAlertDeliveryRetry(context.Context, sqlc.MarkCharlieAlertDeliveryRetryParams) error
	SuppressCharlieAlertDelivery(context.Context, sqlc.SuppressCharlieAlertDeliveryParams) error
}

const CharlieAlertDispatchTaskType = "charlie:alert_dispatch"
const CharlieAlertReconcileType = "charlie:alert_reconcile"

type CharlieAlertReconciler interface {
	Reconcile(context.Context) error
}

var (
	charlieAlertMu         sync.RWMutex
	charlieAlertQueries    CharlieAlertDispatchQuerier
	charlieAlertWriteFence *charlie.WriteFence
	charlieAlertReconciler CharlieAlertReconciler
)

func ConfigureCharlieAlertDispatch(queries CharlieAlertDispatchQuerier, writeFence *charlie.WriteFence) {
	charlieAlertMu.Lock()
	defer charlieAlertMu.Unlock()
	charlieAlertQueries = queries
	charlieAlertWriteFence = writeFence
}

func ConfigureCharlieAlertReconciler(reconciler CharlieAlertReconciler) {
	charlieAlertMu.Lock()
	defer charlieAlertMu.Unlock()
	charlieAlertReconciler = reconciler
}

func HandleCharlieAlertReconcile(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CharlieAlertReconcileType, func() error {
		charlieAlertMu.RLock()
		reconciler := charlieAlertReconciler
		charlieAlertMu.RUnlock()
		if reconciler == nil {
			return nil
		}
		if err := reconciler.Reconcile(ctx); err != nil {
			return codedCharlieAlertError("reconcile_unavailable")
		}
		return nil
	})
}

// HandleCharlieAlertDispatch owns notification delivery only. It rechecks the
// durable finding and local channel, and has no approval, capability, or action
// dispatcher dependency.
func HandleCharlieAlertDispatch(ctx context.Context, task *asynq.Task) error {
	charlieAlertMu.RLock()
	queries := charlieAlertQueries
	writeFence := charlieAlertWriteFence
	charlieAlertMu.RUnlock()
	if queries == nil || writeFence == nil {
		return codedCharlieAlertError("dispatcher_unavailable")
	}
	var payload struct {
		DeliveryID string `json:"delivery_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return codedCharlieAlertError("task_invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return codedCharlieAlertError("task_invalid")
	}
	id, err := uuid.Parse(payload.DeliveryID)
	if err != nil {
		return codedCharlieAlertError("task_invalid")
	}
	// Alert delivery is a Charlie-initiated product write even though it cannot
	// execute a capability. Register it with the same local/distributed fence as
	// Product MCP before claiming durable work. Feature disable, emergency stop,
	// downward mode changes, replacement, and uninstall therefore drain a
	// provider call already in flight and reject a newly queued local delivery.
	operationCtx, releaseWrite, err := writeFence.Begin(ctx)
	if err != nil {
		return codedCharlieAlertError("write_admission_closed")
	}
	defer releaseWrite()

	delivery, err := queries.ClaimCharlieAlertDelivery(operationCtx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			current, currentErr := queries.GetCharlieAlertDelivery(operationCtx, id)
			if currentErr == pgx.ErrNoRows {
				return nil
			}
			if currentErr != nil {
				return codedCharlieAlertError("state_unavailable")
			}
			if current.Status == "delivering" {
				return codedCharlieAlertError("delivery_leased")
			}
			return nil
		}
		return codedCharlieAlertError("claim_unavailable")
	}
	finding, err := queries.GetCharlieFinding(operationCtx, delivery.FindingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return suppressCharlieAlert(operationCtx, queries, id, "finding_inactive")
		}
		return markCharlieAlertFailure(operationCtx, queries, delivery, "finding_unavailable")
	}
	if finding.ConnectionID != delivery.ConnectionID || (finding.Status != "open" && finding.Status != "acknowledged") || (delivery.DeliveryKind == "escalation" && finding.Status != "open") {
		return suppressCharlieAlert(operationCtx, queries, id, "finding_inactive")
	}
	if !delivery.NotificationChannelID.Valid {
		return suppressCharlieAlert(operationCtx, queries, id, "channel_removed")
	}
	channel, err := queries.GetNotificationChannelByID(operationCtx, uuid.UUID(delivery.NotificationChannelID.Bytes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return suppressCharlieAlert(operationCtx, queries, id, "channel_unavailable")
		}
		return markCharlieAlertFailure(operationCtx, queries, delivery, "channel_state_unavailable")
	}
	if !channel.Enabled {
		return suppressCharlieAlert(operationCtx, queries, id, "channel_unavailable")
	}
	recipients := NotificationRecipients(channel)
	if len(recipients) == 0 {
		return markCharlieAlertFailure(operationCtx, queries, delivery, "recipient_unavailable")
	}
	notification, err := NewNotificationSendTask(NotificationSendPayload{
		Channel: channel.ChannelType, Subject: delivery.Subject, Body: delivery.Body,
		Recipients: recipients, Severity: delivery.Severity, RuleID: delivery.FindingID.String(),
		FiredAt: delivery.CreatedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return markCharlieAlertFailure(operationCtx, queries, delivery, "payload_invalid")
	}
	allowed, err := queries.CharlieAlertDeliveryAllowed(operationCtx, id)
	if err != nil {
		return markCharlieAlertFailure(operationCtx, queries, delivery, "policy_unavailable")
	}
	if !allowed {
		return suppressCharlieAlert(operationCtx, queries, id, "policy_changed")
	}
	if err := HandleNotificationSend(operationCtx, notification); err != nil {
		return markCharlieAlertFailure(operationCtx, queries, delivery, "delivery_failed")
	}
	if err := queries.MarkCharlieAlertDeliveryDelivered(operationCtx, id); err != nil {
		return codedCharlieAlertError("state_unavailable")
	}
	return nil
}

func suppressCharlieAlert(ctx context.Context, queries CharlieAlertDispatchQuerier, id uuid.UUID, code string) error {
	if err := queries.SuppressCharlieAlertDelivery(ctx, sqlc.SuppressCharlieAlertDeliveryParams{ID: id, LastErrorCode: code}); err != nil {
		return codedCharlieAlertError("state_unavailable")
	}
	return nil
}

func markCharlieAlertFailure(ctx context.Context, queries CharlieAlertDispatchQuerier, delivery sqlc.CharlieAlertDelivery, code string) error {
	if err := queries.MarkCharlieAlertDeliveryRetry(ctx, sqlc.MarkCharlieAlertDeliveryRetryParams{ID: delivery.ID, NextAttemptAt: time.Now().UTC(), LastErrorCode: code}); err != nil {
		return codedCharlieAlertError("state_unavailable")
	}
	if delivery.AttemptCount >= delivery.MaximumAttempts {
		return nil
	}
	return codedCharlieAlertError(code)
}

type charlieAlertCodedError string

func (e charlieAlertCodedError) Error() string { return "Charlie alert error: " + string(e) }
func codedCharlieAlertError(code string) error { return charlieAlertCodedError(code) }
