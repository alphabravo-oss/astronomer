ALTER TABLE cluster_monitoring_configs
    ADD COLUMN storage_config_id UUID REFERENCES backup_storage_configs(id) ON DELETE SET NULL,
    ADD COLUMN object_storage_secret_name VARCHAR(128) NOT NULL DEFAULT '';
