-- Pod Security Templates

-- name: GetPodSecurityTemplateByID :one
SELECT * FROM pod_security_templates WHERE id = $1;

-- name: GetPodSecurityTemplateByName :one
SELECT * FROM pod_security_templates WHERE name = $1;

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

-- name: UpdateClusterSecurityPolicy :one
UPDATE cluster_security_policies SET
    template_id = $2,
    sync_status = $3,
    error_message = $4
WHERE id = $1
RETURNING *;

-- name: UpdateClusterSecurityPolicyApplied :exec
UPDATE cluster_security_policies SET applied_at = now(), sync_status = 'synced', error_message = '' WHERE id = $1;

-- name: UpdateClusterSecurityPolicyFailed :exec
UPDATE cluster_security_policies SET sync_status = 'failed', error_message = $2 WHERE id = $1;

-- name: DeleteClusterSecurityPolicy :exec
DELETE FROM cluster_security_policies WHERE id = $1;

-- name: CountClusterSecurityPolicies :one
SELECT count(*) FROM cluster_security_policies;

-- Security Scan Results

-- name: GetSecurityScanResultByID :one
SELECT * FROM security_scan_results WHERE id = $1;

-- name: ListSecurityScanResults :many
SELECT * FROM security_scan_results ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListScansByCluster :many
SELECT * FROM security_scan_results WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListScansByClusterAndType :many
SELECT * FROM security_scan_results WHERE cluster_id = $1 AND scan_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: CreateSecurityScanResult :one
INSERT INTO security_scan_results (cluster_id, scan_type, status, summary, results, initiated_by_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateSecurityScanCompleted :exec
UPDATE security_scan_results SET status = 'completed', summary = $2, results = $3, completed_at = now() WHERE id = $1;

-- name: UpdateSecurityScanFailed :exec
UPDATE security_scan_results SET status = 'failed', completed_at = now() WHERE id = $1;

-- name: DeleteSecurityScanResult :exec
DELETE FROM security_scan_results WHERE id = $1;

-- name: CountSecurityScanResults :one
SELECT count(*) FROM security_scan_results;
