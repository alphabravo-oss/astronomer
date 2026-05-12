-- Rollback for 039_backup_drill.up.sql.

DROP INDEX IF EXISTS idx_backup_drill_started;
DROP TABLE IF EXISTS backup_drill_results;
