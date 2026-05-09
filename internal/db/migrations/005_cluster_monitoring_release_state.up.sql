ALTER TABLE cluster_monitoring_configs
    ADD COLUMN last_applied_spec_hash VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN last_observed_status VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN last_observed_revision INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_observed_at TIMESTAMPTZ,
    ADD COLUMN last_drift_detected_at TIMESTAMPTZ;
