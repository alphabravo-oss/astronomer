package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	// DefaultOutboxMaxAttempts is intentionally larger than the ordinary task
	// retry budget. Audit evidence is compliance state, not disposable
	// telemetry; a terminal row remains inspectable and alertable after the
	// bounded automatic attempts are exhausted.
	DefaultOutboxMaxAttempts int32 = 20
	maxOutboxDedupeKeyBytes        = 512
	maxOutboxDetailBytes           = 64 * 1024
)

var (
	ErrOutboxUnavailable = errors.New("transactional audit outbox is unavailable")
	ErrInvalidOutbox     = errors.New("invalid transactional audit intent")
)

// OutboxQuerier is deliberately satisfied by both *sqlc.Queries and a
// transaction-bound sqlc.New(tx). Domain services must pass the latter when
// recording an intent alongside a mutation.
type OutboxQuerier interface {
	UpsertAuditOutbox(context.Context, sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error)
}

// OutboxOptions controls the stable identity of an audit intent. Zero values
// receive production-safe defaults and exist primarily as deterministic test
// seams.
type OutboxOptions struct {
	ID          uuid.UUID
	CreatedAt   time.Time
	MaxAttempts int32
}

// Intent is a transport-neutral audit envelope that domain services can
// persist inside their own database transaction. HTTP handlers construct it;
// controllers never need access to the request object or middleware context.
type Intent struct {
	Event     Event
	DedupeKey string
}

func (intent Intent) IsZero() bool {
	return strings.TrimSpace(intent.Event.Action) == "" || strings.TrimSpace(intent.DedupeKey) == ""
}

func RecordIntent(ctx context.Context, q OutboxQuerier, intent Intent) error {
	if intent.IsZero() {
		return ErrOutboxUnavailable
	}
	_, err := RecordOutbox(ctx, q, intent.Event, intent.DedupeKey, OutboxOptions{})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOutboxUnavailable, err)
	}
	return nil
}

// MutationDedupeKey returns a content-free stable key. Hashing prevents a
// caller from accidentally placing a path, object name, or ticket value in a
// unique index while still making an idempotent request retry converge on the
// original intent.
func MutationDedupeKey(requestID, action, resourceType, resourceID string) string {
	canonical := strings.Join([]string{
		strings.TrimSpace(requestID),
		strings.TrimSpace(action),
		strings.TrimSpace(resourceType),
		strings.TrimSpace(resourceID),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return "audit:v1:" + hex.EncodeToString(sum[:])
}

// RecordOutbox persists a sanitized audit-v1 envelope through q. When q is a
// transaction-bound *sqlc.Queries, the domain mutation and this row commit or
// roll back together. It intentionally does not publish to the event bus: the
// dispatcher publishes only after the row has entered audit_log.
func RecordOutbox(ctx context.Context, q OutboxQuerier, event Event, dedupeKey string, opts OutboxOptions) (sqlc.AuditOutbox, error) {
	if q == nil {
		return sqlc.AuditOutbox{}, ErrOutboxUnavailable
	}
	dedupeKey = strings.TrimSpace(dedupeKey)
	if dedupeKey == "" || len(dedupeKey) > maxOutboxDedupeKeyBytes {
		return sqlc.AuditOutbox{}, fmt.Errorf("%w: dedupe key must contain 1-%d bytes", ErrInvalidOutbox, maxOutboxDedupeKeyBytes)
	}
	row, ok := buildRow(event)
	if !ok || strings.TrimSpace(row.Action) == "" || strings.TrimSpace(row.ResourceType) == "" {
		return sqlc.AuditOutbox{}, fmt.Errorf("%w: action and resource type are required", ErrInvalidOutbox)
	}
	if len(row.Detail) > maxOutboxDetailBytes {
		return sqlc.AuditOutbox{}, fmt.Errorf("%w: sanitized detail exceeds %d bytes", ErrInvalidOutbox, maxOutboxDetailBytes)
	}
	if opts.ID == uuid.Nil {
		opts.ID = uuid.New()
	}
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = time.Now().UTC()
	} else {
		opts.CreatedAt = opts.CreatedAt.UTC()
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = DefaultOutboxMaxAttempts
	}
	return q.UpsertAuditOutbox(ctx, sqlc.UpsertAuditOutboxParams{
		ID: opts.ID, DedupeKey: dedupeKey, EventCreatedAt: opts.CreatedAt,
		SchemaVersion: "audit-v1", UserID: row.UserID,
		ActorAuthMethod: row.ActorAuthMethod, Action: row.Action,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		ResourceName: row.ResourceName, HttpMethod: row.HTTPMethod,
		Path: row.Path, StatusCode: row.StatusCode, DurationMs: row.DurationMs,
		RequestID: row.RequestID, IpAddress: row.IpAddress,
		UserAgent: row.UserAgent, Detail: row.Detail, Source: row.Source,
		CorrelationID: row.CorrelationID, ActionClass: row.ActionClass,
		MaxAttempts: opts.MaxAttempts,
	})
}

// PublishOutboxDelivery emits the structured log and local event only after
// DeliverAuditOutbox has atomically inserted audit_log and acknowledged the
// outbox row. The stable UUID lets downstream queues deduplicate a replay.
func PublishOutboxDelivery(row sqlc.DeliverAuditOutboxRow) {
	detail := map[string]any{}
	if len(row.Detail) > 0 {
		_ = json.Unmarshal(row.Detail, &detail)
	}
	event := Event{
		Source: row.Source, CorrelationID: row.CorrelationID, UserID: row.UserID,
		ActorAuthMethod: row.ActorAuthMethod, Action: row.Action,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		ResourceName: row.ResourceName, HTTPMethod: row.HttpMethod,
		Path: row.Path, StatusCode: row.StatusCode, DurationMs: row.DurationMs,
		RequestID: row.RequestID, IPAddress: row.IpAddress,
		UserAgent: row.UserAgent, Detail: detail, ActionClass: row.ActionClass,
	}
	auditRow := sqlc.CreateAuditLogV1Params{
		Source: row.Source, CorrelationID: row.CorrelationID, UserID: row.UserID,
		ActorAuthMethod: row.ActorAuthMethod, Action: row.Action,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		ResourceName: row.ResourceName, HTTPMethod: row.HttpMethod,
		Path: row.Path, StatusCode: row.StatusCode, DurationMs: row.DurationMs,
		RequestID: row.RequestID, IpAddress: row.IpAddress,
		UserAgent: row.UserAgent, Detail: row.Detail, ActionClass: row.ActionClass,
	}
	emitRecordedLog(auditRow)
	publishToBusWithIdentity(event, auditRow, row.ID, row.EventCreatedAt)
}
