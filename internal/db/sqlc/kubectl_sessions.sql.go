// Migration 065 — in-browser kubectl shell sessions.
//
// Hand-authored sqlc shim. See queries/kubectl_sessions.sql for the
// canonical SQL. Format mirrors cloud_credentials.sql.go so a future
// regen pass produces byte-compatible output.

package sqlc

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// KubectlSession is the row shape for kubectl_sessions.
type KubectlSession struct {
	ID           uuid.UUID          `json:"id"`
	UserID       uuid.UUID          `json:"user_id"`
	ClusterID    uuid.UUID          `json:"cluster_id"`
	SaNamespace  string             `json:"sa_namespace"`
	SaName       string             `json:"sa_name"`
	PodNamespace string             `json:"pod_namespace"`
	PodName      string             `json:"pod_name"`
	Status       string             `json:"status"`
	StartedAt    time.Time          `json:"started_at"`
	LastInputAt  time.Time          `json:"last_input_at"`
	ClosedAt     pgtype.Timestamptz `json:"closed_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
	LastError    string             `json:"last_error"`
	ClientIP     *netip.Addr        `json:"client_ip"`
	UserAgent    string             `json:"user_agent"`
}

// KubectlSessionCommand is the row shape for kubectl_session_commands.
type KubectlSessionCommand struct {
	ID          int64     `json:"id"`
	SessionID   uuid.UUID `json:"session_id"`
	CommandAt   time.Time `json:"command_at"`
	CommandLine string    `json:"command_line"`
}

const kubectlSessionColumns = `id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name, status, started_at, last_input_at, closed_at, expires_at, last_error, client_ip, user_agent`

func scanKubectlSessionRow(row interface {
	Scan(dest ...any) error
}) (KubectlSession, error) {
	var i KubectlSession
	err := row.Scan(
		&i.ID,
		&i.UserID,
		&i.ClusterID,
		&i.SaNamespace,
		&i.SaName,
		&i.PodNamespace,
		&i.PodName,
		&i.Status,
		&i.StartedAt,
		&i.LastInputAt,
		&i.ClosedAt,
		&i.ExpiresAt,
		&i.LastError,
		&i.ClientIP,
		&i.UserAgent,
	)
	return i, err
}

const createKubectlSession = `-- name: CreateKubectlSession :one
INSERT INTO kubectl_sessions (
    user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
    status, client_ip, user_agent
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING ` + kubectlSessionColumns

// CreateKubectlSessionParams matches the INSERT in queries/kubectl_sessions.sql.
type CreateKubectlSessionParams struct {
	UserID       uuid.UUID   `json:"user_id"`
	ClusterID    uuid.UUID   `json:"cluster_id"`
	SaNamespace  string      `json:"sa_namespace"`
	SaName       string      `json:"sa_name"`
	PodNamespace string      `json:"pod_namespace"`
	PodName      string      `json:"pod_name"`
	Status       string      `json:"status"`
	ClientIP     *netip.Addr `json:"client_ip"`
	UserAgent    string      `json:"user_agent"`
}

func (q *Queries) CreateKubectlSession(ctx context.Context, arg CreateKubectlSessionParams) (KubectlSession, error) {
	row := q.db.QueryRow(ctx, createKubectlSession,
		arg.UserID,
		arg.ClusterID,
		arg.SaNamespace,
		arg.SaName,
		arg.PodNamespace,
		arg.PodName,
		arg.Status,
		arg.ClientIP,
		arg.UserAgent,
	)
	return scanKubectlSessionRow(row)
}

const getKubectlSessionByID = `-- name: GetKubectlSessionByID :one
SELECT ` + kubectlSessionColumns + ` FROM kubectl_sessions WHERE id = $1`

func (q *Queries) GetKubectlSessionByID(ctx context.Context, id uuid.UUID) (KubectlSession, error) {
	row := q.db.QueryRow(ctx, getKubectlSessionByID, id)
	return scanKubectlSessionRow(row)
}

const listActiveKubectlSessionsByCluster = `-- name: ListActiveKubectlSessionsByCluster :many
SELECT ` + kubectlSessionColumns + `
FROM kubectl_sessions
WHERE cluster_id = $1 AND status IN ('starting', 'active')
ORDER BY started_at DESC`

func (q *Queries) ListActiveKubectlSessionsByCluster(ctx context.Context, clusterID uuid.UUID) ([]KubectlSession, error) {
	rows, err := q.db.Query(ctx, listActiveKubectlSessionsByCluster, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KubectlSession
	for rows.Next() {
		i, err := scanKubectlSessionRow(rows)
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

const listAllActiveKubectlSessions = `-- name: ListAllActiveKubectlSessions :many
SELECT ` + kubectlSessionColumns + `
FROM kubectl_sessions
WHERE status IN ('starting', 'active')
ORDER BY started_at DESC`

func (q *Queries) ListAllActiveKubectlSessions(ctx context.Context) ([]KubectlSession, error) {
	rows, err := q.db.Query(ctx, listAllActiveKubectlSessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KubectlSession
	for rows.Next() {
		i, err := scanKubectlSessionRow(rows)
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

const listExpiredKubectlSessions = `-- name: ListExpiredKubectlSessions :many
SELECT ` + kubectlSessionColumns + `
FROM kubectl_sessions
WHERE status IN ('starting', 'active')
  AND (expires_at < now() OR last_input_at + interval '30 minutes' < now())`

func (q *Queries) ListExpiredKubectlSessions(ctx context.Context) ([]KubectlSession, error) {
	rows, err := q.db.Query(ctx, listExpiredKubectlSessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KubectlSession
	for rows.Next() {
		i, err := scanKubectlSessionRow(rows)
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

const setKubectlSessionStatus = `-- name: SetKubectlSessionStatus :exec
UPDATE kubectl_sessions
SET status = $2,
    closed_at = CASE WHEN $2 IN ('closed','expired','failed') THEN now() ELSE closed_at END,
    last_error = COALESCE($3, last_error)
WHERE id = $1`

// SetKubectlSessionStatusParams is the typed argument bundle. LastError
// is a pgtype.Text so callers can pass NULL (preserve existing value) by
// leaving Valid=false, or replace with empty string by passing
// Valid=true and an empty String.
type SetKubectlSessionStatusParams struct {
	ID        uuid.UUID   `json:"id"`
	Status    string      `json:"status"`
	LastError pgtype.Text `json:"last_error"`
}

func (q *Queries) SetKubectlSessionStatus(ctx context.Context, arg SetKubectlSessionStatusParams) error {
	_, err := q.db.Exec(ctx, setKubectlSessionStatus, arg.ID, arg.Status, arg.LastError)
	return err
}

const touchKubectlSessionInput = `-- name: TouchKubectlSessionInput :exec
UPDATE kubectl_sessions
SET last_input_at = now()
WHERE id = $1 AND status IN ('starting', 'active')`

func (q *Queries) TouchKubectlSessionInput(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, touchKubectlSessionInput, id)
	return err
}

const insertKubectlSessionCommand = `-- name: InsertKubectlSessionCommand :exec
INSERT INTO kubectl_session_commands (session_id, command_line) VALUES ($1, $2)`

// InsertKubectlSessionCommandParams matches the INSERT.
type InsertKubectlSessionCommandParams struct {
	SessionID   uuid.UUID `json:"session_id"`
	CommandLine string    `json:"command_line"`
}

func (q *Queries) InsertKubectlSessionCommand(ctx context.Context, arg InsertKubectlSessionCommandParams) error {
	_, err := q.db.Exec(ctx, insertKubectlSessionCommand, arg.SessionID, arg.CommandLine)
	return err
}

const listKubectlSessionCommands = `-- name: ListKubectlSessionCommands :many
SELECT id, session_id, command_at, command_line
FROM kubectl_session_commands
WHERE session_id = $1
ORDER BY command_at ASC, id ASC
LIMIT $2 OFFSET $3`

// ListKubectlSessionCommandsParams matches the SELECT pagination.
type ListKubectlSessionCommandsParams struct {
	SessionID uuid.UUID `json:"session_id"`
	Limit     int32     `json:"limit"`
	Offset    int32     `json:"offset"`
}

func (q *Queries) ListKubectlSessionCommands(ctx context.Context, arg ListKubectlSessionCommandsParams) ([]KubectlSessionCommand, error) {
	rows, err := q.db.Query(ctx, listKubectlSessionCommands, arg.SessionID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KubectlSessionCommand
	for rows.Next() {
		var i KubectlSessionCommand
		if err := rows.Scan(&i.ID, &i.SessionID, &i.CommandAt, &i.CommandLine); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countKubectlSessionCommands = `-- name: CountKubectlSessionCommands :one
SELECT COUNT(*) FROM kubectl_session_commands WHERE session_id = $1`

func (q *Queries) CountKubectlSessionCommands(ctx context.Context, sessionID uuid.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countKubectlSessionCommands, sessionID)
	var n int64
	err := row.Scan(&n)
	return n, err
}
