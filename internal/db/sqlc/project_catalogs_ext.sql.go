// Migration 061 — per-project Helm catalog ("BYO catalogs") sqlc shim.
//
// Mirrors what `sqlc generate` would emit for queries/project_catalogs.sql.
// The repo's sqlc CLI is broken locally, so this file is hand-authored and
// kept outside the canonical models.go / catalog.sql.go regeneration targets
// to prevent a future codegen run from clobbering it. The
// _ext.sql.go suffix matches the pattern established by
// cluster_registry_configs_ext.sql.go (migration 050) and
// cluster_snapshots_ext.sql.go (migration 052).
//
// The companion ProjectCatalogSubscription model lives below. We deliberately
// do NOT extend the auto-generated HelmRepository struct with owner_project_id
// because the production sqlc.HelmRepository scans came in via the existing
// catalog.sql.go SELECT lists (which don't include the new column).
// Instead, every new query that needs the column scans into the local
// HelmRepositoryWithOwner type — a thin parallel struct — and the handler
// surface flattens it for the wire response.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// HelmRepositoryWithOwner mirrors HelmRepository plus the owner_project_id
// column added in migration 061. Used by every project-scoped read path so
// callers can tell "global / owned / subscribed" apart.
type HelmRepositoryWithOwner struct {
	ID             uuid.UUID          `json:"id"`
	Name           string             `json:"name"`
	Url            string             `json:"url"`
	RepoType       string             `json:"repo_type"`
	Description    string             `json:"description"`
	IsDefault      bool               `json:"is_default"`
	AuthType       string             `json:"auth_type"`
	AuthConfig     json.RawMessage    `json:"auth_config"`
	Enabled        bool               `json:"enabled"`
	LastSyncedAt   pgtype.Timestamptz `json:"last_synced_at"`
	CreatedByID    pgtype.UUID        `json:"created_by_id"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	OwnerProjectID pgtype.UUID        `json:"owner_project_id"`
}

// ProjectCatalogSubscription is one row of project_catalog_subscriptions.
type ProjectCatalogSubscription struct {
	ID        uuid.UUID   `json:"id"`
	ProjectID uuid.UUID   `json:"project_id"`
	CatalogID uuid.UUID   `json:"catalog_id"`
	CreatedBy pgtype.UUID `json:"created_by"`
	CreatedAt time.Time   `json:"created_at"`
}

// ---------------------------------------------------------------------------
// helm_repositories — owner-aware reads
// ---------------------------------------------------------------------------

const helmRepoWithOwnerSelectColumns = `
    id, name, url, repo_type, description, is_default, auth_type,
    auth_config, enabled, last_synced_at, created_by_id, created_at,
    updated_at, owner_project_id`

func scanHelmRepositoryWithOwnerRow(row interface {
	Scan(dest ...any) error
}) (HelmRepositoryWithOwner, error) {
	var i HelmRepositoryWithOwner
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Url,
		&i.RepoType,
		&i.Description,
		&i.IsDefault,
		&i.AuthType,
		&i.AuthConfig,
		&i.Enabled,
		&i.LastSyncedAt,
		&i.CreatedByID,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.OwnerProjectID,
	)
	return i, err
}

const listCatalogsForProject = `-- name: ListCatalogsForProject :many
SELECT ` + helmRepoWithOwnerSelectColumns + `
FROM helm_repositories
WHERE owner_project_id IS NULL
   OR owner_project_id = $1
   OR id IN (
        SELECT catalog_id FROM project_catalog_subscriptions
        WHERE project_id = $1
   )
ORDER BY name ASC`

// ListCatalogsForProject returns the union of globals, project-owned, and
// subscribed catalogs visible to a given project. Hot path for the
// project-scoped catalog browse.
func (q *Queries) ListCatalogsForProject(ctx context.Context, projectID uuid.UUID) ([]HelmRepositoryWithOwner, error) {
	rows, err := q.db.Query(ctx, listCatalogsForProject, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HelmRepositoryWithOwner{}
	for rows.Next() {
		i, err := scanHelmRepositoryWithOwnerRow(rows)
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

const listProjectOwnedCatalogs = `-- name: ListProjectOwnedCatalogs :many
SELECT ` + helmRepoWithOwnerSelectColumns + `
FROM helm_repositories
WHERE owner_project_id = $1
ORDER BY name ASC`

func (q *Queries) ListProjectOwnedCatalogs(ctx context.Context, projectID uuid.UUID) ([]HelmRepositoryWithOwner, error) {
	rows, err := q.db.Query(ctx, listProjectOwnedCatalogs, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HelmRepositoryWithOwner{}
	for rows.Next() {
		i, err := scanHelmRepositoryWithOwnerRow(rows)
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

const getHelmRepositoryWithOwner = `-- name: GetHelmRepositoryWithOwner :one
SELECT ` + helmRepoWithOwnerSelectColumns + `
FROM helm_repositories
WHERE id = $1`

func (q *Queries) GetHelmRepositoryWithOwner(ctx context.Context, id uuid.UUID) (HelmRepositoryWithOwner, error) {
	return scanHelmRepositoryWithOwnerRow(q.db.QueryRow(ctx, getHelmRepositoryWithOwner, id))
}

const createProjectOwnedCatalog = `-- name: CreateProjectOwnedCatalog :one
INSERT INTO helm_repositories (
    name, url, repo_type, description, is_default, auth_type,
    auth_config, enabled, created_by_id, owner_project_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING ` + helmRepoWithOwnerSelectColumns

// CreateProjectOwnedCatalogParams binds the INSERT for a project-owned
// catalog. The owner_project_id is required (this query is for the
// private/BYO path only; the existing CreateHelmRepository remains the
// global / admin path).
type CreateProjectOwnedCatalogParams struct {
	Name           string          `json:"name"`
	Url            string          `json:"url"`
	RepoType       string          `json:"repo_type"`
	Description    string          `json:"description"`
	IsDefault      bool            `json:"is_default"`
	AuthType       string          `json:"auth_type"`
	AuthConfig     json.RawMessage `json:"auth_config"`
	Enabled        bool            `json:"enabled"`
	CreatedByID    pgtype.UUID     `json:"created_by_id"`
	OwnerProjectID pgtype.UUID     `json:"owner_project_id"`
}

func (q *Queries) CreateProjectOwnedCatalog(ctx context.Context, arg CreateProjectOwnedCatalogParams) (HelmRepositoryWithOwner, error) {
	row := q.db.QueryRow(ctx, createProjectOwnedCatalog,
		arg.Name,
		arg.Url,
		arg.RepoType,
		arg.Description,
		arg.IsDefault,
		arg.AuthType,
		arg.AuthConfig,
		arg.Enabled,
		arg.CreatedByID,
		arg.OwnerProjectID,
	)
	return scanHelmRepositoryWithOwnerRow(row)
}

const listAdminCatalogsIncludingProjectOwned = `-- name: ListAdminCatalogsIncludingProjectOwned :many
SELECT ` + helmRepoWithOwnerSelectColumns + `
FROM helm_repositories
ORDER BY created_at DESC
LIMIT $1 OFFSET $2`

// ListAdminCatalogsIncludingProjectOwnedParams binds the admin all-catalogs
// listing (used when ?include_project_owned=true). Mirrors
// ListHelmRepositoriesParams from the auto-gen output.
type ListAdminCatalogsIncludingProjectOwnedParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

func (q *Queries) ListAdminCatalogsIncludingProjectOwned(ctx context.Context, arg ListAdminCatalogsIncludingProjectOwnedParams) ([]HelmRepositoryWithOwner, error) {
	rows, err := q.db.Query(ctx, listAdminCatalogsIncludingProjectOwned, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HelmRepositoryWithOwner{}
	for rows.Next() {
		i, err := scanHelmRepositoryWithOwnerRow(rows)
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

// ---------------------------------------------------------------------------
// project_catalog_subscriptions
// ---------------------------------------------------------------------------

const projectCatalogSubSelectColumns = `id, project_id, catalog_id, created_by, created_at`

func scanProjectCatalogSubscriptionRow(row interface {
	Scan(dest ...any) error
}) (ProjectCatalogSubscription, error) {
	var i ProjectCatalogSubscription
	err := row.Scan(&i.ID, &i.ProjectID, &i.CatalogID, &i.CreatedBy, &i.CreatedAt)
	return i, err
}

const listProjectSubscriptions = `-- name: ListProjectSubscriptions :many
SELECT ` + projectCatalogSubSelectColumns + `
FROM project_catalog_subscriptions
WHERE project_id = $1
ORDER BY created_at ASC`

func (q *Queries) ListProjectSubscriptions(ctx context.Context, projectID uuid.UUID) ([]ProjectCatalogSubscription, error) {
	rows, err := q.db.Query(ctx, listProjectSubscriptions, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProjectCatalogSubscription{}
	for rows.Next() {
		i, err := scanProjectCatalogSubscriptionRow(rows)
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

const createProjectCatalogSubscription = `-- name: CreateProjectCatalogSubscription :one
INSERT INTO project_catalog_subscriptions (project_id, catalog_id, created_by)
VALUES ($1, $2, $3)
RETURNING ` + projectCatalogSubSelectColumns

// CreateProjectCatalogSubscriptionParams binds the subscription insert.
type CreateProjectCatalogSubscriptionParams struct {
	ProjectID uuid.UUID   `json:"project_id"`
	CatalogID uuid.UUID   `json:"catalog_id"`
	CreatedBy pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateProjectCatalogSubscription(ctx context.Context, arg CreateProjectCatalogSubscriptionParams) (ProjectCatalogSubscription, error) {
	row := q.db.QueryRow(ctx, createProjectCatalogSubscription,
		arg.ProjectID,
		arg.CatalogID,
		arg.CreatedBy,
	)
	return scanProjectCatalogSubscriptionRow(row)
}

const deleteProjectCatalogSubscription = `-- name: DeleteProjectCatalogSubscription :exec
DELETE FROM project_catalog_subscriptions
WHERE project_id = $1 AND catalog_id = $2`

// DeleteProjectCatalogSubscriptionParams binds the unsubscribe.
type DeleteProjectCatalogSubscriptionParams struct {
	ProjectID uuid.UUID `json:"project_id"`
	CatalogID uuid.UUID `json:"catalog_id"`
}

func (q *Queries) DeleteProjectCatalogSubscription(ctx context.Context, arg DeleteProjectCatalogSubscriptionParams) error {
	_, err := q.db.Exec(ctx, deleteProjectCatalogSubscription, arg.ProjectID, arg.CatalogID)
	return err
}

const getProjectCatalogSubscription = `-- name: GetProjectCatalogSubscription :one
SELECT ` + projectCatalogSubSelectColumns + `
FROM project_catalog_subscriptions
WHERE project_id = $1 AND catalog_id = $2`

// GetProjectCatalogSubscriptionParams binds the existence-check query.
type GetProjectCatalogSubscriptionParams struct {
	ProjectID uuid.UUID `json:"project_id"`
	CatalogID uuid.UUID `json:"catalog_id"`
}

func (q *Queries) GetProjectCatalogSubscription(ctx context.Context, arg GetProjectCatalogSubscriptionParams) (ProjectCatalogSubscription, error) {
	return scanProjectCatalogSubscriptionRow(q.db.QueryRow(ctx, getProjectCatalogSubscription, arg.ProjectID, arg.CatalogID))
}

const countSubscriptionsByCatalog = `-- name: CountSubscriptionsByCatalog :one
SELECT count(*) FROM project_catalog_subscriptions WHERE catalog_id = $1`

func (q *Queries) CountSubscriptionsByCatalog(ctx context.Context, catalogID uuid.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countSubscriptionsByCatalog, catalogID)
	var n int64
	err := row.Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Visibility helper
// ---------------------------------------------------------------------------

// CatalogVisibility classifies a (project, catalog) pair for the access
// check. The handler decides 200 vs 403 vs 404 based on this.
type CatalogVisibility string

const (
	// CatalogVisibilityOwn = the project owns this catalog (private).
	CatalogVisibilityOwn CatalogVisibility = "own"
	// CatalogVisibilitySubscribedPublic = the project is subscribed to a
	// global catalog (or has a row in project_catalog_subscriptions for
	// it, regardless of public/private — superuser-curation case).
	CatalogVisibilitySubscribedPublic CatalogVisibility = "subscribed_public"
	// CatalogVisibilityPublic = global catalog the project hasn't yet
	// explicitly subscribed to; still readable in the browse view
	// because globals are universally visible.
	CatalogVisibilityPublic CatalogVisibility = "public"
	// CatalogVisibilityForeignPrivate = catalog is owned by another
	// project and the caller is not subscribed; not visible to
	// non-superusers.
	CatalogVisibilityForeignPrivate CatalogVisibility = "foreign_private"
	// CatalogVisibilityUnauthorized = the catalog doesn't exist at all
	// (404 collapsed into "unauthorized" so the handler never leaks
	// existence to non-owning project admins).
	CatalogVisibilityUnauthorized CatalogVisibility = "unauthorized"
)

// GetCatalogVisibilityForProject returns the relationship between a
// project and a catalog. Pure-DB; the handler layer mixes in superuser
// bypass logic separately.
func (q *Queries) GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (CatalogVisibility, error) {
	cat, err := q.GetHelmRepositoryWithOwner(ctx, catalogID)
	if err != nil {
		return CatalogVisibilityUnauthorized, err
	}
	// Project-owned by caller?
	if cat.OwnerProjectID.Valid && cat.OwnerProjectID.Bytes == projectID {
		return CatalogVisibilityOwn, nil
	}
	// Is there a subscription row?
	if _, subErr := q.GetProjectCatalogSubscription(ctx, GetProjectCatalogSubscriptionParams{
		ProjectID: projectID,
		CatalogID: catalogID,
	}); subErr == nil {
		return CatalogVisibilitySubscribedPublic, nil
	}
	// Public + no subscription → visible to browse but not "subscribed".
	if !cat.OwnerProjectID.Valid {
		return CatalogVisibilityPublic, nil
	}
	// Owned by another project, no subscription → forbidden for non-su.
	return CatalogVisibilityForeignPrivate, nil
}
