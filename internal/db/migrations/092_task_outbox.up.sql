-- Durable outbox for critical Asynq task intents.
--
-- Redis is an execution queue, not the source of truth. Handlers that commit
-- durable Postgres state and then need async processing can insert one of these
-- rows in the same transaction. The task_outbox dispatcher retries delivery to
-- Asynq until the task is acknowledged or an operator-visible dead state is hit.

CREATE TABLE IF NOT EXISTS task_outbox (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key            TEXT,
    task_type             TEXT NOT NULL,
    payload               BYTEA NOT NULL DEFAULT ''::bytea,
    queue_name            TEXT NOT NULL DEFAULT 'default',
    max_retry             INTEGER NOT NULL DEFAULT 3,
    timeout_seconds       INTEGER NOT NULL DEFAULT 0,
    unique_seconds        INTEGER NOT NULL DEFAULT 0,
    max_delivery_attempts INTEGER NOT NULL DEFAULT 20,
    status                TEXT NOT NULL DEFAULT 'pending',
    attempt_count         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until          TIMESTAMPTZ,
    delivered_at          TIMESTAMPTZ,
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_outbox_task_type_nonempty CHECK (length(trim(task_type)) > 0),
    CONSTRAINT task_outbox_queue_name_nonempty CHECK (length(trim(queue_name)) > 0),
    CONSTRAINT task_outbox_max_retry_valid CHECK (max_retry >= 0),
    CONSTRAINT task_outbox_timeout_seconds_valid CHECK (timeout_seconds >= 0),
    CONSTRAINT task_outbox_unique_seconds_valid CHECK (unique_seconds >= 0),
    CONSTRAINT task_outbox_max_delivery_attempts_valid CHECK (max_delivery_attempts > 0),
    CONSTRAINT task_outbox_status_valid CHECK (status IN ('pending', 'delivering', 'failed', 'delivered', 'dead'))
);

CREATE UNIQUE INDEX IF NOT EXISTS task_outbox_dedupe_key_unique
    ON task_outbox (dedupe_key)
    WHERE dedupe_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS task_outbox_due_idx
    ON task_outbox (status, next_attempt_at, created_at)
    WHERE status IN ('pending', 'failed', 'delivering');

CREATE INDEX IF NOT EXISTS task_outbox_dead_idx
    ON task_outbox (updated_at)
    WHERE status = 'dead';
