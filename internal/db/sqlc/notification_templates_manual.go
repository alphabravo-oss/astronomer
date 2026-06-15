// Migration 059 — operator-tunable notification templates.
//
// Hand-authored sqlc shim. The sqlc CLI currently chokes on the
// compliance.sql lexer; rather than re-run the generator across every
// query file (which would regenerate noisy diffs), we add the new
// queries here in the same `_manual.go` style established by the
// audit_v1, compliance, and cluster_registry_configs_ext files.
//
// Schema definition: internal/db/migrations/059_notification_templates.up.sql
// Registry of built-in templates: internal/notify/templates.go.

package sqlc

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const notificationTemplateColumns = `id, template_key, channel, subject_tpl, body_tpl, body_format, enabled, updated_by, created_at, updated_at`

func scanNotificationTemplateRow(row interface {
	Scan(dest ...any) error
}) (NotificationTemplate, error) {
	var n NotificationTemplate
	err := row.Scan(
		&n.ID,
		&n.TemplateKey,
		&n.Channel,
		&n.SubjectTpl,
		&n.BodyTpl,
		&n.BodyFormat,
		&n.Enabled,
		&n.UpdatedBy,
		&n.CreatedAt,
		&n.UpdatedAt,
	)
	return n, err
}

const getNotificationTemplate = `-- name: GetNotificationTemplate :one
SELECT ` + notificationTemplateColumns + `
FROM notification_templates
WHERE template_key = $1`

// GetNotificationTemplate returns the (unique) override row for a
// template key. Returns pgx.ErrNoRows when no override has been
// persisted — the caller should treat that as "use registry default".
func (q *Queries) GetNotificationTemplate(ctx context.Context, key string) (NotificationTemplate, error) {
	row := q.db.QueryRow(ctx, getNotificationTemplate, key)
	return scanNotificationTemplateRow(row)
}

const listNotificationTemplates = `-- name: ListNotificationTemplates :many
SELECT ` + notificationTemplateColumns + `
FROM notification_templates
ORDER BY template_key ASC`

func (q *Queries) ListNotificationTemplates(ctx context.Context) ([]NotificationTemplate, error) {
	rows, err := q.db.Query(ctx, listNotificationTemplates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationTemplate{}
	for rows.Next() {
		n, err := scanNotificationTemplateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertNotificationTemplateParams is the input to UpsertNotificationTemplate.
type UpsertNotificationTemplateParams struct {
	TemplateKey string      `json:"template_key"`
	Channel     string      `json:"channel"`
	SubjectTpl  string      `json:"subject_tpl"`
	BodyTpl     string      `json:"body_tpl"`
	BodyFormat  string      `json:"body_format"`
	Enabled     bool        `json:"enabled"`
	UpdatedBy   pgtype.UUID `json:"updated_by"`
}

const upsertNotificationTemplate = `-- name: UpsertNotificationTemplate :one
INSERT INTO notification_templates (
    template_key, channel, subject_tpl, body_tpl, body_format, enabled, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (template_key) DO UPDATE SET
    channel     = EXCLUDED.channel,
    subject_tpl = EXCLUDED.subject_tpl,
    body_tpl    = EXCLUDED.body_tpl,
    body_format = EXCLUDED.body_format,
    enabled     = EXCLUDED.enabled,
    updated_by  = EXCLUDED.updated_by,
    updated_at  = now()
RETURNING ` + notificationTemplateColumns

func (q *Queries) UpsertNotificationTemplate(ctx context.Context, arg UpsertNotificationTemplateParams) (NotificationTemplate, error) {
	row := q.db.QueryRow(ctx, upsertNotificationTemplate,
		arg.TemplateKey,
		arg.Channel,
		arg.SubjectTpl,
		arg.BodyTpl,
		arg.BodyFormat,
		arg.Enabled,
		arg.UpdatedBy,
	)
	return scanNotificationTemplateRow(row)
}

const deleteNotificationTemplate = `-- name: DeleteNotificationTemplate :exec
DELETE FROM notification_templates WHERE template_key = $1`

func (q *Queries) DeleteNotificationTemplate(ctx context.Context, key string) error {
	_, err := q.db.Exec(ctx, deleteNotificationTemplate, key)
	return err
}
