package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

const operationIdempotencyKeySelectColumns = `
    scope, idempotency_key, operation_table, operation_id, response, created_at, updated_at`

func scanOperationIdempotencyKey(row interface {
	Scan(dest ...any) error
}) (OperationIdempotencyKey, error) {
	var i OperationIdempotencyKey
	err := row.Scan(
		&i.Scope,
		&i.IdempotencyKey,
		&i.OperationTable,
		&i.OperationID,
		&i.Response,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

type ReserveOperationIdempotencyKeyParams struct {
	Scope          string `json:"scope"`
	IdempotencyKey string `json:"idempotency_key"`
}

const reserveOperationIdempotencyKey = `-- name: ReserveOperationIdempotencyKey :one
INSERT INTO operation_idempotency_keys (scope, idempotency_key)
VALUES ($1, $2)
ON CONFLICT (scope, idempotency_key) DO UPDATE
SET updated_at = operation_idempotency_keys.updated_at
RETURNING ` + operationIdempotencyKeySelectColumns

func (q *Queries) ReserveOperationIdempotencyKey(ctx context.Context, arg ReserveOperationIdempotencyKeyParams) (OperationIdempotencyKey, error) {
	return scanOperationIdempotencyKey(q.db.QueryRow(ctx, reserveOperationIdempotencyKey, arg.Scope, arg.IdempotencyKey))
}

type AttachOperationIdempotencyKeyParams struct {
	Scope          string          `json:"scope"`
	IdempotencyKey string          `json:"idempotency_key"`
	OperationTable string          `json:"operation_table"`
	OperationID    uuid.UUID       `json:"operation_id"`
	Response       json.RawMessage `json:"response"`
}

const attachOperationIdempotencyKey = `-- name: AttachOperationIdempotencyKey :one
UPDATE operation_idempotency_keys
SET operation_table = $3,
    operation_id = $4,
    response = COALESCE($5::jsonb, '{}'::jsonb),
    updated_at = now()
WHERE scope = $1
  AND idempotency_key = $2
  AND (operation_id IS NULL OR operation_id = $4)
RETURNING ` + operationIdempotencyKeySelectColumns

func (q *Queries) AttachOperationIdempotencyKey(ctx context.Context, arg AttachOperationIdempotencyKeyParams) (OperationIdempotencyKey, error) {
	return scanOperationIdempotencyKey(q.db.QueryRow(ctx, attachOperationIdempotencyKey,
		arg.Scope,
		arg.IdempotencyKey,
		arg.OperationTable,
		arg.OperationID,
		arg.Response,
	))
}

// attachOperationIdempotencyResponse is deliberately a second statement.
// PostgreSQL data-modifying CTEs share one snapshot, so an UPDATE CTE cannot
// reliably see the operation-idempotency row inserted by a sibling CTE on the
// first request. When Queries is transaction-bound this statement remains in
// the caller's transaction; otherwise the operation pointer from the first
// statement still makes a retry safe if this projection update fails.
func (q *Queries) attachOperationIdempotencyResponse(ctx context.Context, scope, key, table string, operationID uuid.UUID, response any) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = q.AttachOperationIdempotencyKey(ctx, AttachOperationIdempotencyKeyParams{
		Scope: scope, IdempotencyKey: key, OperationTable: table,
		OperationID: operationID, Response: raw,
	})
	return err
}
