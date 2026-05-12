// Migration 066 — cluster groups CRUD + tree expansion.
//
// Hand-authored sqlc shim. See cloud_credentials.sql.go for the
// rationale (sqlc CLI not runnable in agent worktrees); the contents
// below mirror what the sqlc generator would emit for the queries in
// internal/db/queries/cluster_groups.sql.

package sqlc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ClusterGroup is the row shape for cluster_groups.
type ClusterGroup struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	ParentID    pgtype.UUID `json:"parent_id"`
	Color       string      `json:"color"`
	Icon        string      `json:"icon"`
	Enabled     bool        `json:"enabled"`
	CreatedBy   pgtype.UUID `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// ClusterGroupTreeRow is the depth-annotated row returned by
// ListClusterGroupsAsTree — same shape as ClusterGroup plus a Depth
// column computed from the recursive CTE (0 for top-level rows).
type ClusterGroupTreeRow struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	ParentID    pgtype.UUID `json:"parent_id"`
	Color       string      `json:"color"`
	Icon        string      `json:"icon"`
	Enabled     bool        `json:"enabled"`
	CreatedBy   pgtype.UUID `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Depth       int32       `json:"depth"`
}

// ClusterInGroupRow is the minimal cluster surface returned by
// ListClustersInGroupTree — only id + name because the recursive CTE is
// hot-pathed and callers join back to clusters for richer columns when
// they need them.
type ClusterInGroupRow struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

const clusterGroupColumns = `id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at`

func scanClusterGroupRow(row interface {
	Scan(dest ...any) error
}) (ClusterGroup, error) {
	var g ClusterGroup
	err := row.Scan(
		&g.ID,
		&g.Name,
		&g.Slug,
		&g.Description,
		&g.ParentID,
		&g.Color,
		&g.Icon,
		&g.Enabled,
		&g.CreatedBy,
		&g.CreatedAt,
		&g.UpdatedAt,
	)
	return g, err
}

const listClusterGroups = `-- name: ListClusterGroups :many
SELECT ` + clusterGroupColumns + `
FROM cluster_groups
WHERE enabled = true
ORDER BY name ASC`

func (q *Queries) ListClusterGroups(ctx context.Context) ([]ClusterGroup, error) {
	rows, err := q.db.Query(ctx, listClusterGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterGroup{}
	for rows.Next() {
		i, err := scanClusterGroupRow(rows)
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

const getClusterGroupByID = `-- name: GetClusterGroupByID :one
SELECT ` + clusterGroupColumns + `
FROM cluster_groups WHERE id = $1`

func (q *Queries) GetClusterGroupByID(ctx context.Context, id uuid.UUID) (ClusterGroup, error) {
	row := q.db.QueryRow(ctx, getClusterGroupByID, id)
	return scanClusterGroupRow(row)
}

const createClusterGroup = `-- name: CreateClusterGroup :one
INSERT INTO cluster_groups (name, slug, description, parent_id, color, icon, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + clusterGroupColumns

type CreateClusterGroupParams struct {
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	ParentID    pgtype.UUID `json:"parent_id"`
	Color       string      `json:"color"`
	Icon        string      `json:"icon"`
	CreatedBy   pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateClusterGroup(ctx context.Context, arg CreateClusterGroupParams) (ClusterGroup, error) {
	row := q.db.QueryRow(ctx, createClusterGroup,
		arg.Name,
		arg.Slug,
		arg.Description,
		arg.ParentID,
		arg.Color,
		arg.Icon,
		arg.CreatedBy,
	)
	return scanClusterGroupRow(row)
}

const updateClusterGroup = `-- name: UpdateClusterGroup :one
UPDATE cluster_groups
SET name        = $2,
    slug        = $3,
    description = $4,
    parent_id   = $5,
    color       = $6,
    icon        = $7,
    updated_at  = now()
WHERE id = $1
RETURNING ` + clusterGroupColumns

type UpdateClusterGroupParams struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	ParentID    pgtype.UUID `json:"parent_id"`
	Color       string      `json:"color"`
	Icon        string      `json:"icon"`
}

func (q *Queries) UpdateClusterGroup(ctx context.Context, arg UpdateClusterGroupParams) (ClusterGroup, error) {
	row := q.db.QueryRow(ctx, updateClusterGroup,
		arg.ID,
		arg.Name,
		arg.Slug,
		arg.Description,
		arg.ParentID,
		arg.Color,
		arg.Icon,
	)
	return scanClusterGroupRow(row)
}

const deleteClusterGroup = `-- name: DeleteClusterGroup :exec
DELETE FROM cluster_groups WHERE id = $1`

func (q *Queries) DeleteClusterGroup(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteClusterGroup, id)
	return err
}

const listClusterGroupsAsTree = `-- name: ListClusterGroupsAsTree :many
WITH RECURSIVE tree AS (
    SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at, 0 AS depth
    FROM cluster_groups
    WHERE parent_id IS NULL AND enabled = true
    UNION ALL
    SELECT c.id, c.name, c.slug, c.description, c.parent_id, c.color, c.icon, c.enabled, c.created_by, c.created_at, c.updated_at, t.depth + 1
    FROM cluster_groups c
    INNER JOIN tree t ON c.parent_id = t.id
    WHERE c.enabled = true
)
SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at, depth FROM tree
ORDER BY depth, name`

func (q *Queries) ListClusterGroupsAsTree(ctx context.Context) ([]ClusterGroupTreeRow, error) {
	rows, err := q.db.Query(ctx, listClusterGroupsAsTree)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterGroupTreeRow{}
	for rows.Next() {
		var r ClusterGroupTreeRow
		if err := rows.Scan(
			&r.ID,
			&r.Name,
			&r.Slug,
			&r.Description,
			&r.ParentID,
			&r.Color,
			&r.Icon,
			&r.Enabled,
			&r.CreatedBy,
			&r.CreatedAt,
			&r.UpdatedAt,
			&r.Depth,
		); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listClustersInGroupTree = `-- name: ListClustersInGroupTree :many
WITH RECURSIVE subtree AS (
    SELECT id FROM cluster_groups WHERE id = $1 AND enabled = true
    UNION ALL
    SELECT c.id FROM cluster_groups c
    INNER JOIN subtree s ON c.parent_id = s.id
    WHERE c.enabled = true
)
SELECT cl.id, cl.name FROM clusters cl
INNER JOIN subtree s ON cl.group_id = s.id
ORDER BY cl.name ASC`

func (q *Queries) ListClustersInGroupTree(ctx context.Context, rootID uuid.UUID) ([]ClusterInGroupRow, error) {
	rows, err := q.db.Query(ctx, listClustersInGroupTree, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterInGroupRow{}
	for rows.Next() {
		var r ClusterInGroupRow
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countClustersInGroup = `-- name: CountClustersInGroup :one
SELECT COUNT(*)::bigint FROM clusters WHERE group_id = $1`

func (q *Queries) CountClustersInGroup(ctx context.Context, groupID uuid.UUID) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, countClustersInGroup, groupID).Scan(&n)
	return n, err
}

const countClustersInGroupTree = `-- name: CountClustersInGroupTree :one
WITH RECURSIVE subtree AS (
    SELECT id FROM cluster_groups WHERE id = $1 AND enabled = true
    UNION ALL
    SELECT c.id FROM cluster_groups c
    INNER JOIN subtree s ON c.parent_id = s.id
    WHERE c.enabled = true
)
SELECT COUNT(*)::bigint FROM clusters cl WHERE cl.group_id IN (SELECT id FROM subtree)`

func (q *Queries) CountClustersInGroupTree(ctx context.Context, groupID uuid.UUID) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, countClustersInGroupTree, groupID).Scan(&n)
	return n, err
}

const assignClusterGroup = `-- name: AssignClusterGroup :exec
UPDATE clusters SET group_id = $2, updated_at = now() WHERE id = $1`

type AssignClusterGroupParams struct {
	ClusterID uuid.UUID   `json:"cluster_id"`
	GroupID   pgtype.UUID `json:"group_id"`
}

func (q *Queries) AssignClusterGroup(ctx context.Context, arg AssignClusterGroupParams) error {
	_, err := q.db.Exec(ctx, assignClusterGroup, arg.ClusterID, arg.GroupID)
	return err
}

const unassignClusterGroup = `-- name: UnassignClusterGroup :exec
UPDATE clusters SET group_id = NULL, updated_at = now() WHERE id = $1`

func (q *Queries) UnassignClusterGroup(ctx context.Context, clusterID uuid.UUID) error {
	_, err := q.db.Exec(ctx, unassignClusterGroup, clusterID)
	return err
}

const getClusterGroupForCluster = `-- name: GetClusterGroupForCluster :one
SELECT group_id FROM clusters WHERE id = $1`

func (q *Queries) GetClusterGroupForCluster(ctx context.Context, clusterID uuid.UUID) (pgtype.UUID, error) {
	var g pgtype.UUID
	err := q.db.QueryRow(ctx, getClusterGroupForCluster, clusterID).Scan(&g)
	return g, err
}

const countEnabledClusterGroups = `-- name: CountEnabledClusterGroups :one
SELECT COUNT(*)::bigint FROM cluster_groups WHERE enabled = true`

func (q *Queries) CountEnabledClusterGroups(ctx context.Context) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, countEnabledClusterGroups).Scan(&n)
	return n, err
}
