ALTER TABLE public.email_messages
    ADD COLUMN dedupe_key text;

CREATE UNIQUE INDEX email_messages_dedupe_key_unique
    ON public.email_messages (dedupe_key)
    WHERE dedupe_key IS NOT NULL;
