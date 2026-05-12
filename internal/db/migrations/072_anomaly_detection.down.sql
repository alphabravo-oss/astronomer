-- Sprint 072 reversal.

DROP INDEX IF EXISTS idx_alert_rules_kind_enabled;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_anomaly_dir_valid;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rule_kind_valid;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_direction;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_min_samples;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_window_seconds;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_stddev;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS rule_kind;

DROP INDEX IF EXISTS idx_anomaly_baselines_updated_at;
DROP INDEX IF EXISTS idx_anomaly_baselines_lookup;
DROP TABLE IF EXISTS anomaly_baselines;
