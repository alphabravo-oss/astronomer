DROP INDEX IF EXISTS task_outbox_dead_idx;
DROP INDEX IF EXISTS task_outbox_due_idx;
DROP INDEX IF EXISTS task_outbox_dedupe_key_unique;
DROP TABLE IF EXISTS task_outbox;
