ALTER TABLE public.management_backup_destinations
    ADD COLUMN desired_generation bigint NOT NULL DEFAULT 1,
    ADD COLUMN applied_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN desired_state text NOT NULL DEFAULT 'active',
    ADD COLUMN reconcile_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN last_error text NOT NULL DEFAULT '',
    ADD COLUMN last_reconciled_at timestamptz;

ALTER TABLE public.management_backup_destinations
    ADD CONSTRAINT management_backup_destinations_generation_valid
        CHECK (desired_generation > 0 AND applied_generation >= 0 AND applied_generation <= desired_generation),
    ADD CONSTRAINT management_backup_destinations_desired_state_valid
        CHECK (desired_state IN ('active', 'deleted')),
    ADD CONSTRAINT management_backup_destinations_reconcile_status_valid
        CHECK (reconcile_status IN ('pending', 'applying', 'retrying', 'ready', 'failed'));

CREATE INDEX management_backup_destinations_reconcile_idx
    ON public.management_backup_destinations (reconcile_status, updated_at)
    WHERE reconcile_status IN ('pending', 'retrying');

-- Manual backup runs are long-lived external workflows. A destination may
-- have at most one active run regardless of idempotency key or API replica.
CREATE UNIQUE INDEX workload_operations_active_management_backup_run_idx
    ON public.workload_operations (target_key)
    WHERE target_type = 'management_backup_destination'
      AND operation_type = 'management_backup_run'
      AND status IN ('pending', 'running', 'retrying');
