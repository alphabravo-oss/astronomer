-- Phase A4: surface upstream ArgoCD response fields into our operations table
-- so the UI reflects the real sync state (commit sha, server-side operation id,
-- last status message) rather than fabricated rows.

ALTER TABLE argocd_operations
    ADD COLUMN IF NOT EXISTS revision      VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS message       TEXT         NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS operation_id  VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS phase         VARCHAR(32)  NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS poll_attempts INTEGER      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_polled_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_argocd_operations_running_poll
    ON argocd_operations (status, last_polled_at)
    WHERE status = 'running';
