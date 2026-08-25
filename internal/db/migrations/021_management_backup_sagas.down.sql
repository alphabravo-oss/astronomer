DROP INDEX IF EXISTS public.workload_operations_active_management_backup_run_idx;
DROP INDEX IF EXISTS public.management_backup_destinations_reconcile_idx;
ALTER TABLE public.management_backup_destinations
    DROP CONSTRAINT IF EXISTS management_backup_destinations_reconcile_status_valid,
    DROP CONSTRAINT IF EXISTS management_backup_destinations_desired_state_valid,
    DROP CONSTRAINT IF EXISTS management_backup_destinations_generation_valid,
    DROP COLUMN IF EXISTS last_reconciled_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS reconcile_status,
    DROP COLUMN IF EXISTS desired_state,
    DROP COLUMN IF EXISTS applied_generation,
    DROP COLUMN IF EXISTS desired_generation;
