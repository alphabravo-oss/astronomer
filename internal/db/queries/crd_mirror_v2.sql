-- Migration 069 — CRD-mirror v2 queries.
--
-- These queries are hand-implemented in
-- internal/db/sqlc/crd_mirror_v2_ext.sql.go (sqlc CLI is broken; we keep
-- the SQL here as documentation + a future-proof source of truth so a
-- `sqlc generate` run from a working environment would land on the same
-- bind shapes).
--
-- One block per mirrored table: list, list-by-namespace (where
-- applicable), get-by-name, upsert (idempotent), delete, prune-stale.

-- ---------------------------------------------------------------------
-- mirrored_ingress_classes
-- ---------------------------------------------------------------------

-- name: ListMirroredIngressClasses :many
SELECT id, cluster_id, name, controller, parameters, is_default,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_ingress_classes
WHERE cluster_id = $1
ORDER BY name ASC;

-- name: GetMirroredIngressClass :one
SELECT id, cluster_id, name, controller, parameters, is_default,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_ingress_classes
WHERE cluster_id = $1 AND name = $2;

-- name: UpsertMirroredIngressClass :one
INSERT INTO mirrored_ingress_classes (
    cluster_id, name, controller, parameters, is_default,
    labels, annotations, last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (cluster_id, name) DO UPDATE SET
    controller   = EXCLUDED.controller,
    parameters   = EXCLUDED.parameters,
    is_default   = EXCLUDED.is_default,
    labels       = EXCLUDED.labels,
    annotations  = EXCLUDED.annotations,
    last_seen_at = now(),
    updated_at   = now()
RETURNING id, cluster_id, name, controller, parameters, is_default,
          labels, annotations, last_seen_at, created_at, updated_at;

-- name: DeleteMirroredIngressClass :exec
DELETE FROM mirrored_ingress_classes WHERE cluster_id = $1 AND name = $2;

-- name: PruneStaleMirroredIngressClasses :execrows
DELETE FROM mirrored_ingress_classes WHERE last_seen_at < $1;

-- ---------------------------------------------------------------------
-- mirrored_gateway_classes
-- ---------------------------------------------------------------------

-- name: ListMirroredGatewayClasses :many
SELECT id, cluster_id, name, controller_name, description, parameters,
       accepted_status, labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_gateway_classes
WHERE cluster_id = $1
ORDER BY name ASC;

-- name: GetMirroredGatewayClass :one
SELECT id, cluster_id, name, controller_name, description, parameters,
       accepted_status, labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_gateway_classes
WHERE cluster_id = $1 AND name = $2;

-- name: UpsertMirroredGatewayClass :one
INSERT INTO mirrored_gateway_classes (
    cluster_id, name, controller_name, description, parameters,
    accepted_status, labels, annotations, last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (cluster_id, name) DO UPDATE SET
    controller_name = EXCLUDED.controller_name,
    description     = EXCLUDED.description,
    parameters      = EXCLUDED.parameters,
    accepted_status = EXCLUDED.accepted_status,
    labels          = EXCLUDED.labels,
    annotations     = EXCLUDED.annotations,
    last_seen_at    = now(),
    updated_at      = now()
RETURNING id, cluster_id, name, controller_name, description, parameters,
          accepted_status, labels, annotations, last_seen_at, created_at, updated_at;

-- name: DeleteMirroredGatewayClass :exec
DELETE FROM mirrored_gateway_classes WHERE cluster_id = $1 AND name = $2;

-- name: PruneStaleMirroredGatewayClasses :execrows
DELETE FROM mirrored_gateway_classes WHERE last_seen_at < $1;

-- ---------------------------------------------------------------------
-- mirrored_network_policies
-- ---------------------------------------------------------------------

-- name: ListMirroredNetworkPolicies :many
SELECT id, cluster_id, namespace, name, pod_selector, policy_types,
       ingress_rules, egress_rules, labels, annotations, is_managed,
       last_seen_at, created_at, updated_at
FROM mirrored_network_policies
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC;

-- name: ListMirroredNetworkPoliciesByNamespace :many
SELECT id, cluster_id, namespace, name, pod_selector, policy_types,
       ingress_rules, egress_rules, labels, annotations, is_managed,
       last_seen_at, created_at, updated_at
FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC;

-- name: GetMirroredNetworkPolicy :one
SELECT id, cluster_id, namespace, name, pod_selector, policy_types,
       ingress_rules, egress_rules, labels, annotations, is_managed,
       last_seen_at, created_at, updated_at
FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: UpsertMirroredNetworkPolicy :one
INSERT INTO mirrored_network_policies (
    cluster_id, namespace, name, pod_selector, policy_types,
    ingress_rules, egress_rules, labels, annotations, is_managed, last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
    pod_selector  = EXCLUDED.pod_selector,
    policy_types  = EXCLUDED.policy_types,
    ingress_rules = EXCLUDED.ingress_rules,
    egress_rules  = EXCLUDED.egress_rules,
    labels        = EXCLUDED.labels,
    annotations   = EXCLUDED.annotations,
    is_managed    = EXCLUDED.is_managed,
    last_seen_at  = now(),
    updated_at    = now()
RETURNING id, cluster_id, namespace, name, pod_selector, policy_types,
          ingress_rules, egress_rules, labels, annotations, is_managed,
          last_seen_at, created_at, updated_at;

-- name: DeleteMirroredNetworkPolicy :exec
DELETE FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: PruneStaleMirroredNetworkPolicies :execrows
DELETE FROM mirrored_network_policies WHERE last_seen_at < $1;

-- ---------------------------------------------------------------------
-- mirrored_resource_quotas
-- ---------------------------------------------------------------------

-- name: ListMirroredResourceQuotas :many
SELECT id, cluster_id, namespace, name, hard, used, scopes,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_resource_quotas
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC;

-- name: ListMirroredResourceQuotasByNamespace :many
SELECT id, cluster_id, namespace, name, hard, used, scopes,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC;

-- name: GetMirroredResourceQuota :one
SELECT id, cluster_id, namespace, name, hard, used, scopes,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: UpsertMirroredResourceQuota :one
INSERT INTO mirrored_resource_quotas (
    cluster_id, namespace, name, hard, used, scopes,
    labels, annotations, last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
    hard         = EXCLUDED.hard,
    used         = EXCLUDED.used,
    scopes       = EXCLUDED.scopes,
    labels       = EXCLUDED.labels,
    annotations  = EXCLUDED.annotations,
    last_seen_at = now(),
    updated_at   = now()
RETURNING id, cluster_id, namespace, name, hard, used, scopes,
          labels, annotations, last_seen_at, created_at, updated_at;

-- name: DeleteMirroredResourceQuota :exec
DELETE FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: PruneStaleMirroredResourceQuotas :execrows
DELETE FROM mirrored_resource_quotas WHERE last_seen_at < $1;

-- ---------------------------------------------------------------------
-- mirrored_limit_ranges
-- ---------------------------------------------------------------------

-- name: ListMirroredLimitRanges :many
SELECT id, cluster_id, namespace, name, limits,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_limit_ranges
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC;

-- name: ListMirroredLimitRangesByNamespace :many
SELECT id, cluster_id, namespace, name, limits,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC;

-- name: GetMirroredLimitRange :one
SELECT id, cluster_id, namespace, name, limits,
       labels, annotations, last_seen_at, created_at, updated_at
FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: UpsertMirroredLimitRange :one
INSERT INTO mirrored_limit_ranges (
    cluster_id, namespace, name, limits,
    labels, annotations, last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
    limits       = EXCLUDED.limits,
    labels       = EXCLUDED.labels,
    annotations  = EXCLUDED.annotations,
    last_seen_at = now(),
    updated_at   = now()
RETURNING id, cluster_id, namespace, name, limits,
          labels, annotations, last_seen_at, created_at, updated_at;

-- name: DeleteMirroredLimitRange :exec
DELETE FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2 AND name = $3;

-- name: PruneStaleMirroredLimitRanges :execrows
DELETE FROM mirrored_limit_ranges WHERE last_seen_at < $1;
