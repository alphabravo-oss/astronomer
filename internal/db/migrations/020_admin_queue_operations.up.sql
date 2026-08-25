CREATE TABLE public.admin_queue_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    action text NOT NULL,
    queue_name text NOT NULL,
    task_id text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    requested_by uuid NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    locked_until timestamptz,
    effect_started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT admin_queue_operations_action_valid
        CHECK (action IN ('retry', 'discard')),
    CONSTRAINT admin_queue_operations_status_valid
        CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')),
    CONSTRAINT admin_queue_operations_queue_nonempty
        CHECK (length(trim(queue_name)) > 0),
    CONSTRAINT admin_queue_operations_task_nonempty
        CHECK (length(trim(task_id)) > 0),
    CONSTRAINT admin_queue_operations_attempt_count_valid
        CHECK (attempt_count >= 0)
);

-- Only one recoverable intent may own a given external mutation at a time.
-- A terminal success may be followed by a later retry/discard if the same
-- Asynq task is archived again.
CREATE UNIQUE INDEX admin_queue_operations_active_target_unique
    ON public.admin_queue_operations (queue_name, task_id)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX admin_queue_operations_recent_idx
    ON public.admin_queue_operations (created_at DESC, id);
