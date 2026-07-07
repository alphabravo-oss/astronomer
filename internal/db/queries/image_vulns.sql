-- Sprint 062 — image vulnerability report + per-CVE row CRUD.
--
-- Generated sqlc output is the canonical Go API for this surface.

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
    COALESCE(MAX(scanned_at), '1970-01-01T00:00:00Z'::timestamptz) AS last_scanned_at
FROM image_vulnerability_reports
WHERE cluster_id = $1;

-- name: AggregateVulnerabilitiesPerCluster :many
-- Per-cluster critical/high/report_count aggregate for the whole fleet
-- in one pass. Batched equivalent of AggregateClusterVulnerabilities so
-- the compliance-posture rollup avoids one query per cluster.
SELECT
    cluster_id,
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COUNT(*)::bigint AS report_count
FROM image_vulnerability_reports
GROUP BY cluster_id;

-- name: AggregateFleetVulnerabilities :one
SELECT
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COALESCE(SUM(medium_count), 0)::bigint AS medium,
    COALESCE(SUM(low_count), 0)::bigint AS low,
    COALESCE(SUM(unknown_count), 0)::bigint AS unknown,
    COUNT(*)::bigint AS report_count,
    COUNT(DISTINCT cluster_id)::bigint AS cluster_count,
    COALESCE(MAX(scanned_at), '1970-01-01T00:00:00Z'::timestamptz) AS last_scanned_at
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
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: CountVulnerableImagesForCluster :one
SELECT COUNT(*)::bigint FROM image_vulnerability_reports WHERE cluster_id = $1;

-- name: ListVulnerabilitiesForReport :many
-- Severity filter is empty-string-treated-as-no-filter so callers can
-- pass an empty string for the unfiltered list.
SELECT * FROM image_vulnerabilities
WHERE report_id = @report_id::uuid
  AND (@severity_filter::text = '' OR severity = @severity_filter::text)
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
LIMIT @page_limit::int OFFSET @page_offset::int;

-- name: CountVulnerabilitiesForReport :one
SELECT COUNT(*)::bigint FROM image_vulnerabilities
WHERE report_id = @report_id::uuid
  AND (@severity_filter::text = '' OR severity = @severity_filter::text);

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
    COALESCE(MAX(scanned_at), '1970-01-01T00:00:00Z'::timestamptz) AS last_scanned_at
FROM image_vulnerability_reports
GROUP BY cluster_id
ORDER BY critical DESC, high DESC
LIMIT $1;
