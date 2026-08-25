package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type SIEMTestOperation struct {
	ID               uuid.UUID `json:"id"`
	ForwarderID      uuid.UUID `json:"forwarder_id"`
	QueueID          int64     `json:"queue_id"`
	IdempotencyScope string    `json:"-"`
	IdempotencyKey   string    `json:"-"`
	RequestDigest    string    `json:"-"`
	Status           string    `json:"status"`
	ErrorCode        string    `json:"error_code,omitempty"`
	RequestedBy      uuid.UUID `json:"requested_by"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Created          bool      `json:"-"`
}

type CreateSIEMTestOperationAndQueueParams struct {
	ID               uuid.UUID
	ForwarderID      uuid.UUID
	IdempotencyScope string
	IdempotencyKey   string
	RequestDigest    string
	RequestedBy      uuid.UUID
	Payload          json.RawMessage
}

const createSIEMTestOperationAndQueue = `
WITH operation AS (
    INSERT INTO siem_test_operations (
        id, forwarder_id, idempotency_scope, idempotency_key,
        request_digest, requested_by
    ) VALUES ($1, $2, $3, $4, $5, $6)
    ON CONFLICT (idempotency_scope, idempotency_key) DO UPDATE
    SET idempotency_key = siem_test_operations.idempotency_key
    WHERE siem_test_operations.id = EXCLUDED.id
      AND siem_test_operations.forwarder_id = EXCLUDED.forwarder_id
      AND siem_test_operations.request_digest = EXCLUDED.request_digest
    RETURNING siem_test_operations.*, (xmax = 0) AS created
), queued AS (
    INSERT INTO siem_forward_queue (
        forwarder_id, event_name, payload, severity, dedupe_key
    )
    SELECT forwarder_id, 'siem.test_ping', $7, 'info', 'siem-test:' || id::text
    FROM operation
    ON CONFLICT (forwarder_id, dedupe_key) WHERE dedupe_key IS NOT NULL
    DO UPDATE SET dedupe_key = EXCLUDED.dedupe_key
    RETURNING id
), attached AS (
    UPDATE siem_test_operations target
    SET queue_id = COALESCE(target.queue_id, queued.id)
    FROM operation, queued
    WHERE target.id = operation.id
    RETURNING target.id, target.forwarder_id, target.queue_id,
              target.idempotency_scope, target.idempotency_key,
              target.request_digest, target.status, target.error_code,
              target.requested_by, target.completed_at, target.created_at,
              target.updated_at, operation.created
)
SELECT * FROM attached
`

func (q *Queries) CreateSIEMTestOperationAndQueue(ctx context.Context, arg CreateSIEMTestOperationAndQueueParams) (SIEMTestOperation, error) {
	row := q.db.QueryRow(ctx, createSIEMTestOperationAndQueue, arg.ID, arg.ForwarderID,
		arg.IdempotencyScope, arg.IdempotencyKey, arg.RequestDigest, arg.RequestedBy, arg.Payload)
	return scanSIEMTestOperation(row)
}

type GetSIEMTestOperationParams struct {
	ID          uuid.UUID
	ForwarderID uuid.UUID
}

func (q *Queries) GetSIEMTestOperation(ctx context.Context, arg GetSIEMTestOperationParams) (SIEMTestOperation, error) {
	row := q.db.QueryRow(ctx, `
SELECT id, forwarder_id, COALESCE(queue_id, 0), idempotency_scope,
       idempotency_key, request_digest, status, error_code, requested_by,
       completed_at, created_at, updated_at, false
FROM siem_test_operations WHERE id = $1 AND forwarder_id = $2`, arg.ID, arg.ForwarderID)
	return scanSIEMTestOperation(row)
}

func (q *Queries) MarkSIEMTestOperationsSucceededByQueueIDs(ctx context.Context, ids []int64) error {
	_, err := q.db.Exec(ctx, `
UPDATE siem_test_operations
SET status = 'succeeded', error_code = '', completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE queue_id = ANY($1::bigint[]) AND status = 'pending'`, ids)
	return err
}

func (q *Queries) MarkSIEMTestOperationsFailedByQueueIDs(ctx context.Context, ids []int64, code string) error {
	_, err := q.db.Exec(ctx, `
UPDATE siem_test_operations
SET status = 'failed', error_code = $2, completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE queue_id = ANY($1::bigint[]) AND status = 'pending'`, ids, code)
	return err
}

type siemTestOperationRow interface{ Scan(...any) error }

func scanSIEMTestOperation(row siemTestOperationRow) (SIEMTestOperation, error) {
	var item SIEMTestOperation
	var completedAt *time.Time
	err := row.Scan(&item.ID, &item.ForwarderID, &item.QueueID, &item.IdempotencyScope,
		&item.IdempotencyKey, &item.RequestDigest, &item.Status, &item.ErrorCode,
		&item.RequestedBy, &completedAt, &item.CreatedAt, &item.UpdatedAt, &item.Created)
	if completedAt != nil {
		item.CompletedAt = completedAt.UTC()
	}
	return item, err
}
