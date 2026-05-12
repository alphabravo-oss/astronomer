-- Sprint 062 — image vulnerability report + per-CVE row CRUD.
--
-- These queries are also implemented by hand in
-- internal/db/sqlc/image_vulns_ext.sql.go because the local sqlc CLI is
-- broken in this repo. This file remains as the canonical source-of-
-- truth for what the queries do; future regenerations re-emit the hand
-- file's surface.

-- name: UpsertImageVulnerabilityReport :one
-- Idempotent on the upstream (cluster_id, report_name) key. Updates
-- aggregate counts + scanned_at on every re-ingest so the rollups stay
-- live without an extra SELECT.
INSERT INTO image_vulnerability_reports (
    cluster_id, report_name, namespace, workload_kind, workload_name,
    container_name, image_registry, image_repo, image_tag, image_digest,
    scanner, scanner_version,
    critical_count, high_count, medium_count, low_count, unknown_count,
    scanned_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12,
    $13, $14, $15, $16, $17,
    $18
)
ON CONFLICT (cluster_id, report_name) DO UPDATE SET
    namespace       = EXCLUDED.namespace,
    workload_kind   = EXCLUDED.workload_kind,
    workload_name   = EXCLUDED.workload_name,
    container_name  = EXCLUDED.container_name,
    image_registry  = EXCLUDED.image_registry,
    image_repo      = EXCLUDED.image_repo,
    image_tag       = EXCLUDED.image_tag,
    image_digest    = EXCLUDED.image_digest,
    scanner         = EXCLUDED.scanner,
    scanner_version = EXCLUDED.scanner_version,
    critical_count  = EXCLUDED.critical_count,
    high_count      = EXCLUDED.high_count,
    medium_count    = EXCLUDED.medium_count,
    low_count       = EXCLUDED.low_count,
    unknown_count   = EXCLUDED.unknown_count,
    scanned_at      = EXCLUDED.scanned_at,
    updated_at      = now()
RETURNING *;

-- name: GetImageVulnerabilityReportByID :one
SELECT * FROM image_vulnerability_reports WHERE id = $1;

-- name: DeleteImageVulnerabilitiesByReport :exec
-- Wipe a report's CVE rows before bulk re-insert. The ON DELETE CASCADE
-- on the FK would do this for free when the parent row is deleted, but
-- on an upsert path we need an explicit clear.
DELETE FROM image_vulnerabilities WHERE report_id = $1;

-- name: AggregateClusterVulnerabilities :one
SELECT
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COALESCE(SUM(medium_count), 0)::bigint AS medium,
    COALESCE(SUM(low_count), 0)::bigint AS low,
    COALESCE(SUM(unknown_count), 0)::bigint AS unknown,
    COUNT(*)::bigint AS report_count,
    MAX(scanned_at) AS last_scanned_at
FROM image_vulnerability_reports
WHERE cluster_id = $1;

-- name: AggregateFleetVulnerabilities :one
SELECT
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COALESCE(SUM(medium_count), 0)::bigint AS medium,
    COALESCE(SUM(low_count), 0)::bigint AS low,
    COALESCE(SUM(unknown_count), 0)::bigint AS unknown,
    COUNT(*)::bigint AS report_count,
    COUNT(DISTINCT cluster_id)::bigint AS cluster_count,
    MAX(scanned_at) AS last_scanned_at
FROM image_vulnerability_reports;

-- name: TopVulnerableImages :many
-- Ordered by critical desc, high desc — the idx_ivr_cluster_severity
-- index covers this without an explicit sort.
SELECT * FROM image_vulnerability_reports
WHERE cluster_id = $1
ORDER BY critical_count DESC, high_count DESC, scanned_at DESC
LIMIT $2 OFFSET $3;

-- name: ListVulnerableImagesByNamespace :many
SELECT * FROM image_vulnerability_reports
WHERE cluster_id = $1 AND namespace = $2
ORDER BY critical_count DESC, high_count DESC, scanned_at DESC
LIMIT $3 OFFSET $4;

-- name: CountVulnerableImagesForCluster :one
SELECT COUNT(*)::bigint FROM image_vulnerability_reports WHERE cluster_id = $1;

-- name: ListVulnerabilitiesForReport :many
-- Severity filter is empty-string-treated-as-no-filter so callers can
-- pass '' for the unfiltered list.
SELECT * FROM image_vulnerabilities
WHERE report_id = $1
  AND ($2::text = '' OR severity = $2)
ORDER BY
    CASE severity
        WHEN 'CRITICAL' THEN 0
        WHEN 'HIGH' THEN 1
        WHEN 'MEDIUM' THEN 2
        WHEN 'LOW' THEN 3
        ELSE 4
    END ASC,
    cvss_score DESC NULLS LAST,
    vulnerability_id ASC
LIMIT $3 OFFSET $4;

-- name: CountVulnerabilitiesForReport :one
SELECT COUNT(*)::bigint FROM image_vulnerabilities
WHERE report_id = $1
  AND ($2::text = '' OR severity = $2);

-- name: TopClustersByVulnerability :many
-- For the fleet rollup at /dashboard/security/. Sums critical+high per
-- cluster_id and returns the worst N.
SELECT
    cluster_id,
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COALESCE(SUM(medium_count), 0)::bigint AS medium,
    COALESCE(SUM(low_count), 0)::bigint AS low,
    COUNT(*)::bigint AS report_count,
    MAX(scanned_at) AS last_scanned_at
FROM image_vulnerability_reports
GROUP BY cluster_id
ORDER BY critical DESC, high DESC
LIMIT $1;
