ALTER TABLE cluster_monitoring_configs
    ADD COLUMN storage_class VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN storage_size VARCHAR(64) NOT NULL DEFAULT '';
