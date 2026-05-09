ALTER TABLE cluster_monitoring_configs
    DROP COLUMN IF EXISTS object_storage_secret_name,
    DROP COLUMN IF EXISTS storage_config_id;
