-- Phase B2 down: drop the Velero-tracking columns.

DROP INDEX IF EXISTS idx_restore_operations_running_poll;
ALTER TABLE restore_operations
    DROP COLUMN IF EXISTS last_polled_at,
    DROP COLUMN IF EXISTS poll_attempts,
    DROP COLUMN IF EXISTS namespace_mapping,
    DROP COLUMN IF EXISTS included_namespaces,
    DROP COLUMN IF EXISTS velero_restore_name,
    DROP COLUMN IF EXISTS velero_namespace,
    DROP COLUMN IF EXISTS cluster_id;

ALTER TABLE backup_schedules
    DROP COLUMN IF EXISTS ttl,
    DROP COLUMN IF EXISTS excluded_namespaces,
    DROP COLUMN IF EXISTS included_namespaces,
    DROP COLUMN IF EXISTS velero_schedule_name,
    DROP COLUMN IF EXISTS velero_namespace,
    DROP COLUMN IF EXISTS cluster_id;

DROP INDEX IF EXISTS idx_backups_running_poll;
ALTER TABLE backups
    DROP COLUMN IF EXISTS last_polled_at,
    DROP COLUMN IF EXISTS poll_attempts,
    DROP COLUMN IF EXISTS excluded_namespaces,
    DROP COLUMN IF EXISTS included_namespaces,
    DROP COLUMN IF EXISTS velero_namespace,
    DROP COLUMN IF EXISTS velero_backup_name,
    DROP COLUMN IF EXISTS cluster_id;

DROP INDEX IF EXISTS idx_backup_storage_configs_cluster;
ALTER TABLE backup_storage_configs
    DROP COLUMN IF EXISTS encrypted_credentials,
    DROP COLUMN IF EXISTS bsl_name,
    DROP COLUMN IF EXISTS velero_namespace,
    DROP COLUMN IF EXISTS cluster_id;
