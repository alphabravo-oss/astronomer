CREATE TABLE IF NOT EXISTS operation_idempotency_keys (
    scope text NOT NULL,
    idempotency_key text NOT NULL,
    operation_table text NOT NULL DEFAULT '',
    operation_id uuid,
    response jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, idempotency_key),
    CONSTRAINT operation_idempotency_keys_scope_check CHECK (scope <> ''),
    CONSTRAINT operation_idempotency_keys_key_check CHECK (idempotency_key <> '')
);

CREATE INDEX IF NOT EXISTS idx_operation_idempotency_keys_operation
    ON operation_idempotency_keys (operation_table, operation_id)
    WHERE operation_id IS NOT NULL;
