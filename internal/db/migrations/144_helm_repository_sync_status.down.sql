-- Reverse of 144: drop the per-repository sync-failure columns.
--
-- Dropping loses the recorded failure reasons. That is acceptable on a
-- rollback because the columns hold only the most recent sweep's outcome,
-- which the next sweep on the re-upgraded schema regenerates in full.

ALTER TABLE helm_repositories DROP COLUMN IF EXISTS last_sync_attempted_at;

ALTER TABLE helm_repositories DROP COLUMN IF EXISTS last_sync_error;
