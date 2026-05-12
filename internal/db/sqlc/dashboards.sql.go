// Migration 058 — dashboard widgets + Prometheus datasources, hand-authored sqlc shim.
//
// Mirrors what `sqlc generate` would emit for queries/dashboards.sql.
// The sqlc CLI is presently broken on this tree (compliance.sql lexer
// error blocks a fresh generate); we follow the same pattern as
// cloud_credentials.sql.go and cluster_snapshots_ext.sql.go so the
// build keeps passing.
//
// Two row types + a render-time helper (GetClusterUIDForID) projected
// directly off clusters since the generated Cluster model doesn't yet
// carry the migration-058 cluster_uid column.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// DashboardWidget mirrors one row of dashboard_widgets. spec is left
// opaque (json.RawMessage) — the handler interprets it per widget_type.
type DashboardWidget struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	WidgetType     string          `json:"widget_type"`
	Spec           json.RawMessage `json:"spec"`
	Scope          string          `json:"scope"`
	ScopeIDs       []uuid.UUID     `json:"scope_ids"`
	GridX          int32           `json:"grid_x"`
	GridY          int32           `json:"grid_y"`
	GridW          int32           `json:"grid_w"`
	GridH          int32           `json:"grid_h"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Enabled        bool            `json:"enabled"`
	CreatedBy      pgtype.UUID     `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// PrometheusDatasource mirrors one row of prometheus_datasources.
// auth_encrypted is the Fernet-encrypted JSON object the handler
// decrypts at /test/ time. The renderer in internal/dashboards never
// reads the cleartext — it dials the URL with a Bearer header or
// Basic-Auth only when explicitly tested.
type PrometheusDatasource struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	AuthEncrypted  string    `json:"auth_encrypted"`
	TLSSkipVerify  bool      `json:"tls_skip_verify"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const dashboardWidgetColumns = `id, name, description, widget_type, spec, scope, scope_ids,
       grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled,
       created_by, created_at, updated_at`

func scanDashboardWidgetRow(row interface {
	Scan(dest ...any) error
}) (DashboardWidget, error) {
	var i DashboardWidget
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Description,
		&i.WidgetType,
		&i.Spec,
		&i.Scope,
		&i.ScopeIDs,
		&i.GridX,
		&i.GridY,
		&i.GridW,
		&i.GridH,
		&i.RefreshSeconds,
		&i.Enabled,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listDashboardWidgets = `-- name: ListDashboardWidgets :many
SELECT ` + dashboardWidgetColumns + `
FROM dashboard_widgets
ORDER BY scope ASC, grid_y ASC, grid_x ASC, name ASC`

// ListDashboardWidgets returns every row, enabled or not. The admin UI
// uses this; the render handler uses ListWidgetsForScope instead.
func (q *Queries) ListDashboardWidgets(ctx context.Context) ([]DashboardWidget, error) {
	rows, err := q.db.Query(ctx, listDashboardWidgets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardWidget{}
	for rows.Next() {
		i, err := scanDashboardWidgetRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const getDashboardWidgetByID = `-- name: GetDashboardWidgetByID :one
SELECT ` + dashboardWidgetColumns + `
FROM dashboard_widgets
WHERE id = $1`

func (q *Queries) GetDashboardWidgetByID(ctx context.Context, id uuid.UUID) (DashboardWidget, error) {
	row := q.db.QueryRow(ctx, getDashboardWidgetByID, id)
	return scanDashboardWidgetRow(row)
}

// CreateDashboardWidgetParams matches the ordered VALUES of
// queries/dashboards.sql's CreateDashboardWidget statement.
type CreateDashboardWidgetParams struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	WidgetType     string          `json:"widget_type"`
	Spec           json.RawMessage `json:"spec"`
	Scope          string          `json:"scope"`
	ScopeIDs       []uuid.UUID     `json:"scope_ids"`
	GridX          int32           `json:"grid_x"`
	GridY          int32           `json:"grid_y"`
	GridW          int32           `json:"grid_w"`
	GridH          int32           `json:"grid_h"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Enabled        bool            `json:"enabled"`
	CreatedBy      pgtype.UUID     `json:"created_by"`
}

const createDashboardWidget = `-- name: CreateDashboardWidget :one
INSERT INTO dashboard_widgets (
    name, description, widget_type, spec, scope, scope_ids,
    grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING ` + dashboardWidgetColumns

func (q *Queries) CreateDashboardWidget(ctx context.Context, arg CreateDashboardWidgetParams) (DashboardWidget, error) {
	row := q.db.QueryRow(ctx, createDashboardWidget,
		arg.Name, arg.Description, arg.WidgetType, arg.Spec, arg.Scope, arg.ScopeIDs,
		arg.GridX, arg.GridY, arg.GridW, arg.GridH, arg.RefreshSeconds, arg.Enabled, arg.CreatedBy,
	)
	return scanDashboardWidgetRow(row)
}

// UpdateDashboardWidgetParams is the UPDATE statement's bind-shape; ID
// + every mutable column. created_by / created_at are intentionally
// immutable.
type UpdateDashboardWidgetParams struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	WidgetType     string          `json:"widget_type"`
	Spec           json.RawMessage `json:"spec"`
	Scope          string          `json:"scope"`
	ScopeIDs       []uuid.UUID     `json:"scope_ids"`
	GridX          int32           `json:"grid_x"`
	GridY          int32           `json:"grid_y"`
	GridW          int32           `json:"grid_w"`
	GridH          int32           `json:"grid_h"`
	RefreshSeconds int32           `json:"refresh_seconds"`
	Enabled        bool            `json:"enabled"`
}

const updateDashboardWidget = `-- name: UpdateDashboardWidget :one
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
RETURNING ` + dashboardWidgetColumns

func (q *Queries) UpdateDashboardWidget(ctx context.Context, arg UpdateDashboardWidgetParams) (DashboardWidget, error) {
	row := q.db.QueryRow(ctx, updateDashboardWidget,
		arg.ID, arg.Name, arg.Description, arg.WidgetType, arg.Spec, arg.Scope, arg.ScopeIDs,
		arg.GridX, arg.GridY, arg.GridW, arg.GridH, arg.RefreshSeconds, arg.Enabled,
	)
	return scanDashboardWidgetRow(row)
}

const deleteDashboardWidget = `-- name: DeleteDashboardWidget :exec
DELETE FROM dashboard_widgets WHERE id = $1`

func (q *Queries) DeleteDashboardWidget(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteDashboardWidget, id)
	return err
}

// ListWidgetsForScopeParams is the (scope, scopeID) coordinate the
// render handler resolves. scope is one of 'cluster' | 'project'; for
// 'global' the handler can pass either coordinate (the SQL filter
// ignores them on the global branch) but conventionally passes
// ('global', uuid.Nil).
type ListWidgetsForScopeParams struct {
	Scope   string    `json:"scope"`
	ScopeID uuid.UUID `json:"scope_id"`
}

const listWidgetsForScope = `-- name: ListWidgetsForScope :many
SELECT ` + dashboardWidgetColumns + `
FROM dashboard_widgets
WHERE enabled = true
  AND (
    scope = 'global'
    OR (
      scope = $1
      AND (cardinality(scope_ids) = 0 OR $2 = ANY(scope_ids))
    )
  )
ORDER BY scope ASC, grid_y ASC, grid_x ASC, name ASC`

// ListWidgetsForScope is the public-render hot path. The SQL combines
// two predicates so the handler does ONE round-trip per dashboard
// load (an N+1 fanout to "first fetch globals, then fetch scoped"
// would double the latency for no win).
func (q *Queries) ListWidgetsForScope(ctx context.Context, arg ListWidgetsForScopeParams) ([]DashboardWidget, error) {
	rows, err := q.db.Query(ctx, listWidgetsForScope, arg.Scope, arg.ScopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardWidget{}
	for rows.Next() {
		i, err := scanDashboardWidgetRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Prometheus datasources ────────────────────────────────────────────

const promDatasourceColumns = `id, name, url, auth_encrypted, tls_skip_verify, enabled, created_at, updated_at`

func scanPromDatasourceRow(row interface {
	Scan(dest ...any) error
}) (PrometheusDatasource, error) {
	var i PrometheusDatasource
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.URL,
		&i.AuthEncrypted,
		&i.TLSSkipVerify,
		&i.Enabled,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listPrometheusDatasources = `-- name: ListPrometheusDatasources :many
SELECT ` + promDatasourceColumns + `
FROM prometheus_datasources
ORDER BY name ASC`

func (q *Queries) ListPrometheusDatasources(ctx context.Context) ([]PrometheusDatasource, error) {
	rows, err := q.db.Query(ctx, listPrometheusDatasources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PrometheusDatasource{}
	for rows.Next() {
		i, err := scanPromDatasourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const listEnabledPrometheusDatasources = `-- name: ListEnabledPrometheusDatasources :many
SELECT ` + promDatasourceColumns + `
FROM prometheus_datasources
WHERE enabled = true
ORDER BY name ASC`

// ListEnabledPrometheusDatasources is what the render handler reads
// before resolving a widget spec's datasource by name — disabled rows
// shouldn't be queried.
func (q *Queries) ListEnabledPrometheusDatasources(ctx context.Context) ([]PrometheusDatasource, error) {
	rows, err := q.db.Query(ctx, listEnabledPrometheusDatasources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PrometheusDatasource{}
	for rows.Next() {
		i, err := scanPromDatasourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const getPrometheusDatasourceByID = `-- name: GetPrometheusDatasourceByID :one
SELECT ` + promDatasourceColumns + `
FROM prometheus_datasources
WHERE id = $1`

func (q *Queries) GetPrometheusDatasourceByID(ctx context.Context, id uuid.UUID) (PrometheusDatasource, error) {
	row := q.db.QueryRow(ctx, getPrometheusDatasourceByID, id)
	return scanPromDatasourceRow(row)
}

const getPrometheusDatasourceByName = `-- name: GetPrometheusDatasourceByName :one
SELECT ` + promDatasourceColumns + `
FROM prometheus_datasources
WHERE name = $1`

func (q *Queries) GetPrometheusDatasourceByName(ctx context.Context, name string) (PrometheusDatasource, error) {
	row := q.db.QueryRow(ctx, getPrometheusDatasourceByName, name)
	return scanPromDatasourceRow(row)
}

// CreatePrometheusDatasourceParams matches the ordered VALUES of the
// CreatePrometheusDatasource statement.
type CreatePrometheusDatasourceParams struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	AuthEncrypted string `json:"auth_encrypted"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	Enabled       bool   `json:"enabled"`
}

const createPrometheusDatasource = `-- name: CreatePrometheusDatasource :one
INSERT INTO prometheus_datasources (name, url, auth_encrypted, tls_skip_verify, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING ` + promDatasourceColumns

func (q *Queries) CreatePrometheusDatasource(ctx context.Context, arg CreatePrometheusDatasourceParams) (PrometheusDatasource, error) {
	row := q.db.QueryRow(ctx, createPrometheusDatasource,
		arg.Name, arg.URL, arg.AuthEncrypted, arg.TLSSkipVerify, arg.Enabled,
	)
	return scanPromDatasourceRow(row)
}

// UpdatePrometheusDatasourceParams is the UPDATE bind-shape. Name is
// the natural-key field and intentionally NOT mutable here — renaming
// a datasource silently breaks dependent widgets, and rename is rare
// enough that "create new + delete old" is acceptable.
type UpdatePrometheusDatasourceParams struct {
	ID            uuid.UUID `json:"id"`
	URL           string    `json:"url"`
	AuthEncrypted string    `json:"auth_encrypted"`
	TLSSkipVerify bool      `json:"tls_skip_verify"`
	Enabled       bool      `json:"enabled"`
}

const updatePrometheusDatasource = `-- name: UpdatePrometheusDatasource :one
UPDATE prometheus_datasources
SET url             = $2,
    auth_encrypted  = $3,
    tls_skip_verify = $4,
    enabled         = $5,
    updated_at      = now()
WHERE id = $1
RETURNING ` + promDatasourceColumns

func (q *Queries) UpdatePrometheusDatasource(ctx context.Context, arg UpdatePrometheusDatasourceParams) (PrometheusDatasource, error) {
	row := q.db.QueryRow(ctx, updatePrometheusDatasource,
		arg.ID, arg.URL, arg.AuthEncrypted, arg.TLSSkipVerify, arg.Enabled,
	)
	return scanPromDatasourceRow(row)
}

const deletePrometheusDatasource = `-- name: DeletePrometheusDatasource :exec
DELETE FROM prometheus_datasources WHERE id = $1`

func (q *Queries) DeletePrometheusDatasource(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deletePrometheusDatasource, id)
	return err
}

// ── Cluster UID projection ────────────────────────────────────────────

const getClusterUIDForID = `-- name: GetClusterUIDForID :one
SELECT cluster_uid FROM clusters WHERE id = $1`

// GetClusterUIDForID projects the migration-058 cluster_uid column.
// We keep it as a tiny separate query so the render handler doesn't
// pull the full Cluster row (which through GetClusterByID still uses
// the stale, pre-058 column list).
func (q *Queries) GetClusterUIDForID(ctx context.Context, id uuid.UUID) (string, error) {
	row := q.db.QueryRow(ctx, getClusterUIDForID, id)
	var s string
	err := row.Scan(&s)
	return s, err
}
