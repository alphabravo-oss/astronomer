// Migration 069 — CRD-mirror v2 queries, hand-authored sqlc shim.
//
// Mirrors what `sqlc generate` would emit for
// internal/db/queries/crd_mirror_v2.sql. Kept off the canonical
// models.go / *.sql.go output paths so future regen runs don't
// clobber it (same pattern as cluster_snapshots_ext.sql.go).
//
// Each table gets list-per-cluster, optional list-per-(cluster,
// namespace), get-by-name, upsert (idempotent on the natural key),
// delete, prune-stale.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------
// mirrored_ingress_classes
// ---------------------------------------------------------------------

const mirroredIngressClassColumns = `
    id, cluster_id, name, controller, parameters, is_default,
    labels, annotations, last_seen_at, created_at, updated_at`

func scanMirroredIngressClassRow(row interface {
	Scan(dest ...any) error
}) (MirroredIngressClass, error) {
	var i MirroredIngressClass
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Name,
		&i.Controller,
		&i.Parameters,
		&i.IsDefault,
		&i.Labels,
		&i.Annotations,
		&i.LastSeenAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMirroredIngressClasses = `-- name: ListMirroredIngressClasses :many
SELECT ` + mirroredIngressClassColumns + `
FROM mirrored_ingress_classes
WHERE cluster_id = $1
ORDER BY name ASC`

func (q *Queries) ListMirroredIngressClasses(ctx context.Context, clusterID uuid.UUID) ([]MirroredIngressClass, error) {
	rows, err := q.db.Query(ctx, listMirroredIngressClasses, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredIngressClass{}
	for rows.Next() {
		i, err := scanMirroredIngressClassRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getMirroredIngressClass = `-- name: GetMirroredIngressClass :one
SELECT ` + mirroredIngressClassColumns + `
FROM mirrored_ingress_classes
WHERE cluster_id = $1 AND name = $2`

// GetMirroredIngressClassParams is the bind set for the natural-key get.
type GetMirroredIngressClassParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Name      string    `json:"name"`
}

func (q *Queries) GetMirroredIngressClass(ctx context.Context, arg GetMirroredIngressClassParams) (MirroredIngressClass, error) {
	return scanMirroredIngressClassRow(q.db.QueryRow(ctx, getMirroredIngressClass, arg.ClusterID, arg.Name))
}

const upsertMirroredIngressClass = `-- name: UpsertMirroredIngressClass :one
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
RETURNING ` + mirroredIngressClassColumns

// UpsertMirroredIngressClassParams is the bind set for the idempotent
// upsert. last_seen_at and created_at are owned by the DB (the schema
// has DEFAULT now() on insert; the ON CONFLICT branch refreshes
// last_seen_at via the trigger-less SET).
type UpsertMirroredIngressClassParams struct {
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Name        string          `json:"name"`
	Controller  string          `json:"controller"`
	Parameters  json.RawMessage `json:"parameters"`
	IsDefault   bool            `json:"is_default"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
}

func (q *Queries) UpsertMirroredIngressClass(ctx context.Context, arg UpsertMirroredIngressClassParams) (MirroredIngressClass, error) {
	row := q.db.QueryRow(ctx, upsertMirroredIngressClass,
		arg.ClusterID,
		arg.Name,
		arg.Controller,
		arg.Parameters,
		arg.IsDefault,
		arg.Labels,
		arg.Annotations,
	)
	return scanMirroredIngressClassRow(row)
}

const deleteMirroredIngressClass = `-- name: DeleteMirroredIngressClass :exec
DELETE FROM mirrored_ingress_classes WHERE cluster_id = $1 AND name = $2`

// DeleteMirroredIngressClassParams matches the watcher's delete event shape.
type DeleteMirroredIngressClassParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Name      string    `json:"name"`
}

func (q *Queries) DeleteMirroredIngressClass(ctx context.Context, arg DeleteMirroredIngressClassParams) error {
	_, err := q.db.Exec(ctx, deleteMirroredIngressClass, arg.ClusterID, arg.Name)
	return err
}

const pruneStaleMirroredIngressClasses = `-- name: PruneStaleMirroredIngressClasses :execrows
DELETE FROM mirrored_ingress_classes WHERE last_seen_at < $1`

func (q *Queries) PruneStaleMirroredIngressClasses(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneStaleMirroredIngressClasses, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------
// mirrored_gateway_classes
// ---------------------------------------------------------------------

const mirroredGatewayClassColumns = `
    id, cluster_id, name, controller_name, description, parameters,
    accepted_status, labels, annotations, last_seen_at, created_at, updated_at`

func scanMirroredGatewayClassRow(row interface {
	Scan(dest ...any) error
}) (MirroredGatewayClass, error) {
	var i MirroredGatewayClass
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Name,
		&i.ControllerName,
		&i.Description,
		&i.Parameters,
		&i.AcceptedStatus,
		&i.Labels,
		&i.Annotations,
		&i.LastSeenAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMirroredGatewayClasses = `-- name: ListMirroredGatewayClasses :many
SELECT ` + mirroredGatewayClassColumns + `
FROM mirrored_gateway_classes
WHERE cluster_id = $1
ORDER BY name ASC`

func (q *Queries) ListMirroredGatewayClasses(ctx context.Context, clusterID uuid.UUID) ([]MirroredGatewayClass, error) {
	rows, err := q.db.Query(ctx, listMirroredGatewayClasses, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredGatewayClass{}
	for rows.Next() {
		i, err := scanMirroredGatewayClassRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getMirroredGatewayClass = `-- name: GetMirroredGatewayClass :one
SELECT ` + mirroredGatewayClassColumns + `
FROM mirrored_gateway_classes
WHERE cluster_id = $1 AND name = $2`

type GetMirroredGatewayClassParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Name      string    `json:"name"`
}

func (q *Queries) GetMirroredGatewayClass(ctx context.Context, arg GetMirroredGatewayClassParams) (MirroredGatewayClass, error) {
	return scanMirroredGatewayClassRow(q.db.QueryRow(ctx, getMirroredGatewayClass, arg.ClusterID, arg.Name))
}

const upsertMirroredGatewayClass = `-- name: UpsertMirroredGatewayClass :one
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
RETURNING ` + mirroredGatewayClassColumns

type UpsertMirroredGatewayClassParams struct {
	ClusterID      uuid.UUID       `json:"cluster_id"`
	Name           string          `json:"name"`
	ControllerName string          `json:"controller_name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	AcceptedStatus string          `json:"accepted_status"`
	Labels         json.RawMessage `json:"labels"`
	Annotations    json.RawMessage `json:"annotations"`
}

func (q *Queries) UpsertMirroredGatewayClass(ctx context.Context, arg UpsertMirroredGatewayClassParams) (MirroredGatewayClass, error) {
	row := q.db.QueryRow(ctx, upsertMirroredGatewayClass,
		arg.ClusterID,
		arg.Name,
		arg.ControllerName,
		arg.Description,
		arg.Parameters,
		arg.AcceptedStatus,
		arg.Labels,
		arg.Annotations,
	)
	return scanMirroredGatewayClassRow(row)
}

const deleteMirroredGatewayClass = `-- name: DeleteMirroredGatewayClass :exec
DELETE FROM mirrored_gateway_classes WHERE cluster_id = $1 AND name = $2`

type DeleteMirroredGatewayClassParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Name      string    `json:"name"`
}

func (q *Queries) DeleteMirroredGatewayClass(ctx context.Context, arg DeleteMirroredGatewayClassParams) error {
	_, err := q.db.Exec(ctx, deleteMirroredGatewayClass, arg.ClusterID, arg.Name)
	return err
}

const pruneStaleMirroredGatewayClasses = `-- name: PruneStaleMirroredGatewayClasses :execrows
DELETE FROM mirrored_gateway_classes WHERE last_seen_at < $1`

func (q *Queries) PruneStaleMirroredGatewayClasses(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneStaleMirroredGatewayClasses, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------
// mirrored_network_policies
// ---------------------------------------------------------------------

const mirroredNetworkPolicyColumns = `
    id, cluster_id, namespace, name, pod_selector, policy_types,
    ingress_rules, egress_rules, labels, annotations, is_managed,
    last_seen_at, created_at, updated_at`

func scanMirroredNetworkPolicyRow(row interface {
	Scan(dest ...any) error
}) (MirroredNetworkPolicy, error) {
	var i MirroredNetworkPolicy
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Namespace,
		&i.Name,
		&i.PodSelector,
		&i.PolicyTypes,
		&i.IngressRules,
		&i.EgressRules,
		&i.Labels,
		&i.Annotations,
		&i.IsManaged,
		&i.LastSeenAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMirroredNetworkPolicies = `-- name: ListMirroredNetworkPolicies :many
SELECT ` + mirroredNetworkPolicyColumns + `
FROM mirrored_network_policies
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC`

func (q *Queries) ListMirroredNetworkPolicies(ctx context.Context, clusterID uuid.UUID) ([]MirroredNetworkPolicy, error) {
	rows, err := q.db.Query(ctx, listMirroredNetworkPolicies, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredNetworkPolicy{}
	for rows.Next() {
		i, err := scanMirroredNetworkPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listMirroredNetworkPoliciesByNamespace = `-- name: ListMirroredNetworkPoliciesByNamespace :many
SELECT ` + mirroredNetworkPolicyColumns + `
FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC`

type ListMirroredNetworkPoliciesByNamespaceParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
}

func (q *Queries) ListMirroredNetworkPoliciesByNamespace(ctx context.Context, arg ListMirroredNetworkPoliciesByNamespaceParams) ([]MirroredNetworkPolicy, error) {
	rows, err := q.db.Query(ctx, listMirroredNetworkPoliciesByNamespace, arg.ClusterID, arg.Namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredNetworkPolicy{}
	for rows.Next() {
		i, err := scanMirroredNetworkPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getMirroredNetworkPolicy = `-- name: GetMirroredNetworkPolicy :one
SELECT ` + mirroredNetworkPolicyColumns + `
FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type GetMirroredNetworkPolicyParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) GetMirroredNetworkPolicy(ctx context.Context, arg GetMirroredNetworkPolicyParams) (MirroredNetworkPolicy, error) {
	return scanMirroredNetworkPolicyRow(q.db.QueryRow(ctx, getMirroredNetworkPolicy, arg.ClusterID, arg.Namespace, arg.Name))
}

const upsertMirroredNetworkPolicy = `-- name: UpsertMirroredNetworkPolicy :one
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
RETURNING ` + mirroredNetworkPolicyColumns

type UpsertMirroredNetworkPolicyParams struct {
	ClusterID    uuid.UUID       `json:"cluster_id"`
	Namespace    string          `json:"namespace"`
	Name         string          `json:"name"`
	PodSelector  json.RawMessage `json:"pod_selector"`
	PolicyTypes  json.RawMessage `json:"policy_types"`
	IngressRules json.RawMessage `json:"ingress_rules"`
	EgressRules  json.RawMessage `json:"egress_rules"`
	Labels       json.RawMessage `json:"labels"`
	Annotations  json.RawMessage `json:"annotations"`
	IsManaged    bool            `json:"is_managed"`
}

func (q *Queries) UpsertMirroredNetworkPolicy(ctx context.Context, arg UpsertMirroredNetworkPolicyParams) (MirroredNetworkPolicy, error) {
	row := q.db.QueryRow(ctx, upsertMirroredNetworkPolicy,
		arg.ClusterID,
		arg.Namespace,
		arg.Name,
		arg.PodSelector,
		arg.PolicyTypes,
		arg.IngressRules,
		arg.EgressRules,
		arg.Labels,
		arg.Annotations,
		arg.IsManaged,
	)
	return scanMirroredNetworkPolicyRow(row)
}

const deleteMirroredNetworkPolicy = `-- name: DeleteMirroredNetworkPolicy :exec
DELETE FROM mirrored_network_policies
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type DeleteMirroredNetworkPolicyParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) DeleteMirroredNetworkPolicy(ctx context.Context, arg DeleteMirroredNetworkPolicyParams) error {
	_, err := q.db.Exec(ctx, deleteMirroredNetworkPolicy, arg.ClusterID, arg.Namespace, arg.Name)
	return err
}

const pruneStaleMirroredNetworkPolicies = `-- name: PruneStaleMirroredNetworkPolicies :execrows
DELETE FROM mirrored_network_policies WHERE last_seen_at < $1`

func (q *Queries) PruneStaleMirroredNetworkPolicies(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneStaleMirroredNetworkPolicies, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------
// mirrored_resource_quotas
// ---------------------------------------------------------------------

const mirroredResourceQuotaColumns = `
    id, cluster_id, namespace, name, hard, used, scopes,
    labels, annotations, last_seen_at, created_at, updated_at`

func scanMirroredResourceQuotaRow(row interface {
	Scan(dest ...any) error
}) (MirroredResourceQuota, error) {
	var i MirroredResourceQuota
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Namespace,
		&i.Name,
		&i.Hard,
		&i.Used,
		&i.Scopes,
		&i.Labels,
		&i.Annotations,
		&i.LastSeenAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMirroredResourceQuotas = `-- name: ListMirroredResourceQuotas :many
SELECT ` + mirroredResourceQuotaColumns + `
FROM mirrored_resource_quotas
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC`

func (q *Queries) ListMirroredResourceQuotas(ctx context.Context, clusterID uuid.UUID) ([]MirroredResourceQuota, error) {
	rows, err := q.db.Query(ctx, listMirroredResourceQuotas, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredResourceQuota{}
	for rows.Next() {
		i, err := scanMirroredResourceQuotaRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listMirroredResourceQuotasByNamespace = `-- name: ListMirroredResourceQuotasByNamespace :many
SELECT ` + mirroredResourceQuotaColumns + `
FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC`

type ListMirroredResourceQuotasByNamespaceParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
}

func (q *Queries) ListMirroredResourceQuotasByNamespace(ctx context.Context, arg ListMirroredResourceQuotasByNamespaceParams) ([]MirroredResourceQuota, error) {
	rows, err := q.db.Query(ctx, listMirroredResourceQuotasByNamespace, arg.ClusterID, arg.Namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredResourceQuota{}
	for rows.Next() {
		i, err := scanMirroredResourceQuotaRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getMirroredResourceQuota = `-- name: GetMirroredResourceQuota :one
SELECT ` + mirroredResourceQuotaColumns + `
FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type GetMirroredResourceQuotaParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) GetMirroredResourceQuota(ctx context.Context, arg GetMirroredResourceQuotaParams) (MirroredResourceQuota, error) {
	return scanMirroredResourceQuotaRow(q.db.QueryRow(ctx, getMirroredResourceQuota, arg.ClusterID, arg.Namespace, arg.Name))
}

const upsertMirroredResourceQuota = `-- name: UpsertMirroredResourceQuota :one
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
RETURNING ` + mirroredResourceQuotaColumns

type UpsertMirroredResourceQuotaParams struct {
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Namespace   string          `json:"namespace"`
	Name        string          `json:"name"`
	Hard        json.RawMessage `json:"hard"`
	Used        json.RawMessage `json:"used"`
	Scopes      json.RawMessage `json:"scopes"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
}

func (q *Queries) UpsertMirroredResourceQuota(ctx context.Context, arg UpsertMirroredResourceQuotaParams) (MirroredResourceQuota, error) {
	row := q.db.QueryRow(ctx, upsertMirroredResourceQuota,
		arg.ClusterID,
		arg.Namespace,
		arg.Name,
		arg.Hard,
		arg.Used,
		arg.Scopes,
		arg.Labels,
		arg.Annotations,
	)
	return scanMirroredResourceQuotaRow(row)
}

const deleteMirroredResourceQuota = `-- name: DeleteMirroredResourceQuota :exec
DELETE FROM mirrored_resource_quotas
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type DeleteMirroredResourceQuotaParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) DeleteMirroredResourceQuota(ctx context.Context, arg DeleteMirroredResourceQuotaParams) error {
	_, err := q.db.Exec(ctx, deleteMirroredResourceQuota, arg.ClusterID, arg.Namespace, arg.Name)
	return err
}

const pruneStaleMirroredResourceQuotas = `-- name: PruneStaleMirroredResourceQuotas :execrows
DELETE FROM mirrored_resource_quotas WHERE last_seen_at < $1`

func (q *Queries) PruneStaleMirroredResourceQuotas(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneStaleMirroredResourceQuotas, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------
// mirrored_limit_ranges
// ---------------------------------------------------------------------

const mirroredLimitRangeColumns = `
    id, cluster_id, namespace, name, limits,
    labels, annotations, last_seen_at, created_at, updated_at`

func scanMirroredLimitRangeRow(row interface {
	Scan(dest ...any) error
}) (MirroredLimitRange, error) {
	var i MirroredLimitRange
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Namespace,
		&i.Name,
		&i.Limits,
		&i.Labels,
		&i.Annotations,
		&i.LastSeenAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMirroredLimitRanges = `-- name: ListMirroredLimitRanges :many
SELECT ` + mirroredLimitRangeColumns + `
FROM mirrored_limit_ranges
WHERE cluster_id = $1
ORDER BY namespace ASC, name ASC`

func (q *Queries) ListMirroredLimitRanges(ctx context.Context, clusterID uuid.UUID) ([]MirroredLimitRange, error) {
	rows, err := q.db.Query(ctx, listMirroredLimitRanges, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredLimitRange{}
	for rows.Next() {
		i, err := scanMirroredLimitRangeRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listMirroredLimitRangesByNamespace = `-- name: ListMirroredLimitRangesByNamespace :many
SELECT ` + mirroredLimitRangeColumns + `
FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2
ORDER BY name ASC`

type ListMirroredLimitRangesByNamespaceParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
}

func (q *Queries) ListMirroredLimitRangesByNamespace(ctx context.Context, arg ListMirroredLimitRangesByNamespaceParams) ([]MirroredLimitRange, error) {
	rows, err := q.db.Query(ctx, listMirroredLimitRangesByNamespace, arg.ClusterID, arg.Namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MirroredLimitRange{}
	for rows.Next() {
		i, err := scanMirroredLimitRangeRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getMirroredLimitRange = `-- name: GetMirroredLimitRange :one
SELECT ` + mirroredLimitRangeColumns + `
FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type GetMirroredLimitRangeParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) GetMirroredLimitRange(ctx context.Context, arg GetMirroredLimitRangeParams) (MirroredLimitRange, error) {
	return scanMirroredLimitRangeRow(q.db.QueryRow(ctx, getMirroredLimitRange, arg.ClusterID, arg.Namespace, arg.Name))
}

const upsertMirroredLimitRange = `-- name: UpsertMirroredLimitRange :one
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
RETURNING ` + mirroredLimitRangeColumns

type UpsertMirroredLimitRangeParams struct {
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Namespace   string          `json:"namespace"`
	Name        string          `json:"name"`
	Limits      json.RawMessage `json:"limits"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
}

func (q *Queries) UpsertMirroredLimitRange(ctx context.Context, arg UpsertMirroredLimitRangeParams) (MirroredLimitRange, error) {
	row := q.db.QueryRow(ctx, upsertMirroredLimitRange,
		arg.ClusterID,
		arg.Namespace,
		arg.Name,
		arg.Limits,
		arg.Labels,
		arg.Annotations,
	)
	return scanMirroredLimitRangeRow(row)
}

const deleteMirroredLimitRange = `-- name: DeleteMirroredLimitRange :exec
DELETE FROM mirrored_limit_ranges
WHERE cluster_id = $1 AND namespace = $2 AND name = $3`

type DeleteMirroredLimitRangeParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
}

func (q *Queries) DeleteMirroredLimitRange(ctx context.Context, arg DeleteMirroredLimitRangeParams) error {
	_, err := q.db.Exec(ctx, deleteMirroredLimitRange, arg.ClusterID, arg.Namespace, arg.Name)
	return err
}

const pruneStaleMirroredLimitRanges = `-- name: PruneStaleMirroredLimitRanges :execrows
DELETE FROM mirrored_limit_ranges WHERE last_seen_at < $1`

func (q *Queries) PruneStaleMirroredLimitRanges(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneStaleMirroredLimitRanges, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
