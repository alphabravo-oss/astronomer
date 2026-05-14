-- Sprint 081 — image vulnerability scan snapshots.
--
-- The sprint-062 ingester UPSERTs `image_vulnerability_reports` on
-- each Trivy mirror event, so the live row only ever carries the
-- LATEST counts. Operators want three derived views:
--
--   1. Scan history — a sparkline of total critical/high counts over
--      time, so they can see "we went from 18 criticals on Monday to
--      7 today, the cleanup is working."
--   2. What changed since the last scan — which CVEs newly appeared,
--      which were resolved.
--   3. Download — a CSV / JSON export of the current snapshot for
--      ticket attachments + offline analysis.
--
-- This migration adds the snapshot history table that the ingester
-- appends to on every event. The base row stays the canonical
-- "latest" record (matching today's read API surface); the snapshot
-- table is an append-only audit trail.
--
-- Retention: 90 days, swept hourly by the existing
-- ImageVulnerabilityRetentionTask (sprint 064 retention helper —
-- this migration also adds the new prune-snapshots :exec query that
-- the task calls). 90 days because the typical SLA conversation
-- ("show me CVE trend over the quarter") needs at least that much
-- history; older rows are summarised into the daily-rollup view
-- below before being deleted.
--
-- Storage cost estimate (200 pods × 1 daily rescan × 90 days ×
-- ~250 bytes/row) is ~5 MB per managed cluster, which is fine
-- alongside the multi-GB image_vulnerabilities CVE rows we already
-- store.

CREATE TABLE image_vulnerability_report_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- report_id is the live image_vulnerability_reports row this
    -- snapshot derives from. Cascade so deleting a report (cluster
    -- decommission, workload removal) cleans its history too — the
    -- historical rows are useless without the live row's context.
    report_id       UUID NOT NULL REFERENCES image_vulnerability_reports(id) ON DELETE CASCADE,
    -- cluster_id denormalised here so the per-cluster history query
    -- doesn't have to JOIN through reports just to filter. Same
    -- ON DELETE rule cascades from clusters → reports → snapshots.
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    critical_count  INTEGER NOT NULL DEFAULT 0,
    high_count      INTEGER NOT NULL DEFAULT 0,
    medium_count    INTEGER NOT NULL DEFAULT 0,
    low_count       INTEGER NOT NULL DEFAULT 0,
    unknown_count   INTEGER NOT NULL DEFAULT 0,
    -- scanned_at carries the upstream Trivy timestamp; we don't use
    -- now() because the agent may batch + re-send late on reconnect
    -- and the trend line should reflect when the scan actually ran,
    -- not when we received it.
    scanned_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cluster-scoped trend query: every history-API call filters by
-- cluster_id + ORDER BY scanned_at. The composite covers both.
CREATE INDEX idx_ivrs_cluster_scanned ON image_vulnerability_report_snapshots
    (cluster_id, scanned_at DESC);

-- Per-report drill-down — "show me this image's CVE history" — is
-- a (report_id, scanned_at) scan. Separate from the cluster index
-- because the cardinality + access pattern differs (one report's
-- timeline is usually <100 rows; cluster-wide is thousands).
CREATE INDEX idx_ivrs_report_scanned ON image_vulnerability_report_snapshots
    (report_id, scanned_at DESC);

-- Idempotency: prevent two-of-the-same-second-event from
-- double-counting. The upstream Trivy timestamp has second precision
-- and the ingester is happy to re-emit the same scan on reconnect
-- (mirror_subscriber replay), so a unique key drops duplicates
-- cheaply at insert time.
CREATE UNIQUE INDEX idx_ivrs_dedup ON image_vulnerability_report_snapshots
    (report_id, scanned_at);
