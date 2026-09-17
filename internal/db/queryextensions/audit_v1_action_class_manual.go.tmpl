// Migration 063 — audit_log filter by action_class.
//
// Hand-authored sqlc shim. Adds list/count helpers that filter the
// existing audit_log table by the new action_class column.

package sqlc

import (
	"context"
)

const listAuditLogV1ByActionClass = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE action_class = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3
`

// ListAuditLogsByActionClassParams is the input to ListAuditLogV1ByActionClass.
type ListAuditLogsByActionClassParams struct {
	ActionClass string `json:"action_class"`
	Limit       int32  `json:"limit"`
	Offset      int32  `json:"offset"`
}

func (q *Queries) ListAuditLogV1ByActionClass(ctx context.Context, arg ListAuditLogsByActionClassParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1ByActionClass, arg.ActionClass, arg.Limit, arg.Offset)
}

const countAuditLogV1ByActionClass = `
SELECT count(*) FROM audit_log WHERE action_class = $1
`

func (q *Queries) CountAuditLogV1ByActionClass(ctx context.Context, actionClass string) (int64, error) {
	row := q.db.QueryRow(ctx, countAuditLogV1ByActionClass, actionClass)
	var n int64
	err := row.Scan(&n)
	return n, err
}
