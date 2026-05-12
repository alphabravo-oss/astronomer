// Migration 068 — network policy templates + applications, hand-authored
// sqlc shim. Mirrors what `sqlc generate` would emit for
// queries/network_policies.sql; kept outside the canonical models.go /
// *.sql.go output targets so a future regeneration run doesn't clobber.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----------------------------------------------------------------------
// network_policy_templates
// ----------------------------------------------------------------------

const networkPolicyTemplateSelectColumns = `
    id, slug, name, description, kind, spec_template, enabled,
    created_by, created_at, updated_at`

func scanNetworkPolicyTemplate(row interface{ Scan(dest ...any) error }, t *NetworkPolicyTemplate) error {
	return row.Scan(
		&t.ID,
		&t.Slug,
		&t.Name,
		&t.Description,
		&t.Kind,
		&t.SpecTemplate,
		&t.Enabled,
		&t.CreatedBy,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
}

const listNetworkPolicyTemplates = `-- name: ListNetworkPolicyTemplates :many
SELECT ` + networkPolicyTemplateSelectColumns + `
FROM network_policy_templates
ORDER BY kind DESC, name
LIMIT $1 OFFSET $2`

type ListNetworkPolicyTemplatesParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

func (q *Queries) ListNetworkPolicyTemplates(ctx context.Context, arg ListNetworkPolicyTemplatesParams) ([]NetworkPolicyTemplate, error) {
	rows, err := q.db.Query(ctx, listNetworkPolicyTemplates, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyTemplate{}
	for rows.Next() {
		var t NetworkPolicyTemplate
		if err := scanNetworkPolicyTemplate(rows, &t); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

const countNetworkPolicyTemplates = `-- name: CountNetworkPolicyTemplates :one
SELECT count(*) FROM network_policy_templates`

func (q *Queries) CountNetworkPolicyTemplates(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, countNetworkPolicyTemplates)
	var n int64
	err := row.Scan(&n)
	return n, err
}

const getNetworkPolicyTemplateByID = `-- name: GetNetworkPolicyTemplateByID :one
SELECT ` + networkPolicyTemplateSelectColumns + `
FROM network_policy_templates WHERE id = $1`

func (q *Queries) GetNetworkPolicyTemplateByID(ctx context.Context, id uuid.UUID) (NetworkPolicyTemplate, error) {
	row := q.db.QueryRow(ctx, getNetworkPolicyTemplateByID, id)
	var t NetworkPolicyTemplate
	err := scanNetworkPolicyTemplate(row, &t)
	return t, err
}

const getNetworkPolicyTemplateBySlug = `-- name: GetNetworkPolicyTemplateBySlug :one
SELECT ` + networkPolicyTemplateSelectColumns + `
FROM network_policy_templates WHERE slug = $1`

func (q *Queries) GetNetworkPolicyTemplateBySlug(ctx context.Context, slug string) (NetworkPolicyTemplate, error) {
	row := q.db.QueryRow(ctx, getNetworkPolicyTemplateBySlug, slug)
	var t NetworkPolicyTemplate
	err := scanNetworkPolicyTemplate(row, &t)
	return t, err
}

const createNetworkPolicyTemplate = `-- name: CreateNetworkPolicyTemplate :one
INSERT INTO network_policy_templates
    (slug, name, description, kind, spec_template, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + networkPolicyTemplateSelectColumns

type CreateNetworkPolicyTemplateParams struct {
	Slug         string      `json:"slug"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Kind         string      `json:"kind"`
	SpecTemplate string      `json:"spec_template"`
	Enabled      bool        `json:"enabled"`
	CreatedBy    pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateNetworkPolicyTemplate(ctx context.Context, arg CreateNetworkPolicyTemplateParams) (NetworkPolicyTemplate, error) {
	row := q.db.QueryRow(ctx, createNetworkPolicyTemplate,
		arg.Slug, arg.Name, arg.Description, arg.Kind, arg.SpecTemplate, arg.Enabled, arg.CreatedBy,
	)
	var t NetworkPolicyTemplate
	err := scanNetworkPolicyTemplate(row, &t)
	return t, err
}

const updateNetworkPolicyTemplate = `-- name: UpdateNetworkPolicyTemplate :one
UPDATE network_policy_templates SET
    name           = $2,
    description    = $3,
    spec_template  = $4,
    enabled        = $5,
    updated_at     = now()
WHERE id = $1
RETURNING ` + networkPolicyTemplateSelectColumns

type UpdateNetworkPolicyTemplateParams struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SpecTemplate string    `json:"spec_template"`
	Enabled      bool      `json:"enabled"`
}

func (q *Queries) UpdateNetworkPolicyTemplate(ctx context.Context, arg UpdateNetworkPolicyTemplateParams) (NetworkPolicyTemplate, error) {
	row := q.db.QueryRow(ctx, updateNetworkPolicyTemplate,
		arg.ID, arg.Name, arg.Description, arg.SpecTemplate, arg.Enabled,
	)
	var t NetworkPolicyTemplate
	err := scanNetworkPolicyTemplate(row, &t)
	return t, err
}

const deleteNetworkPolicyTemplate = `-- name: DeleteNetworkPolicyTemplate :exec
DELETE FROM network_policy_templates WHERE id = $1`

func (q *Queries) DeleteNetworkPolicyTemplate(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteNetworkPolicyTemplate, id)
	return err
}

// ----------------------------------------------------------------------
// network_policy_applications
// ----------------------------------------------------------------------

const networkPolicyApplicationSelectColumns = `
    id, template_id, cluster_id, namespace, policy_name, status,
    last_applied_at, last_error, applied_by, created_at, updated_at`

func scanNetworkPolicyApplication(row interface{ Scan(dest ...any) error }, a *NetworkPolicyApplication) error {
	return row.Scan(
		&a.ID,
		&a.TemplateID,
		&a.ClusterID,
		&a.Namespace,
		&a.PolicyName,
		&a.Status,
		&a.LastAppliedAt,
		&a.LastError,
		&a.AppliedBy,
		&a.CreatedAt,
		&a.UpdatedAt,
	)
}

const listNetworkPolicyApplications = `-- name: ListNetworkPolicyApplications :many
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
ORDER BY updated_at DESC
LIMIT $1 OFFSET $2`

type ListNetworkPolicyApplicationsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

func (q *Queries) ListNetworkPolicyApplications(ctx context.Context, arg ListNetworkPolicyApplicationsParams) ([]NetworkPolicyApplication, error) {
	rows, err := q.db.Query(ctx, listNetworkPolicyApplications, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplication{}
	for rows.Next() {
		var a NetworkPolicyApplication
		if err := scanNetworkPolicyApplication(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const listApplicationsForCluster = `-- name: ListApplicationsForCluster :many
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
WHERE cluster_id = $1
ORDER BY updated_at DESC`

func (q *Queries) ListApplicationsForCluster(ctx context.Context, clusterID uuid.UUID) ([]NetworkPolicyApplication, error) {
	rows, err := q.db.Query(ctx, listApplicationsForCluster, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplication{}
	for rows.Next() {
		var a NetworkPolicyApplication
		if err := scanNetworkPolicyApplication(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const listApplicationsForTemplate = `-- name: ListApplicationsForTemplate :many
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
WHERE template_id = $1
ORDER BY updated_at DESC`

func (q *Queries) ListApplicationsForTemplate(ctx context.Context, templateID uuid.UUID) ([]NetworkPolicyApplication, error) {
	rows, err := q.db.Query(ctx, listApplicationsForTemplate, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplication{}
	for rows.Next() {
		var a NetworkPolicyApplication
		if err := scanNetworkPolicyApplication(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const listPendingNetworkPolicyApplications = `-- name: ListPendingNetworkPolicyApplications :many
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
WHERE status IN ('pending', 'failed', 'drifting')
ORDER BY updated_at ASC
LIMIT $1`

func (q *Queries) ListPendingNetworkPolicyApplications(ctx context.Context, limit int32) ([]NetworkPolicyApplication, error) {
	rows, err := q.db.Query(ctx, listPendingNetworkPolicyApplications, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplication{}
	for rows.Next() {
		var a NetworkPolicyApplication
		if err := scanNetworkPolicyApplication(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const listAppliedNetworkPolicyApplications = `-- name: ListAppliedNetworkPolicyApplications :many
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
WHERE status = 'applied'
ORDER BY updated_at ASC
LIMIT $1`

func (q *Queries) ListAppliedNetworkPolicyApplications(ctx context.Context, limit int32) ([]NetworkPolicyApplication, error) {
	rows, err := q.db.Query(ctx, listAppliedNetworkPolicyApplications, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplication{}
	for rows.Next() {
		var a NetworkPolicyApplication
		if err := scanNetworkPolicyApplication(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const getNetworkPolicyApplicationByID = `-- name: GetNetworkPolicyApplicationByID :one
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications WHERE id = $1`

func (q *Queries) GetNetworkPolicyApplicationByID(ctx context.Context, id uuid.UUID) (NetworkPolicyApplication, error) {
	row := q.db.QueryRow(ctx, getNetworkPolicyApplicationByID, id)
	var a NetworkPolicyApplication
	err := scanNetworkPolicyApplication(row, &a)
	return a, err
}

const getNetworkPolicyApplicationByUnique = `-- name: GetNetworkPolicyApplicationByUnique :one
SELECT ` + networkPolicyApplicationSelectColumns + `
FROM network_policy_applications
WHERE cluster_id = $1 AND namespace = $2 AND template_id = $3`

type GetNetworkPolicyApplicationByUniqueParams struct {
	ClusterID  uuid.UUID `json:"cluster_id"`
	Namespace  string    `json:"namespace"`
	TemplateID uuid.UUID `json:"template_id"`
}

func (q *Queries) GetNetworkPolicyApplicationByUnique(ctx context.Context, arg GetNetworkPolicyApplicationByUniqueParams) (NetworkPolicyApplication, error) {
	row := q.db.QueryRow(ctx, getNetworkPolicyApplicationByUnique, arg.ClusterID, arg.Namespace, arg.TemplateID)
	var a NetworkPolicyApplication
	err := scanNetworkPolicyApplication(row, &a)
	return a, err
}

const createNetworkPolicyApplication = `-- name: CreateNetworkPolicyApplication :one
INSERT INTO network_policy_applications
    (template_id, cluster_id, namespace, policy_name, status, applied_by)
VALUES ($1, $2, $3, $4, 'pending', $5)
RETURNING ` + networkPolicyApplicationSelectColumns

type CreateNetworkPolicyApplicationParams struct {
	TemplateID uuid.UUID   `json:"template_id"`
	ClusterID  uuid.UUID   `json:"cluster_id"`
	Namespace  string      `json:"namespace"`
	PolicyName string      `json:"policy_name"`
	AppliedBy  pgtype.UUID `json:"applied_by"`
}

func (q *Queries) CreateNetworkPolicyApplication(ctx context.Context, arg CreateNetworkPolicyApplicationParams) (NetworkPolicyApplication, error) {
	row := q.db.QueryRow(ctx, createNetworkPolicyApplication,
		arg.TemplateID, arg.ClusterID, arg.Namespace, arg.PolicyName, arg.AppliedBy,
	)
	var a NetworkPolicyApplication
	err := scanNetworkPolicyApplication(row, &a)
	return a, err
}

const deleteNetworkPolicyApplication = `-- name: DeleteNetworkPolicyApplication :exec
DELETE FROM network_policy_applications WHERE id = $1`

func (q *Queries) DeleteNetworkPolicyApplication(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteNetworkPolicyApplication, id)
	return err
}

const markNetworkPolicyApplicationStatus = `-- name: MarkNetworkPolicyApplicationStatus :one
UPDATE network_policy_applications SET
    status          = $2,
    last_error      = $3,
    last_applied_at = CASE WHEN $4::bool THEN now() ELSE last_applied_at END,
    updated_at      = now()
WHERE id = $1
RETURNING ` + networkPolicyApplicationSelectColumns

type MarkNetworkPolicyApplicationStatusParams struct {
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	LastError    string    `json:"last_error"`
	TouchApplied bool      `json:"touch_applied"`
}

func (q *Queries) MarkNetworkPolicyApplicationStatus(ctx context.Context, arg MarkNetworkPolicyApplicationStatusParams) (NetworkPolicyApplication, error) {
	row := q.db.QueryRow(ctx, markNetworkPolicyApplicationStatus,
		arg.ID, arg.Status, arg.LastError, arg.TouchApplied,
	)
	var a NetworkPolicyApplication
	err := scanNetworkPolicyApplication(row, &a)
	return a, err
}

// NetworkPolicyApplicationStatusCount is the row shape returned by
// CountNetworkPolicyApplicationsByStatus — one count per (cluster,
// status) bucket. Used to populate the per-cluster status gauge.
type NetworkPolicyApplicationStatusCount struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Status    string    `json:"status"`
	Total     int64     `json:"total"`
}

const countNetworkPolicyApplicationsByStatus = `-- name: CountNetworkPolicyApplicationsByStatus :many
SELECT cluster_id, status, count(*)::bigint AS total
FROM network_policy_applications
GROUP BY cluster_id, status`

func (q *Queries) CountNetworkPolicyApplicationsByStatus(ctx context.Context) ([]NetworkPolicyApplicationStatusCount, error) {
	rows, err := q.db.Query(ctx, countNetworkPolicyApplicationsByStatus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NetworkPolicyApplicationStatusCount{}
	for rows.Next() {
		var c NetworkPolicyApplicationStatusCount
		if err := rows.Scan(&c.ClusterID, &c.Status, &c.Total); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}
