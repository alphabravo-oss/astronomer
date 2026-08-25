-- The canonical schema clears search_path, so every relation in incremental
-- migrations must be schema-qualified. Defaults backfill existing authored
-- rows without rewriting their manifest or ownership data.
ALTER TABLE public.authored_constraints
    ADD COLUMN desired_state varchar(16) NOT NULL DEFAULT 'present',
    ADD COLUMN sync_status varchar(16) NOT NULL DEFAULT 'pending',
    ADD COLUMN generation bigint NOT NULL DEFAULT 1,
    ADD COLUMN observed_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN last_error text NOT NULL DEFAULT '',
    ADD COLUMN last_reconciled_at timestamptz;

ALTER TABLE public.authored_constraints
    ADD CONSTRAINT authored_constraints_desired_state_valid
        CHECK (desired_state IN ('present', 'absent')),
    ADD CONSTRAINT authored_constraints_sync_status_valid
        CHECK (sync_status IN ('pending', 'synced', 'failed')),
    ADD CONSTRAINT authored_constraints_generation_valid
        CHECK (generation > 0 AND observed_generation >= 0 AND observed_generation <= generation);

CREATE INDEX idx_authored_constraints_reconcile_due
    ON public.authored_constraints (updated_at, cluster_id, name)
    WHERE sync_status IN ('pending', 'failed');
