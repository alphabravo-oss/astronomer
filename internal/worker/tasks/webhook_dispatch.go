package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
)

// Migration 048 — webhook dispatcher + retention task types.
const (
	// WebhookDispatchType is the periodic task that drains pending
	// webhook_deliveries rows. Runs every 15s; cooperative DB lease keeps
	// multiple worker pods from racing on the same row.
	WebhookDispatchType = "webhook:dispatch"

	// WebhookCleanupOldType deletes delivered/dropped rows older than the
	// retention window (default 30 days). Daily cadence.
	WebhookCleanupOldType = "webhook:cleanup_old"
)

// webhookDispatchBatchSize is the cap on rows per tick. 50 is well above
// the realistic per-tick rate (an audit-heavy stack emits maybe 10
// events / 15s) and small enough that one slow receiver can't starve
// the next tick.
const webhookDispatchBatchSize = 50

// webhookRetention is the default delete-older-than window. Operators
// can override via platform_settings 'webhook.delivery_retention_days'.
const webhookRetention = 30 * 24 * time.Hour

// WebhookSender is the surface the dispatcher needs from the webhook
// package. *webhook.Sender satisfies it.
type WebhookSender interface {
	Send(ctx context.Context, sub webhook.Subscription, event webhook.Event) (webhook.Outcome, int, error)
}

// WebhookQuerier is the database surface the dispatch task needs.
// *sqlc.Queries satisfies it directly.
type WebhookQuerier interface {
	ListPendingWebhookDeliveries(ctx context.Context, arg sqlc.ListPendingWebhookDeliveriesParams) ([]sqlc.WebhookDelivery, error)
	GetWebhookSubscription(ctx context.Context, id uuid.UUID) (sqlc.WebhookSubscription, error)
	MarkWebhookDeliveryDelivered(ctx context.Context, arg sqlc.MarkWebhookDeliveryDeliveredParams) error
	MarkWebhookDeliveryFailed(ctx context.Context, arg sqlc.MarkWebhookDeliveryFailedParams) error
	MarkWebhookDeliveryDropped(ctx context.Context, arg sqlc.MarkWebhookDeliveryDroppedParams) error
	DeleteWebhookDeliveriesOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// WebhookDeps is the dependency bag the dispatcher reads. Wired by
// NewApp at server startup; stays in a package-level var so the
// asynq HandleFunc signature can stay standard.
type WebhookDeps struct {
	Queries   WebhookQuerier
	Sender    WebhookSender
	Encryptor *auth.Encryptor
}

var webhookDeps WebhookDeps

// ConfigureWebhook wires the dispatcher's dependencies. Safe to call
// multiple times (last call wins).
func ConfigureWebhook(deps WebhookDeps) {
	webhookDeps = deps
}

// HandleWebhookDispatch is the periodic task that drains pending
// deliveries. Pattern matches the email dispatcher:
//
//  1. Leader-elect so only one worker pod runs the loop.
//  2. Pull pending rows whose next_attempt_at <= now.
//  3. Per row: load + decrypt the subscription's HMAC secret, send,
//     map the outcome to a status transition + retry timer.
//
// We DO NOT batch-load subscriptions: the realistic concurrency is
// "1-3 subscriptions × tens of events" and per-row lookups keep the
// failure isolation simple. If a single subscription's row is broken,
// only its deliveries fail; the rest of the batch still ships.
func HandleWebhookDispatch(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, WebhookDispatchType, func() error {
		if webhookDeps.Queries == nil || webhookDeps.Sender == nil {
			runtimeLogger().InfoContext(ctx, "webhook dispatcher not configured, skipping")
			return nil
		}
		now := time.Now().UTC()
		rows, err := webhookDeps.Queries.ListPendingWebhookDeliveries(ctx, sqlc.ListPendingWebhookDeliveriesParams{
			NextAttemptAt: pgtype.Timestamptz{Time: now, Valid: true},
			Limit:         webhookDispatchBatchSize,
		})
		if err != nil {
			return fmt.Errorf("list pending webhook deliveries: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		// Per-tick subscription cache. Rows are usually clustered by
		// subscription (filters tend to overlap) so this saves
		// duplicate GETs and duplicate Fernet decrypts.
		cache := map[uuid.UUID]webhook.Subscription{}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil
			}
			sub, ok := cache[row.SubscriptionID]
			if !ok {
				loaded, err := loadSubscription(ctx, row.SubscriptionID)
				if err != nil {
					runtimeLogger().WarnContext(ctx, "webhook dispatcher: load subscription failed",
						"subscription_id", row.SubscriptionID.String(), "error", err)
					// Mark the delivery dropped with the load failure so
					// the operator can spot it on the recent-deliveries
					// view. Don't keep trying — re-loading on the next
					// tick will hit the same error.
					_ = webhookDeps.Queries.MarkWebhookDeliveryDropped(ctx, sqlc.MarkWebhookDeliveryDroppedParams{
						ID:        row.ID,
						Attempts:  row.Attempts + 1,
						LastError: truncateDispatchLastError("subscription load failed: "+err.Error(), 1024),
					})
					webhook.RecordOutcome("dropped", 0)
					continue
				}
				cache[row.SubscriptionID] = loaded
				sub = loaded
			}
			dispatchOne(ctx, row, sub)
		}
		return nil
	})
}

// dispatchOne is the per-row sender + status writeback. We capture the
// elapsed time for the metrics histogram regardless of outcome.
func dispatchOne(ctx context.Context, row sqlc.WebhookDelivery, sub webhook.Subscription) {
	event := buildEventFromRow(row)
	start := time.Now()
	outcome, _, err := webhookDeps.Sender.Send(ctx, sub, event)
	elapsed := time.Since(start).Seconds()
	now := time.Now().UTC()

	if err != nil && outcome.Err == nil {
		// Send returned a non-recoverable build error (template render,
		// oversized payload). Treat as dropped; the row will not be
		// retried because the failure is deterministic.
		webhook.RecordOutcome("dropped", elapsed)
		_ = webhookDeps.Queries.MarkWebhookDeliveryDropped(ctx, sqlc.MarkWebhookDeliveryDroppedParams{
			ID:        row.ID,
			Attempts:  row.Attempts + 1,
			LastError: truncateDispatchLastError(err.Error(), 1024),
		})
		return
	}

	if outcome.IsSuccess() {
		webhook.RecordOutcome("delivered", elapsed)
		_ = webhookDeps.Queries.MarkWebhookDeliveryDelivered(ctx, sqlc.MarkWebhookDeliveryDeliveredParams{
			ID:             row.ID,
			Attempts:       row.Attempts + 1,
			ResponseStatus: int32(outcome.Status),
			ResponseBody:   outcome.ResponseBody,
			DeliveredAt:    pgtype.Timestamptz{Time: now, Valid: true},
		})
		return
	}

	// Pull the max-retries budget off the live subscription row (cached
	// in webhook.Subscription via the loader below).
	maxRetries := 5
	if sub.MaxRetries > 0 {
		maxRetries = sub.MaxRetries
	}
	nextAttempts := row.Attempts + 1
	if !outcome.IsRetryable() || int(nextAttempts) >= maxRetries {
		// Either a 4xx (operator must fix it) or retry budget exhausted.
		webhook.RecordOutcome("dropped", elapsed)
		_ = webhookDeps.Queries.MarkWebhookDeliveryDropped(ctx, sqlc.MarkWebhookDeliveryDroppedParams{
			ID:             row.ID,
			Attempts:       nextAttempts,
			ResponseStatus: int32(outcome.Status),
			ResponseBody:   outcome.ResponseBody,
			LastError:      truncateDispatchLastError(failureReason(outcome), 1024),
		})
		return
	}

	backoff := webhook.NextBackoff(int(nextAttempts))
	webhook.RecordOutcome("failed", elapsed)
	_ = webhookDeps.Queries.MarkWebhookDeliveryFailed(ctx, sqlc.MarkWebhookDeliveryFailedParams{
		ID:             row.ID,
		Attempts:       nextAttempts,
		ResponseStatus: int32(outcome.Status),
		ResponseBody:   outcome.ResponseBody,
		LastError:      truncateDispatchLastError(failureReason(outcome), 1024),
		NextAttemptAt:  pgtype.Timestamptz{Time: now.Add(backoff), Valid: true},
	})
}

// loadSubscription pulls the row + decrypts the secret + decodes the
// JSONB extra_headers. The decrypted plaintext lives only in the
// returned struct; the dispatcher discards it after Send completes.
func loadSubscription(ctx context.Context, id uuid.UUID) (webhook.Subscription, error) {
	row, err := webhookDeps.Queries.GetWebhookSubscription(ctx, id)
	if err != nil {
		return webhook.Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	secret := ""
	if row.SecretEncrypted != "" {
		if webhookDeps.Encryptor == nil {
			return webhook.Subscription{}, fmt.Errorf("encryptor unavailable; cannot decrypt secret")
		}
		plain, err := webhookDeps.Encryptor.Decrypt(row.SecretEncrypted)
		if err != nil {
			return webhook.Subscription{}, fmt.Errorf("decrypt secret: %w", err)
		}
		secret = plain
	}
	headers := map[string]string{}
	if len(row.ExtraHeaders) > 0 {
		_ = json.Unmarshal(row.ExtraHeaders, &headers)
	}
	return webhook.Subscription{
		ID:              row.ID.String(),
		Name:            row.Name,
		URL:             row.Url,
		Secret:          secret,
		PayloadTemplate: row.PayloadTemplate,
		ExtraHeaders:    headers,
		TimeoutSeconds:  int(row.TimeoutSeconds),
		MaxRetries:      int(row.MaxRetries),
	}, nil
}

// buildEventFromRow reconstructs the Sender's Event from a persisted
// row. The tap writes the same shape into payload at enqueue time so
// rehydration is the inverse marshal.
func buildEventFromRow(row sqlc.WebhookDelivery) webhook.Event {
	var env eventPayload
	if len(row.Payload) > 0 {
		_ = json.Unmarshal(row.Payload, &env)
	}
	return webhook.Event{
		EventName:    row.EventName,
		EventID:      row.EventID,
		Timestamp:    env.Timestamp,
		Detail:       env.Detail,
		ActorUserID:  env.ActorUserID,
		ResourceID:   env.ResourceID,
		ResourceType: env.ResourceType,
		DeliveryID:   row.ID.String(),
	}
}

// eventPayload is the JSONB-on-disk shape the tap writes; mirrors the
// tap's local eventEnvelope struct + a few audit-bridge-extra fields.
type eventPayload struct {
	EventName    string          `json:"event_name"`
	EventID      string          `json:"event_id"`
	Timestamp    time.Time       `json:"timestamp"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	ActorUserID  string          `json:"actor_user_id,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
}

// failureReason picks the most operator-useful single-line summary for
// a failed delivery. Transport errors win because they're often more
// actionable than the HTTP status.
func failureReason(outcome webhook.Outcome) string {
	if outcome.Err != nil {
		return outcome.Err.Error()
	}
	return fmt.Sprintf("HTTP %d", outcome.Status)
}

// truncateDispatchLastError caps the string at n bytes so an absurdly verbose receiver
// error doesn't blow up the last_error column.
func truncateDispatchLastError(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// HandleWebhookCleanupOld deletes webhook_deliveries rows older than
// the retention window. Daily cadence; cooperative DB lease.
func HandleWebhookCleanupOld(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, WebhookCleanupOldType, func() error {
		if webhookDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "webhook cleanup not configured, skipping")
			return nil
		}
		cutoff := time.Now().UTC().Add(-webhookRetention)
		removed, err := webhookDeps.Queries.DeleteWebhookDeliveriesOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete old webhook deliveries: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "webhook retention sweep",
			"deliveries_deleted", removed,
			"cutoff", cutoff.Format(time.RFC3339),
		)
		return nil
	})
}
