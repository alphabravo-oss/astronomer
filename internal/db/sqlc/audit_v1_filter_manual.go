package sqlc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// AuditLogFilterParams is the composable audit-log search input used by the
// operator audit page. Empty fields are ignored.
type AuditLogFilterParams struct {
	UserID        pgtype.UUID `json:"user_id"`
	Actor         string      `json:"actor"`
	ResourceType  string      `json:"resource_type"`
	ResourceID    string      `json:"resource_id"`
	ResourceName  string      `json:"resource_name"`
	Target        string      `json:"target"`
	Action        string      `json:"action"`
	ActionClass   string      `json:"action_class"`
	Result        string      `json:"result"`
	StatusCode    int32       `json:"status_code"`
	HasStatusCode bool        `json:"has_status_code"`
	Source        string      `json:"source"`
	CorrelationID string      `json:"correlation_id"`
	RequestID     string      `json:"request_id"`
	ClusterID     string      `json:"cluster_id"`
	ProjectID     string      `json:"project_id"`
	From          time.Time   `json:"from"`
	HasFrom       bool        `json:"has_from"`
	To            time.Time   `json:"to"`
	HasTo         bool        `json:"has_to"`
	Limit         int32       `json:"limit"`
	Offset        int32       `json:"offset"`
}

const auditLogV1SelectColumns = `
a.id, a.created_at, a.schema_version, a.source, a.correlation_id, a.user_id, a.actor_auth_method, a.action, a.resource_type, a.resource_id, a.resource_name, a.http_method, a.path, a.status_code, a.duration_ms, a.request_id, a.ip_address, a.user_agent, a.detail, a.action_class
`

// ListAuditLogV1Filtered returns audit rows matching every supplied filter.
func (q *Queries) ListAuditLogV1Filtered(ctx context.Context, arg AuditLogFilterParams) ([]AuditLog, error) {
	where, args := buildAuditLogV1FilterWhere(arg)
	args = append(args, arg.Limit, arg.Offset)
	query := fmt.Sprintf(`
SELECT %s
FROM audit_log AS a
%s
ORDER BY a.created_at DESC
LIMIT $%d OFFSET $%d
`, auditLogV1SelectColumns, where, len(args)-1, len(args))
	return q.listAuditLogV1Rows(ctx, query, args...)
}

// CountAuditLogV1Filtered counts audit rows matching every supplied filter.
func (q *Queries) CountAuditLogV1Filtered(ctx context.Context, arg AuditLogFilterParams) (int64, error) {
	where, args := buildAuditLogV1FilterWhere(arg)
	query := fmt.Sprintf(`
SELECT count(*)
FROM audit_log AS a
%s
`, where)
	row := q.db.QueryRow(ctx, query, args...)
	var n int64
	err := row.Scan(&n)
	return n, err
}

func buildAuditLogV1FilterWhere(arg AuditLogFilterParams) (string, []any) {
	var clauses []string
	args := []any{}
	add := func(format string, value any) {
		args = append(args, value)
		placeholder := fmt.Sprintf("$%d", len(args))
		clauses = append(clauses, strings.ReplaceAll(format, "$%d", placeholder))
	}
	addText := func(column, value string) {
		if value == "" {
			return
		}
		add("a."+column+" = $%d", value)
	}

	if arg.UserID.Valid {
		add("a.user_id = $%d", arg.UserID)
	}
	if actor := strings.TrimSpace(arg.Actor); actor != "" {
		pattern := "%" + strings.ToLower(actor) + "%"
		add(`(
			lower(a.user_id::text) LIKE $%d OR
			lower(a.actor_auth_method) LIKE $%d OR
			EXISTS (
				SELECT 1
				FROM users u
				WHERE u.id = a.user_id
				  AND (
					lower(u.email) LIKE $%d OR
					lower(u.username) LIKE $%d OR
					lower(concat_ws(' ', u.first_name, u.last_name)) LIKE $%d
				  )
			)
		)`, pattern)
	}
	addText("resource_type", strings.TrimSpace(arg.ResourceType))
	addText("resource_id", strings.TrimSpace(arg.ResourceID))
	addText("resource_name", strings.TrimSpace(arg.ResourceName))
	if target := strings.TrimSpace(arg.Target); target != "" {
		pattern := "%" + strings.ToLower(target) + "%"
		add(`(
			lower(a.resource_type) LIKE $%d OR
			lower(a.resource_id) LIKE $%d OR
			lower(a.resource_name) LIKE $%d OR
			lower(a.path) LIKE $%d
		)`, pattern)
	}
	addText("action", strings.TrimSpace(arg.Action))
	addText("action_class", strings.TrimSpace(arg.ActionClass))
	if arg.HasStatusCode {
		add("a.status_code = $%d", arg.StatusCode)
	}
	switch strings.TrimSpace(arg.Result) {
	case "success":
		clauses = append(clauses, "(a.status_code = 0 OR (a.status_code >= 200 AND a.status_code < 400))")
	case "failure":
		clauses = append(clauses, "(a.status_code >= 400 AND a.status_code < 500)")
	case "error":
		clauses = append(clauses, "a.status_code >= 500")
	}
	if source := strings.TrimSpace(arg.Source); source != "" {
		pattern := "%" + strings.ToLower(source) + "%"
		add(`(
			lower(a.source) LIKE $%d OR
			lower(a.ip_address::text) LIKE $%d OR
			lower(a.user_agent) LIKE $%d
		)`, pattern)
	}
	addText("correlation_id", strings.TrimSpace(arg.CorrelationID))
	addText("request_id", strings.TrimSpace(arg.RequestID))
	if clusterID := strings.TrimSpace(arg.ClusterID); clusterID != "" {
		add(`(
			(a.resource_type = 'cluster' AND (a.resource_id = $%d OR a.resource_name = $%d)) OR
			a.detail->>'cluster_id' = $%d OR
			a.detail->>'clusterId' = $%d OR
			a.detail->>'cluster' = $%d OR
			a.detail->>'cluster_name' = $%d
		)`, clusterID)
	}
	if projectID := strings.TrimSpace(arg.ProjectID); projectID != "" {
		add(`(
			(a.resource_type = 'project' AND (a.resource_id = $%d OR a.resource_name = $%d)) OR
			a.detail->>'project_id' = $%d OR
			a.detail->>'projectId' = $%d OR
			a.detail->>'project' = $%d OR
			a.detail->>'project_name' = $%d
		)`, projectID)
	}
	if arg.HasFrom {
		add("a.created_at >= $%d", arg.From)
	}
	if arg.HasTo {
		add("a.created_at <= $%d", arg.To)
	}

	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, "\n  AND "), args
}
