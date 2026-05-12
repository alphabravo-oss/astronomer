-- Phase: backup restore drill results.
--
-- The nightly pg_dump CronJob (deploy/chart/templates/management-plane-backup-cronjob.yaml)
-- produces backups, but until now nothing has *proven* they're restorable.
-- The new restore-drill CronJob (deploy/chart/templates/management-plane-restore-drill-cronjob.yaml)
-- runs weekly, pulls the latest backup from S3, restores it into an ephemeral
-- sidecar Postgres, runs a schema-sanity + row-count check, and records the
-- outcome here.
--
-- A separate admin endpoint (GET /api/v1/admin/backup-drill/) reads this table
-- so operators can confirm the drill is current; PrometheusRule alerts fire
-- when no successful row has appeared in 14 days
-- (AstronomerBackupRestoreDrillStale) or when failures are accumulating
-- (AstronomerBackupRestoreDrillFailed).
--
-- NIST CP-9 and ISO 27001 A.12.3.1 both require backup restorability to be
-- periodically validated; this is the audit-trail half of the loop. The
-- restore itself goes into the sidecar Postgres — this table is the *only*
-- thing the drill ever writes to the production management DB.

CREATE TABLE IF NOT EXISTS backup_drill_results (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- When the drill Job started its work (set by the restore container at
    -- the top of its script, before download).
    started_at     TIMESTAMPTZ NOT NULL,
    -- When the drill finished — NULL only if the row was inserted but the
    -- finalising UPDATE never landed (Job pod evicted mid-run, etc).
    finished_at    TIMESTAMPTZ,
    -- 'success' | 'failure'. Kept as a VARCHAR rather than an enum so we
    -- can extend later (e.g. 'partial' if some checks pass and others fail)
    -- without a follow-up migration.
    status         VARCHAR(32) NOT NULL,
    -- The S3 key of the backup that was restored. Empty when the drill
    -- failed before it could even resolve the latest backup.
    backup_key     VARCHAR(512) NOT NULL DEFAULT '',
    -- The schema_migrations.version observed in the restored DB. NULL when
    -- the drill never got far enough to read it (e.g. restore failed
    -- partway through).
    schema_version INTEGER,
    -- Free-text failure detail; empty on success.
    error_message  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The two consumers — the admin endpoint's "latest" and "history" queries,
-- plus the Prometheus staleness rule — both scan by started_at DESC. One
-- index covers them.
CREATE INDEX IF NOT EXISTS idx_backup_drill_started
    ON backup_drill_results (started_at DESC);
