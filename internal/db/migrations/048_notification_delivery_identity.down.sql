DROP INDEX IF EXISTS email_messages_dedupe_key_unique;

ALTER TABLE email_messages
    DROP COLUMN IF EXISTS dedupe_key;
