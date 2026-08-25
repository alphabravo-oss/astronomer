-- Pod Security Templates

-- name: GetPodSecurityTemplateByID :one
SELECT * FROM pod_security_templates WHERE id = $1;

-- name: GetDefaultPodSecurityTemplate :one
SELECT * FROM pod_security_templates WHERE is_default = true LIMIT 1;

-- name: ListPodSecurityTemplates :many
SELECT * FROM pod_security_templates ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreatePodSecurityTemplate :one
INSERT INTO pod_security_templates (name, description, is_default, enforce_level, enforce_version, audit_level, audit_version, warn_level, warn_version, exempt_usernames, exempt_runtime_classes, exempt_namespaces, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpdatePodSecurityTemplate :one
UPDATE pod_security_templates SET
    name = $2,
    description = $3,
    is_default = $4,
    enforce_level = $5,
    enforce_version = $6,
    audit_level = $7,
    audit_version = $8,
    warn_level = $9,
    warn_version = $10,
    exempt_usernames = $11,
    exempt_runtime_classes = $12,
    exempt_namespaces = $13
WHERE id = $1
RETURNING *;

-- name: DeletePodSecurityTemplate :exec
DELETE FROM pod_security_templates WHERE id = $1;

-- name: CountPodSecurityTemplates :one
SELECT count(*) FROM pod_security_templates;

-- Cluster Security Policies

-- name: GetClusterSecurityPolicyByID :one
SELECT * FROM cluster_security_policies WHERE id = $1;

-- name: GetPolicyByCluster :one
SELECT * FROM cluster_security_policies WHERE cluster_id = $1;

-- name: ListClusterSecurityPolicies :many
SELECT * FROM cluster_security_policies ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateClusterSecurityPolicy :one
INSERT INTO cluster_security_policies (cluster_id, template_id, sync_status)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateClusterSecurityPolicyApplied :exec
UPDATE cluster_security_policies SET applied_at = now(), sync_status = 'synced', error_message = '' WHERE id = $1;

-- name: DeleteClusterSecurityPolicy :exec
DELETE FROM cluster_security_policies WHERE id = $1;

-- name: CountClusterSecurityPolicies :one
SELECT count(*) FROM cluster_security_policies;

-- name: ListClusterIDsWithSecurityPolicy :many
-- Estate-wide set of cluster_ids that have at least one security policy
-- row. Unbounded (no LIMIT/OFFSET) so the compliance-posture rollup can
-- answer "does this cluster have a policy?" for any fleet size in one
-- query instead of a per-cluster page that silently caps at 10 rows.
SELECT DISTINCT cluster_id FROM cluster_security_policies;

-- name: LatestCISScanPerCluster :many
-- Latest CIS scan per cluster in one pass, mirroring the per-cluster
-- "ORDER BY created_at DESC LIMIT 1" selection but batched across the
-- whole fleet for the compliance-posture rollup.
SELECT DISTINCT ON (cluster_id)
    cluster_id,
    passed,
    failed,
    completed_at
FROM security_scan_results
WHERE scan_type = 'cis'
ORDER BY cluster_id, created_at DESC;

-- Security Scan Results

-- name: GetSecurityScanResultByID :one
SELECT * FROM security_scan_results WHERE id = $1;

-- name: GetSecurityScanResultByClusterAndID :one
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.cluster_id = $1 AND s.id = $2;

-- name: GetActiveSecurityScanResultByID :one
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.id = $1;

-- name: GetActiveSecurityScanResultByIDForScopes :one
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.id = sqlc.arg(id)
  AND s.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: ListSecurityScanResults :many
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
ORDER BY s.created_at DESC, s.id DESC
LIMIT $1 OFFSET $2;

-- name: ListSecurityScanResultsForScopes :many
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[])
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: ListScansByCluster :many
SELECT s.*
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.cluster_id = $1
ORDER BY s.created_at DESC, s.id DESC
LIMIT $2 OFFSET $3;

-- name: CreateSecurityScanResult :one
INSERT INTO security_scan_results (cluster_id, scan_type, status, summary, results, initiated_by_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- Phase B5: explicit constructor that records the upstream ClusterScan CR name
-- so the worker can poll the matching ClusterScanReport for ingestion.
-- name: CreateCISScan :one
INSERT INTO security_scan_results (
    cluster_id, scan_type, status, summary, results,
    cluster_scan_name, initiated_by_id, next_poll_at, poll_deadline
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 seconds', now() + interval '35 minutes')
RETURNING *;

-- Create the management-plane scan row and its first tunnel-queue delivery
-- intent atomically. The payload needs only the generated scan id: every other
-- mutable lifecycle field is reloaded under a database lease by the consumer.
-- name: CreateCISScanWithOutbox :one
WITH scan AS (
    INSERT INTO security_scan_results (
        cluster_id, scan_type, status, summary, results,
        cluster_scan_name, initiated_by_id, next_poll_at, poll_deadline
    )
    VALUES (
        sqlc.arg(cluster_id), sqlc.arg(scan_type), 'running', '{}'::jsonb, '[]'::jsonb,
        sqlc.arg(cluster_scan_name), sqlc.arg(initiated_by_id),
        now() + interval '30 seconds', now() + interval '35 minutes'
    )
    RETURNING *
), task AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry,
        timeout_seconds, unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT
        'security_scan_ingest:' || scan.id::text || ':1',
        'security:ingest_scan_results',
        convert_to(jsonb_build_object('scan_id', scan.id::text)::text, 'UTF8'),
        'tunnel', 3, 120, 0, 20, scan.next_poll_at
    FROM scan
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE
    SET status = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        attempt_count = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.attempt_count ELSE 0 END,
        next_attempt_at = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until = NULL,
        last_error = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at = now()
    RETURNING id
), audit_intent AS (
    INSERT INTO audit_outbox (
        id, dedupe_key, event_created_at, schema_version, user_id,
        actor_auth_method, action, resource_type, resource_id, resource_name,
        http_method, path, status_code, request_id, ip_address, user_agent,
        detail, source, correlation_id, action_class, max_attempts
    )
    SELECT
        sqlc.arg(audit_id), sqlc.arg(audit_dedupe_key), now(), 'audit-v1',
        sqlc.arg(initiated_by_id), sqlc.arg(audit_actor_auth_method),
        'security.scan.create', 'security_scan', scan.id::text,
        scan.cluster_scan_name, sqlc.arg(audit_http_method), sqlc.arg(audit_path),
        201, sqlc.arg(audit_request_id), sqlc.narg(audit_ip_address),
        sqlc.arg(audit_user_agent), sqlc.arg(audit_detail), 'service',
        sqlc.arg(audit_correlation_id), 'mutation', 20
    FROM scan
    ON CONFLICT (dedupe_key) DO UPDATE
    SET dedupe_key = EXCLUDED.dedupe_key
    RETURNING id
)
SELECT scan.* FROM scan, task, audit_intent;

-- A poll is executable only after winning the row's renewable lease. The
-- generation guard prevents a stale delivery from a prior retry generation
-- from completing or failing a newly restarted scan.
-- name: ClaimSecurityScanPoll :one
UPDATE security_scan_results
SET poll_owner = sqlc.arg(owner),
    poll_lease_expires_at = sqlc.arg(lease_expires_at),
    poll_attempt = poll_attempt + 1,
    next_poll_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND poll_generation = sqlc.arg(generation)
  AND status IN ('pending', 'running', 'in_progress')
  AND cancel_requested_at IS NULL
  AND (next_poll_at IS NULL OR next_poll_at <= sqlc.arg(now_at))
  AND (poll_lease_expires_at IS NULL OR poll_lease_expires_at <= sqlc.arg(now_at))
RETURNING *;

-- name: RescheduleSecurityScanPoll :execrows
UPDATE security_scan_results
SET status = 'running',
    next_poll_at = sqlc.arg(next_poll_at),
    poll_owner = '',
    poll_lease_expires_at = NULL,
    terminal_reason = left(sqlc.arg(reason), 2048),
    summary = jsonb_set(coalesce(summary, '{}'::jsonb), '{progress}', to_jsonb(left(sqlc.arg(reason), 2048)::text), true),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND poll_generation = sqlc.arg(generation)
  AND poll_owner = sqlc.arg(owner)
  AND status IN ('pending', 'running', 'in_progress')
  AND cancel_requested_at IS NULL;

-- name: FinalizeSecurityScanReport :execrows
UPDATE security_scan_results
SET status = 'completed',
    summary = sqlc.arg(summary),
    results = sqlc.arg(results),
    passed = sqlc.arg(passed),
    failed = sqlc.arg(failed),
    warned = sqlc.arg(warned),
    skipped = sqlc.arg(skipped),
    findings = sqlc.arg(findings),
    upstream_report_name = sqlc.arg(upstream_report_name),
    terminal_reason = '',
    next_poll_at = NULL,
    poll_owner = '',
    poll_lease_expires_at = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND poll_generation = sqlc.arg(generation)
  AND poll_owner = sqlc.arg(owner)
  AND status IN ('pending', 'running', 'in_progress')
  AND cancel_requested_at IS NULL;

-- name: FailSecurityScanPoll :execrows
UPDATE security_scan_results
SET status = 'failed',
    terminal_reason = left(sqlc.arg(reason), 2048),
    summary = jsonb_set(coalesce(summary, '{}'::jsonb), '{error}', to_jsonb(left(sqlc.arg(reason), 2048)::text), true),
    next_poll_at = NULL,
    poll_owner = '',
    poll_lease_expires_at = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND poll_generation = sqlc.arg(generation)
  AND (sqlc.arg(owner)::text = '' OR poll_owner = sqlc.arg(owner))
  AND status IN ('pending', 'running', 'in_progress')
  AND cancel_requested_at IS NULL;

-- name: CancelSecurityScan :one
UPDATE security_scan_results
SET status = 'cancelled',
    cancel_requested_at = now(),
    terminal_reason = 'cancelled by operator',
    next_poll_at = NULL,
    poll_owner = '',
    poll_lease_expires_at = NULL,
    completed_at = now(),
    updated_at = now()
WHERE cluster_id = sqlc.arg(cluster_id)
  AND id = sqlc.arg(id)
  AND status IN ('pending', 'running', 'in_progress')
RETURNING *;

-- Recover rows whose initial delivery was lost, whose consumer crashed while
-- holding a lease, or whose durable next-poll timestamp is now due.
-- name: ListRecoverableSecurityScans :many
SELECT *
FROM security_scan_results
WHERE status IN ('pending', 'running', 'in_progress')
  AND cancel_requested_at IS NULL
  AND (next_poll_at IS NULL OR next_poll_at <= sqlc.arg(now_at))
  AND (poll_lease_expires_at IS NULL OR poll_lease_expires_at <= sqlc.arg(now_at))
ORDER BY coalesce(next_poll_at, started_at), created_at
LIMIT sqlc.arg(row_limit);

-- name: CountSecurityScanResults :one
SELECT count(*)
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL;

-- name: CountSecurityScanResultsForScopes :one
SELECT count(*)
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: CountSecurityScanResultsByCluster :one
SELECT count(*)
FROM security_scan_results s
JOIN clusters c ON c.id = s.cluster_id AND c.decommissioned_at IS NULL
WHERE s.cluster_id = $1;
