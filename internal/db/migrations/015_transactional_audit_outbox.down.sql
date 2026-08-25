DROP INDEX IF EXISTS public.siem_forward_queue_dedupe_idx;

ALTER TABLE public.siem_forward_queue
    DROP COLUMN IF EXISTS dedupe_key;

DROP TABLE IF EXISTS public.audit_outbox;
