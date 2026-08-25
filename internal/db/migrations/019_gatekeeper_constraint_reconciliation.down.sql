DROP INDEX IF EXISTS public.idx_authored_constraints_reconcile_due;

ALTER TABLE public.authored_constraints
    DROP CONSTRAINT IF EXISTS authored_constraints_generation_valid,
    DROP CONSTRAINT IF EXISTS authored_constraints_sync_status_valid,
    DROP CONSTRAINT IF EXISTS authored_constraints_desired_state_valid,
    DROP COLUMN IF EXISTS last_reconciled_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS observed_generation,
    DROP COLUMN IF EXISTS generation,
    DROP COLUMN IF EXISTS sync_status,
    DROP COLUMN IF EXISTS desired_state;
