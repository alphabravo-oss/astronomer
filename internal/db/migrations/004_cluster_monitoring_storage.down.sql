ALTER TABLE cluster_monitoring_configs
    DROP COLUMN IF EXISTS storage_size,
    DROP COLUMN IF EXISTS storage_class;
