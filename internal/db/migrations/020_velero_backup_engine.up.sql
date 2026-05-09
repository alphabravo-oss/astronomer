-- Phase B2: Velero is the backup engine. Backups, schedules and storage
-- configurations are projected as Velero CRDs (BackupStorageLocation,
-- Schedule, Backup, Restore) into the cluster's velero namespace. This
-- migration adds the columns we need to track the cluster scope and the
-- Velero CR identities so we can round-trip status from upstream into our
-- own UI without fabricating data.

-- Storage config now belongs to a specific cluster (Velero is per-cluster).
-- The cluster column is nullable while existing rows are migrated; once
-- backfilled it is expected to be set. The encrypted_credentials column
-- stores Fernet-encrypted aws-style credentials so the agent can render a
-- BSL Secret on demand.
ALTER TABLE backup_storage_configs
    ADD COLUMN IF NOT EXISTS cluster_id              UUID REFERENCES clusters(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS velero_namespace        VARCHAR(63) NOT NULL DEFAULT 'velero',
    ADD COLUMN IF NOT EXISTS bsl_name                VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS encrypted_credentials   TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_backup_storage_configs_cluster
    ON backup_storage_configs (cluster_id);

-- Backups: track the upstream Velero Backup CR + included/excluded namespaces
-- so manual triggers and scheduled fan-outs can both surface real status.
ALTER TABLE backups
    ADD COLUMN IF NOT EXISTS cluster_id              UUID REFERENCES clusters(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS velero_backup_name      VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS velero_namespace        VARCHAR(63) NOT NULL DEFAULT 'velero',
    ADD COLUMN IF NOT EXISTS included_namespaces     JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS excluded_namespaces     JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS poll_attempts           INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_polled_at          TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_backups_running_poll
    ON backups (status, last_polled_at)
    WHERE status = 'running';

-- Schedules grow the same Velero-aware columns. include/exclude semantics
-- are passed through to Velero's `spec.template`.
ALTER TABLE backup_schedules
    ADD COLUMN IF NOT EXISTS cluster_id              UUID REFERENCES clusters(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS velero_namespace        VARCHAR(63) NOT NULL DEFAULT 'velero',
    ADD COLUMN IF NOT EXISTS velero_schedule_name    VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS included_namespaces     JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS excluded_namespaces     JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS ttl                     VARCHAR(32) NOT NULL DEFAULT '';

-- Restores: track the upstream Velero Restore CR.
ALTER TABLE restore_operations
    ADD COLUMN IF NOT EXISTS cluster_id              UUID REFERENCES clusters(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS velero_namespace        VARCHAR(63) NOT NULL DEFAULT 'velero',
    ADD COLUMN IF NOT EXISTS velero_restore_name     VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS included_namespaces     JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS namespace_mapping       JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS poll_attempts           INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_polled_at          TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_restore_operations_running_poll
    ON restore_operations (status, last_polled_at)
    WHERE status = 'running';
