package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const cloudCredentialMaterializationColumns = `
    id, credential_id, cluster_id, namespace, secret_name, status,
    last_applied_at, last_error, created_at, updated_at`

func scanCloudCredentialMaterializationRow(row interface {
	Scan(dest ...any) error
}) (CloudCredentialMaterialization, error) {
	var i CloudCredentialMaterialization
	err := row.Scan(
		&i.ID,
		&i.CredentialID,
		&i.ClusterID,
		&i.Namespace,
		&i.SecretName,
		&i.Status,
		&i.LastAppliedAt,
		&i.LastError,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const upsertCloudCredentialMaterializationWithTaskOutbox = `
WITH materialization AS (
    INSERT INTO cloud_credential_materializations (
        credential_id, cluster_id, namespace, secret_name, status
    ) VALUES ($1, $2, $3, $4, 'pending')
    ON CONFLICT (credential_id, cluster_id, namespace) DO UPDATE SET
        secret_name = EXCLUDED.secret_name,
        status      = CASE
            WHEN cloud_credential_materializations.secret_name = EXCLUDED.secret_name
                THEN cloud_credential_materializations.status
            ELSE 'pending'
        END,
        updated_at  = now()
    RETURNING ` + cloudCredentialMaterializationColumns + `
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    ) VALUES (
        $5, $6, $7, $8, $9, $10, $11, $12, $13
    )
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE
    SET task_type             = EXCLUDED.task_type,
        payload               = EXCLUDED.payload,
        queue_name            = EXCLUDED.queue_name,
        max_retry             = EXCLUDED.max_retry,
        timeout_seconds       = EXCLUDED.timeout_seconds,
        unique_seconds        = EXCLUDED.unique_seconds,
        max_delivery_attempts = EXCLUDED.max_delivery_attempts,
        status                = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        next_attempt_at       = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until          = NULL,
        last_error            = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at            = now()
    RETURNING id
)
SELECT materialization.` + cloudCredentialMaterializationColumns + `
FROM materialization
CROSS JOIN outbox
`

type UpsertCloudCredentialMaterializationWithTaskOutboxParams struct {
	CredentialID        uuid.UUID          `json:"credential_id"`
	ClusterID           uuid.UUID          `json:"cluster_id"`
	Namespace           string             `json:"namespace"`
	SecretName          string             `json:"secret_name"`
	DedupeKey           pgtype.Text        `json:"dedupe_key"`
	TaskType            string             `json:"task_type"`
	Payload             []byte             `json:"payload"`
	QueueName           string             `json:"queue_name"`
	MaxRetry            int32              `json:"max_retry"`
	TimeoutSeconds      int32              `json:"timeout_seconds"`
	UniqueSeconds       int32              `json:"unique_seconds"`
	MaxDeliveryAttempts int32              `json:"max_delivery_attempts"`
	NextAttemptAt       pgtype.Timestamptz `json:"next_attempt_at"`
}

func (q *Queries) UpsertCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg UpsertCloudCredentialMaterializationWithTaskOutboxParams) (CloudCredentialMaterialization, error) {
	row := q.db.QueryRow(ctx, upsertCloudCredentialMaterializationWithTaskOutbox,
		arg.CredentialID,
		arg.ClusterID,
		arg.Namespace,
		arg.SecretName,
		arg.DedupeKey,
		arg.TaskType,
		arg.Payload,
		arg.QueueName,
		arg.MaxRetry,
		arg.TimeoutSeconds,
		arg.UniqueSeconds,
		arg.MaxDeliveryAttempts,
		arg.NextAttemptAt,
	)
	return scanCloudCredentialMaterializationRow(row)
}

const deleteCloudCredentialMaterializationWithTaskOutbox = `
WITH deleted AS (
    DELETE FROM cloud_credential_materializations
    WHERE credential_id = $1 AND cluster_id = $2 AND namespace = $3
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    ) VALUES (
        $4, $5, $6, $7, $8, $9, $10, $11, $12
    )
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE
    SET task_type             = EXCLUDED.task_type,
        payload               = EXCLUDED.payload,
        queue_name            = EXCLUDED.queue_name,
        max_retry             = EXCLUDED.max_retry,
        timeout_seconds       = EXCLUDED.timeout_seconds,
        unique_seconds        = EXCLUDED.unique_seconds,
        max_delivery_attempts = EXCLUDED.max_delivery_attempts,
        status                = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        next_attempt_at       = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until          = NULL,
        last_error            = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at            = now()
    RETURNING id
)
SELECT 1 FROM outbox
`

type DeleteCloudCredentialMaterializationWithTaskOutboxParams struct {
	CredentialID        uuid.UUID          `json:"credential_id"`
	ClusterID           uuid.UUID          `json:"cluster_id"`
	Namespace           string             `json:"namespace"`
	DedupeKey           pgtype.Text        `json:"dedupe_key"`
	TaskType            string             `json:"task_type"`
	Payload             []byte             `json:"payload"`
	QueueName           string             `json:"queue_name"`
	MaxRetry            int32              `json:"max_retry"`
	TimeoutSeconds      int32              `json:"timeout_seconds"`
	UniqueSeconds       int32              `json:"unique_seconds"`
	MaxDeliveryAttempts int32              `json:"max_delivery_attempts"`
	NextAttemptAt       pgtype.Timestamptz `json:"next_attempt_at"`
}

func (q *Queries) DeleteCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg DeleteCloudCredentialMaterializationWithTaskOutboxParams) error {
	var ok int
	return q.db.QueryRow(ctx, deleteCloudCredentialMaterializationWithTaskOutbox,
		arg.CredentialID,
		arg.ClusterID,
		arg.Namespace,
		arg.DedupeKey,
		arg.TaskType,
		arg.Payload,
		arg.QueueName,
		arg.MaxRetry,
		arg.TimeoutSeconds,
		arg.UniqueSeconds,
		arg.MaxDeliveryAttempts,
		arg.NextAttemptAt,
	).Scan(&ok)
}
