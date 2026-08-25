DROP INDEX IF EXISTS public.security_scan_results_poll_due_idx;

ALTER TABLE public.security_scan_results
    DROP CONSTRAINT IF EXISTS security_scan_results_poll_attempt_valid,
    DROP CONSTRAINT IF EXISTS security_scan_results_poll_generation_valid,
    DROP COLUMN IF EXISTS cancel_requested_at,
    DROP COLUMN IF EXISTS terminal_reason,
    DROP COLUMN IF EXISTS upstream_report_name,
    DROP COLUMN IF EXISTS poll_lease_expires_at,
    DROP COLUMN IF EXISTS poll_owner,
    DROP COLUMN IF EXISTS poll_deadline,
    DROP COLUMN IF EXISTS next_poll_at,
    DROP COLUMN IF EXISTS poll_attempt,
    DROP COLUMN IF EXISTS poll_generation;
