DROP INDEX IF EXISTS public.logging_outputs_one_system_per_cluster;

ALTER TABLE public.logging_outputs
    DROP CONSTRAINT IF EXISTS logging_outputs_system_requires_cluster;

ALTER TABLE public.logging_outputs
    DROP COLUMN IF EXISTS is_system;
