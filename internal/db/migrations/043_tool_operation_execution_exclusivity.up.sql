-- A target has at most one executing plan, even when replicas claim distinct
-- pending operations concurrently. Attempts renew and fence their own row.
WITH ranked AS (
    SELECT id, row_number() OVER (
        PARTITION BY target_type, target_key ORDER BY created_at DESC, id DESC
    ) AS position
    FROM public.tool_operations WHERE status = 'running'
), superseded AS (
    UPDATE public.tool_operations operation
    SET status = 'superseded', completed_at = now(), updated_at = now(),
        error_message = 'Superseded duplicate execution while establishing target exclusivity'
    FROM ranked WHERE operation.id = ranked.id AND ranked.position > 1
    RETURNING operation.id
)
INSERT INTO public.tool_operation_events (operation_id, level, stage, message, detail)
SELECT id, 'warn', 'queue', 'Duplicate running operation superseded by execution exclusivity migration', '{}'::jsonb
FROM superseded;

CREATE UNIQUE INDEX IF NOT EXISTS tool_operations_running_target_uidx
    ON public.tool_operations (target_type, target_key)
    WHERE status = 'running';
