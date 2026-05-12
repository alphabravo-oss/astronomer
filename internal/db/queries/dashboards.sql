-- Dashboard widgets + Prometheus datasources (migration 058).
--
-- Hand-edited SQL companion to the hand-written sqlc shim in
-- internal/db/sqlc/dashboards.sql.go. The sqlc CLI isn't part of the
-- local build path (compliance.sql lexer error blocks a fresh
-- generate); these queries are kept in the canonical queries/ tree so
-- a future `sqlc generate` picks them up by name.

-- name: ListDashboardWidgets :many
SELECT id, name, description, widget_type, spec, scope, scope_ids,
       grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
       created_by, created_at, updated_at
FROM dashboard_widgets
ORDER BY scope ASC, grid_y ASC, grid_x ASC, name ASC;

-- name: GetDashboardWidgetByID :one
SELECT id, name, description, widget_type, spec, scope, scope_ids,
       grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
       created_by, created_at, updated_at
FROM dashboard_widgets
WHERE id = $1;

-- name: CreateDashboardWidget :one
INSERT INTO dashboard_widgets (
    name, description, widget_type, spec, scope, scope_ids,
    grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, name, description, widget_type, spec, scope, scope_ids,
          grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
          created_by, created_at, updated_at;

-- name: UpdateDashboardWidget :one
UPDATE dashboard_widgets
SET name            = $2,
    description     = $3,
    widget_type     = $4,
    spec            = $5,
    scope           = $6,
    scope_ids       = $7,
    grid_x          = $8,
    grid_y          = $9,
    grid_w          = $10,
    grid_h          = $11,
    refresh_seconds = $12,
    enabled         = $13,
    updated_at      = now()
WHERE id = $1
RETURNING id, name, description, widget_type, spec, scope, scope_ids,
          grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
          created_by, created_at, updated_at;

-- name: DeleteDashboardWidget :exec
DELETE FROM dashboard_widgets WHERE id = $1;

-- name: ListWidgetsForScope :many
-- Returns the widgets the render handler should ship to a client at the
-- given (scope, scope_id) coordinate. Always includes globals; for
-- 'cluster' / 'project' the list expands to widgets scoped to that
-- entity OR widgets in that scope with an empty scope_ids set.
SELECT id, name, description, widget_type, spec, scope, scope_ids,
       grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
       created_by, created_at, updated_at
FROM dashboard_widgets
WHERE enabled = true
  AND (
    scope = 'global'
    OR (
      scope = $1
      AND (cardinality(scope_ids) = 0 OR $2 = ANY(scope_ids))
    )
  )
ORDER BY scope ASC, grid_y ASC, grid_x ASC, name ASC;

-- Prometheus datasources -------------------------------------------------

-- name: ListPrometheusDatasources :many
SELECT id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at
FROM prometheus_datasources
ORDER BY name ASC;

-- name: ListEnabledPrometheusDatasources :many
SELECT id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at
FROM prometheus_datasources
WHERE enabled = true
ORDER BY name ASC;

-- name: GetPrometheusDatasourceByID :one
SELECT id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at
FROM prometheus_datasources
WHERE id = $1;

-- name: GetPrometheusDatasourceByName :one
SELECT id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at
FROM prometheus_datasources
WHERE name = $1;

-- name: CreatePrometheusDatasource :one
INSERT INTO prometheus_datasources (name, url, auth_encrypted, tls_skip_verify, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at;

-- name: UpdatePrometheusDatasource :one
UPDATE prometheus_datasources
SET url             = $2,
    auth_encrypted  = $3,
    tls_skip_verify = $4,
    enabled         = $5,
    updated_at      = now()
WHERE id = $1
RETURNING id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at;

-- name: DeletePrometheusDatasource :exec
DELETE FROM prometheus_datasources WHERE id = $1;

-- name: GetClusterUIDForID :one
-- Project just the cluster_uid column for the render handler's
-- templating step. The generated GetClusterByID query (still keyed on
-- the migration-053 column list) doesn't include cluster_uid, so this
-- targeted query avoids a full row scan + a stale model.
SELECT cluster_uid FROM clusters WHERE id = $1;
