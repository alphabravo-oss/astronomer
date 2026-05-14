-- Down migration for 088.
--
-- This is a one-shot data fix. We can't recover the prior 'disconnected'
-- (or other) status values because we don't have a history column. A
-- no-op down is the safe choice — re-applying 088 is idempotent.

SELECT 1;
