-- Down migration for 087.
--
-- The up migration is a one-shot data fix; there is no clean reversal
-- because we can't distinguish backfilled rows from rows that legitimately
-- failed with the sentinel error_message. We could match the sentinel and
-- flip back to 'running', but that would re-introduce the very orphan
-- state we were repairing.
--
-- Choose a no-op so down-migrating doesn't corrupt the data.

SELECT 1;
