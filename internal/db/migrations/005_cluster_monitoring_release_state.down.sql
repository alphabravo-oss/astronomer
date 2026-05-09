ALTER TABLE cluster_monitoring_configs
    DROP COLUMN IF EXISTS last_drift_detected_at,
    DROP COLUMN IF EXISTS last_observed_at,
    DROP COLUMN IF EXISTS last_observed_revision,
    DROP COLUMN IF EXISTS last_observed_status,
    DROP COLUMN IF EXISTS last_applied_spec_hash;
