DROP INDEX IF EXISTS public.email_messages_dedupe_key_unique;

ALTER TABLE public.email_messages
    DROP COLUMN IF EXISTS dedupe_key;
