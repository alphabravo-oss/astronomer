-- Down for 086_cluster_condition_remediation.
--
-- Drops the table and its two indexes. The reconciler that writes to
-- this table degrades gracefully when the table is missing (every
-- INSERT becomes a no-op with a logged warning), so this is safe to
-- run on a live cluster.

DROP INDEX IF EXISTS idx_ccra_attempted_at;
DROP INDEX IF EXISTS idx_ccra_cluster_type_attempted;
DROP TABLE IF EXISTS cluster_condition_remediation_attempts;
