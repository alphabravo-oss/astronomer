package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type UIExtension struct {
	ID                  uuid.UUID       `json:"id"`
	Name                string          `json:"name"`
	DisplayName         string          `json:"display_name"`
	Version             string          `json:"version"`
	Source              string          `json:"source"`
	Checksum            string          `json:"checksum"`
	Enabled             bool            `json:"enabled"`
	CompatibilityStatus string          `json:"compatibility_status"`
	Manifest            json.RawMessage `json:"manifest"`
	InstalledBy         pgtype.UUID     `json:"installed_by"`
	InstalledAt         time.Time       `json:"installed_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type UpsertUIExtensionParams struct {
	Name                string          `json:"name"`
	DisplayName         string          `json:"display_name"`
	Version             string          `json:"version"`
	Source              string          `json:"source"`
	Checksum            string          `json:"checksum"`
	Enabled             bool            `json:"enabled"`
	CompatibilityStatus string          `json:"compatibility_status"`
	Manifest            json.RawMessage `json:"manifest"`
	InstalledBy         pgtype.UUID     `json:"installed_by"`
}

type SetUIExtensionEnabledParams struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func scanUIExtension(row pgx.Row) (UIExtension, error) {
	var i UIExtension
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.DisplayName,
		&i.Version,
		&i.Source,
		&i.Checksum,
		&i.Enabled,
		&i.CompatibilityStatus,
		&i.Manifest,
		&i.InstalledBy,
		&i.InstalledAt,
		&i.UpdatedAt,
	)
	return i, err
}

const uiExtensionColumns = `
    id,
    name,
    display_name,
    version,
    source,
    checksum,
    enabled,
    compatibility_status,
    manifest,
    installed_by,
    installed_at,
    updated_at`

const listUIExtensions = `-- name: ListUIExtensions :many
SELECT ` + uiExtensionColumns + `
FROM ui_extensions
ORDER BY enabled DESC, name ASC`

func (q *Queries) ListUIExtensions(ctx context.Context) ([]UIExtension, error) {
	rows, err := q.db.Query(ctx, listUIExtensions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UIExtension{}
	for rows.Next() {
		item, err := scanUIExtension(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const upsertUIExtension = `-- name: UpsertUIExtension :one
INSERT INTO ui_extensions (
    name,
    display_name,
    version,
    source,
    checksum,
    enabled,
    compatibility_status,
    manifest,
    installed_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (name) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    version = EXCLUDED.version,
    source = EXCLUDED.source,
    checksum = EXCLUDED.checksum,
    enabled = EXCLUDED.enabled,
    compatibility_status = EXCLUDED.compatibility_status,
    manifest = EXCLUDED.manifest,
    installed_by = EXCLUDED.installed_by,
    updated_at = now()
RETURNING ` + uiExtensionColumns

func (q *Queries) UpsertUIExtension(ctx context.Context, arg UpsertUIExtensionParams) (UIExtension, error) {
	row := q.db.QueryRow(ctx, upsertUIExtension,
		arg.Name,
		arg.DisplayName,
		arg.Version,
		arg.Source,
		arg.Checksum,
		arg.Enabled,
		arg.CompatibilityStatus,
		arg.Manifest,
		arg.InstalledBy,
	)
	return scanUIExtension(row)
}

const setUIExtensionEnabled = `-- name: SetUIExtensionEnabled :one
UPDATE ui_extensions
SET enabled = $2,
    updated_at = now()
WHERE name = $1
RETURNING ` + uiExtensionColumns

func (q *Queries) SetUIExtensionEnabled(ctx context.Context, arg SetUIExtensionEnabledParams) (UIExtension, error) {
	row := q.db.QueryRow(ctx, setUIExtensionEnabled, arg.Name, arg.Enabled)
	return scanUIExtension(row)
}
