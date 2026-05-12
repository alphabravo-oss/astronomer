-- Reverse of 078_registration_wizard.up.sql. The clusters columns are
-- kept ON DOWN because dropping a NOT NULL column rewrites the entire
-- table — too expensive a rollback for what is otherwise additive. The
-- per-step table can be dropped safely.

DROP INDEX IF EXISTS idx_reg_steps_cluster;
DROP TABLE IF EXISTS cluster_registration_steps;

ALTER TABLE clusters DROP CONSTRAINT IF EXISTS registration_phase_valid;
ALTER TABLE clusters DROP COLUMN IF EXISTS install_baseline;
ALTER TABLE clusters DROP COLUMN IF EXISTS registration_completed_at;
ALTER TABLE clusters DROP COLUMN IF EXISTS registration_started_at;
ALTER TABLE clusters DROP COLUMN IF EXISTS registration_phase;
