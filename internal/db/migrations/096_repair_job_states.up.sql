CREATE TABLE IF NOT EXISTS repair_job_states (
    job_name text NOT NULL,
    scope text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unknown',
    last_successful_reconcile_at timestamptz,
    last_error_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    success_count bigint NOT NULL DEFAULT 0,
    error_count bigint NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_name, scope),
    CONSTRAINT repair_job_states_status_check CHECK (status IN ('unknown', 'success', 'failed', 'skipped'))
);

CREATE INDEX IF NOT EXISTS idx_repair_job_states_status_updated
    ON repair_job_states (status, updated_at DESC);
