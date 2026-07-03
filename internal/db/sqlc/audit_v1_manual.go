package sqlc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const createAuditLogV1 = `
INSERT INTO audit_log (
    source,
    correlation_id,
    user_id,
    actor_auth_method,
    action,
    resource_type,
    resource_id,
    resource_name,
    http_method,
    path,
    status_code,
    duration_ms,
    request_id,
    ip_address,
    user_agent,
    detail,
    action_class
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
`

type CreateAuditLogV1Params struct {
	Source          string          `json:"source"`
	CorrelationID   string          `json:"correlation_id"`
	UserID          pgtype.UUID     `json:"user_id"`
	ActorAuthMethod string          `json:"actor_auth_method"`
	Action          string          `json:"action"`
	ResourceType    string          `json:"resource_type"`
	ResourceID      string          `json:"resource_id"`
	ResourceName    string          `json:"resource_name"`
	HTTPMethod      string          `json:"http_method"`
	Path            string          `json:"path"`
	StatusCode      int32           `json:"status_code"`
	DurationMs      int64           `json:"duration_ms"`
	RequestID       string          `json:"request_id"`
	IpAddress       *netip.Addr     `json:"ip_address"`
	UserAgent       string          `json:"user_agent"`
	Detail          json.RawMessage `json:"detail"`
	// ActionClass is migration-063's "read"/"mutation"/"auth"/"system"
	// label. Empty defers to the column DEFAULT 'mutation'.
	ActionClass string `json:"action_class"`
}

type ListAuditLogsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListAuditLogsByUserParams struct {
	UserID pgtype.UUID `json:"user_id"`
	Limit  int32       `json:"limit"`
	Offset int32       `json:"offset"`
}

type ListAuditLogsByResourceTypeParams struct {
	ResourceType string `json:"resource_type"`
	Limit        int32  `json:"limit"`
	Offset       int32  `json:"offset"`
}

type ListAuditLogsByActionParams struct {
	Action string `json:"action"`
	Limit  int32  `json:"limit"`
	Offset int32  `json:"offset"`
}

type ListAuditLogsSinceParams struct {
	SinceID uuid.UUID `json:"since_id"`
	Limit   int32     `json:"limit"`
}

func (q *Queries) CreateAuditLogV1(ctx context.Context, arg CreateAuditLogV1Params) error {
	cls := arg.ActionClass
	if cls == "" {
		cls = "mutation"
	}
	_, err := q.db.Exec(ctx, createAuditLogV1,
		arg.Source,
		arg.CorrelationID,
		arg.UserID,
		arg.ActorAuthMethod,
		arg.Action,
		arg.ResourceType,
		arg.ResourceID,
		arg.ResourceName,
		arg.HTTPMethod,
		arg.Path,
		arg.StatusCode,
		arg.DurationMs,
		arg.RequestID,
		arg.IpAddress,
		arg.UserAgent,
		arg.Detail,
		cls,
	)
	return err
}

// auditLogColumnsPerRow is the number of parameters per row in the
// BatchInsertAuditLog VALUES list. Must stay in sync with the column list
// in batchInsertAuditLogPrefix below.
const auditLogColumnsPerRow = 17

const batchInsertAuditLogPrefix = `
INSERT INTO audit_log (
    source,
    correlation_id,
    user_id,
    actor_auth_method,
    action,
    resource_type,
    resource_id,
    resource_name,
    http_method,
    path,
    status_code,
    duration_ms,
    request_id,
    ip_address,
    user_agent,
    detail,
    action_class
) VALUES `

// BatchInsertAuditLog issues a single multi-row INSERT for the given rows.
// PostgreSQL caps a single statement at 65 535 bind parameters; at
// auditLogColumnsPerRow (17) columns per row that's 3 855 rows in a single
// Exec — we expect batch sizes of 50-250 in production, so the limit is
// academic but the implementation chunks defensively just in case.
//
// We hand-build the VALUES list because sqlc's :copyfrom path uses
// PostgreSQL COPY, which doesn't go through the same parameter-substitution
// pipeline our other queries do, and the unnest()-based form would need
// a function for each column type combination (jsonb / inet / uuid are
// awkward). A literal "VALUES ($1,$2,...),($17,$18,...)" Exec keeps the
// types identical to CreateAuditLogV1Params and works through pgx's
// existing pgtype encoders.
func (q *Queries) BatchInsertAuditLog(ctx context.Context, rows []CreateAuditLogV1Params) error {
	if len(rows) == 0 {
		return nil
	}

	// PostgreSQL bind-parameter limit is 65 535. Derive the row cap from the
	// actual column count so a chunk can never overflow it (the previous fixed
	// 4000 × 17 columns = 68 000 params overflowed and dropped the whole batch).
	const maxRowsPerExec = 65535 / auditLogColumnsPerRow
	for start := 0; start < len(rows); start += maxRowsPerExec {
		end := start + maxRowsPerExec
		if end > len(rows) {
			end = len(rows)
		}
		if err := q.execBatchInsertAuditLog(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (q *Queries) execBatchInsertAuditLog(ctx context.Context, rows []CreateAuditLogV1Params) error {
	if len(rows) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.Grow(len(batchInsertAuditLogPrefix) + len(rows)*auditLogColumnsPerRow*5)
	sb.WriteString(batchInsertAuditLogPrefix)

	args := make([]any, 0, len(rows)*auditLogColumnsPerRow)
	for i, r := range rows {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(")
		for j := 0; j < auditLogColumnsPerRow; j++ {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("$")
			sb.WriteString(strconv.Itoa(i*auditLogColumnsPerRow + j + 1))
		}
		sb.WriteString(")")
		cls := r.ActionClass
		if cls == "" {
			cls = "mutation"
		}
		args = append(args,
			r.Source,
			r.CorrelationID,
			r.UserID,
			r.ActorAuthMethod,
			r.Action,
			r.ResourceType,
			r.ResourceID,
			r.ResourceName,
			r.HTTPMethod,
			r.Path,
			r.StatusCode,
			r.DurationMs,
			r.RequestID,
			r.IpAddress,
			r.UserAgent,
			r.Detail,
			cls,
		)
	}

	_, err := q.db.Exec(ctx, sb.String(), args...)
	return err
}

const ensureAuditLogPartitions = `
SELECT create_audit_log_partition(now());
SELECT create_audit_log_partition(now() + INTERVAL '1 month');
`

func (q *Queries) EnsureAuditLogPartitions(ctx context.Context) error {
	_, err := q.db.Exec(ctx, ensureAuditLogPartitions)
	return err
}

const listAuditLogPartitions = `
SELECT child.relname
FROM pg_inherits
JOIN pg_class parent ON pg_inherits.inhparent = parent.oid
JOIN pg_class child ON pg_inherits.inhrelid = child.oid
JOIN pg_namespace nsp ON child.relnamespace = nsp.oid
WHERE parent.relname = 'audit_log'
  AND nsp.nspname = current_schema()
ORDER BY child.relname ASC
`

var auditLogMonthlyPartitionName = regexp.MustCompile(`^audit_log_\d{4}_\d{2}$`)

func (q *Queries) ListAuditLogPartitions(ctx context.Context) ([]string, error) {
	rows, err := q.db.Query(ctx, listAuditLogPartitions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	partitions := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if auditLogMonthlyPartitionName.MatchString(name) {
			partitions = append(partitions, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return partitions, nil
}

func (q *Queries) DropAuditLogPartition(ctx context.Context, name string) error {
	if !auditLogMonthlyPartitionName.MatchString(name) {
		return fmt.Errorf("invalid audit partition name %q", name)
	}
	_, err := q.db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", pgx.Identifier{name}.Sanitize()))
	return err
}

const getAuditLogV1ByID = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE id = $1
ORDER BY created_at DESC
LIMIT 1
`

func (q *Queries) GetAuditLogV1ByID(ctx context.Context, id uuid.UUID) (AuditLog, error) {
	row := q.db.QueryRow(ctx, getAuditLogV1ByID, id)
	var item AuditLog
	err := row.Scan(
		&item.ID,
		&item.CreatedAt,
		&item.SchemaVersion,
		&item.Source,
		&item.CorrelationID,
		&item.UserID,
		&item.ActorAuthMethod,
		&item.Action,
		&item.ResourceType,
		&item.ResourceID,
		&item.ResourceName,
		&item.HttpMethod,
		&item.Path,
		&item.StatusCode,
		&item.DurationMs,
		&item.RequestID,
		&item.IpAddress,
		&item.UserAgent,
		&item.Detail,
		&item.ActionClass,
	)
	if err != nil {
		return AuditLog{}, err
	}
	return item, nil
}

const listAuditLogV1 = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
ORDER BY created_at DESC
LIMIT $1 OFFSET $2
`

func (q *Queries) ListAuditLogV1(ctx context.Context, arg ListAuditLogsParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1, arg.Limit, arg.Offset)
}

const listAuditLogV1ByUser = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3
`

func (q *Queries) ListAuditLogV1ByUser(ctx context.Context, arg ListAuditLogsByUserParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1ByUser, arg.UserID, arg.Limit, arg.Offset)
}

const listAuditLogV1ByResourceType = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE resource_type = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3
`

func (q *Queries) ListAuditLogV1ByResourceType(ctx context.Context, arg ListAuditLogsByResourceTypeParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1ByResourceType, arg.ResourceType, arg.Limit, arg.Offset)
}

const listAuditLogV1ByAction = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE action = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3
`

const listAuditLogV1Since = `
SELECT id, created_at, schema_version, source, correlation_id, user_id, actor_auth_method, action, resource_type, resource_id, resource_name, http_method, path, status_code, duration_ms, request_id, ip_address, user_agent, detail, action_class
FROM audit_log
WHERE (created_at, id) > (
    SELECT created_at, id
    FROM audit_log
    WHERE id = $1
    ORDER BY created_at DESC
    LIMIT 1
)
ORDER BY created_at ASC, id ASC
LIMIT $2
`

func (q *Queries) ListAuditLogV1ByAction(ctx context.Context, arg ListAuditLogsByActionParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1ByAction, arg.Action, arg.Limit, arg.Offset)
}

func (q *Queries) ListAuditLogV1Since(ctx context.Context, arg ListAuditLogsSinceParams) ([]AuditLog, error) {
	return q.listAuditLogV1Rows(ctx, listAuditLogV1Since, arg.SinceID, arg.Limit)
}

const countAuditLogV1 = `
SELECT count(*) FROM audit_log
`

func (q *Queries) CountAuditLogV1(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, countAuditLogV1)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const countAuditLogV1ByUser = `
SELECT count(*) FROM audit_log WHERE user_id = $1
`

func (q *Queries) CountAuditLogV1ByUser(ctx context.Context, userID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countAuditLogV1ByUser, userID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) listAuditLogV1Rows(ctx context.Context, query string, args ...any) ([]AuditLog, error) {
	rows, err := q.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []AuditLog{}
	for rows.Next() {
		item, scanErr := scanAuditLogV1(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func scanAuditLogV1(row interface {
	Scan(dest ...any) error
}) (AuditLog, error) {
	var item AuditLog
	err := row.Scan(
		&item.ID,
		&item.CreatedAt,
		&item.SchemaVersion,
		&item.Source,
		&item.CorrelationID,
		&item.UserID,
		&item.ActorAuthMethod,
		&item.Action,
		&item.ResourceType,
		&item.ResourceID,
		&item.ResourceName,
		&item.HttpMethod,
		&item.Path,
		&item.StatusCode,
		&item.DurationMs,
		&item.RequestID,
		&item.IpAddress,
		&item.UserAgent,
		&item.Detail,
		&item.ActionClass,
	)
	if err != nil {
		return AuditLog{}, err
	}
	return item, nil
}
