-- Per-project catalog queries — migration 061.
--
-- The hot path is ListCatalogsForProject which UNIONs three buckets:
--   1. globals (owner_project_id IS NULL)
--   2. catalogs the project owns
--   3. catalogs the project has subscribed to
-- so a single SELECT returns the catalog row set the project admin
-- (and project users browsing Apps) should see.
--
-- NOTE: these queries are HAND-AUTHORED in
-- internal/db/sqlc/project_catalogs_ext.sql.go because the repo's local
-- sqlc CLI is broken; this .sql file is the canonical source of truth
-- the codegen would consume on a future regen.

-- name: ListCatalogsForProject :many
SELECT id, name, url, repo_type, description, is_default, auth_type,
       auth_config, enabled, last_synced_at, created_by_id, created_at,
       updated_at, owner_project_id
FROM helm_repositories
WHERE owner_project_id IS NULL
   OR owner_project_id = $1
   OR id IN (
        SELECT catalog_id FROM project_catalog_subscriptions
        WHERE project_id = $1
   )
ORDER BY name ASC;

-- name: ListProjectSubscriptions :many
SELECT id, project_id, catalog_id, created_by, created_at
FROM project_catalog_subscriptions
WHERE project_id = $1
ORDER BY created_at ASC;

-- name: CreateProjectCatalogSubscription :one
INSERT INTO project_catalog_subscriptions (project_id, catalog_id, created_by)
VALUES ($1, $2, $3)
RETURNING id, project_id, catalog_id, created_by, created_at;

-- name: DeleteProjectCatalogSubscription :exec
DELETE FROM project_catalog_subscriptions
WHERE project_id = $1 AND catalog_id = $2;

-- name: GetProjectCatalogSubscription :one
SELECT id, project_id, catalog_id, created_by, created_at
FROM project_catalog_subscriptions
WHERE project_id = $1 AND catalog_id = $2;

-- name: ListProjectOwnedCatalogs :many
SELECT id, name, url, repo_type, description, is_default, auth_type,
       auth_config, enabled, last_synced_at, created_by_id, created_at,
       updated_at, owner_project_id
FROM helm_repositories
WHERE owner_project_id = $1
ORDER BY name ASC;

-- name: CountSubscriptionsByCatalog :one
SELECT count(*) FROM project_catalog_subscriptions WHERE catalog_id = $1;

-- name: GetHelmRepositoryWithOwner :one
SELECT id, name, url, repo_type, description, is_default, auth_type,
       auth_config, enabled, last_synced_at, created_by_id, created_at,
       updated_at, owner_project_id
FROM helm_repositories
WHERE id = $1;

-- name: CreateProjectOwnedCatalog :one
INSERT INTO helm_repositories (
    name, url, repo_type, description, is_default, auth_type,
    auth_config, enabled, created_by_id, owner_project_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, name, url, repo_type, description, is_default, auth_type,
          auth_config, enabled, last_synced_at, created_by_id, created_at,
          updated_at, owner_project_id;

-- name: ListAdminCatalogsIncludingProjectOwned :many
SELECT id, name, url, repo_type, description, is_default, auth_type,
       auth_config, enabled, last_synced_at, created_by_id, created_at,
       updated_at, owner_project_id
FROM helm_repositories
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
