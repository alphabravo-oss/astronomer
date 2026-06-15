// Migration 063 — read-side audit policies.
//
// Hand-authored sqlc shim. The sqlc CLI currently chokes on the
// compliance.sql lexer; rather than re-run the generator across every
// query file (which would regenerate noisy diffs), we add the new
// queries here in the same `_manual.go` style established by the
// audit_v1, compliance, and notification_templates files.
//
// Schema definition: internal/db/migrations/063_read_audit.up.sql.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const readAuditPolicyColumns = `id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at`

func scanReadAuditPolicy(row interface {
	Scan(dest ...any) error
}) (ReadAuditPolicy, error) {
	var p ReadAuditPolicy
	var sample pgtype.Numeric
	err := row.Scan(
		&p.ID,
		&p.Name,
		&p.Description,
		&p.PathPattern,
		&p.Verbs,
		&sample,
		&p.Enabled,
		&p.CreatedBy,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		return ReadAuditPolicy{}, err
	}
	// pgtype.Numeric → float64 via the canonical Float64Value accessor.
	if v, err := sample.Float64Value(); err == nil && v.Valid {
		p.SampleRate = v.Float64
	}
	return p, nil
}

const listReadAuditPolicies = `-- name: ListReadAuditPolicies :many
SELECT ` + readAuditPolicyColumns + `
FROM read_audit_policies
ORDER BY name ASC`

// ListReadAuditPolicies returns every row, enabled or not. The middleware
// filters by Enabled at evaluation time; admin UX still wants to see the
// disabled ones.
func (q *Queries) ListReadAuditPolicies(ctx context.Context) ([]ReadAuditPolicy, error) {
	rows, err := q.db.Query(ctx, listReadAuditPolicies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReadAuditPolicy{}
	for rows.Next() {
		p, err := scanReadAuditPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const listEnabledReadAuditPolicies = `-- name: ListEnabledReadAuditPolicies :many
SELECT ` + readAuditPolicyColumns + `
FROM read_audit_policies
WHERE enabled = true
ORDER BY name ASC`

func (q *Queries) ListEnabledReadAuditPolicies(ctx context.Context) ([]ReadAuditPolicy, error) {
	rows, err := q.db.Query(ctx, listEnabledReadAuditPolicies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReadAuditPolicy{}
	for rows.Next() {
		p, err := scanReadAuditPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const getReadAuditPolicy = `-- name: GetReadAuditPolicy :one
SELECT ` + readAuditPolicyColumns + `
FROM read_audit_policies
WHERE id = $1`

func (q *Queries) GetReadAuditPolicy(ctx context.Context, id uuid.UUID) (ReadAuditPolicy, error) {
	row := q.db.QueryRow(ctx, getReadAuditPolicy, id)
	return scanReadAuditPolicy(row)
}

// CreateReadAuditPolicyParams is the input to CreateReadAuditPolicy.
type CreateReadAuditPolicyParams struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	PathPattern string      `json:"path_pattern"`
	Verbs       string      `json:"verbs"`
	SampleRate  float64     `json:"sample_rate"`
	Enabled     bool        `json:"enabled"`
	CreatedBy   pgtype.UUID `json:"created_by"`
}

const createReadAuditPolicy = `-- name: CreateReadAuditPolicy :one
INSERT INTO read_audit_policies (
    name, description, path_pattern, verbs, sample_rate, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + readAuditPolicyColumns

func (q *Queries) CreateReadAuditPolicy(ctx context.Context, arg CreateReadAuditPolicyParams) (ReadAuditPolicy, error) {
	sample := numericFromFloat(arg.SampleRate)
	row := q.db.QueryRow(ctx, createReadAuditPolicy,
		arg.Name,
		arg.Description,
		arg.PathPattern,
		arg.Verbs,
		sample,
		arg.Enabled,
		arg.CreatedBy,
	)
	return scanReadAuditPolicy(row)
}

// UpdateReadAuditPolicyParams is the input to UpdateReadAuditPolicy.
type UpdateReadAuditPolicyParams struct {
	ID          uuid.UUID `json:"id"`
	Description string    `json:"description"`
	PathPattern string    `json:"path_pattern"`
	Verbs       string    `json:"verbs"`
	SampleRate  float64   `json:"sample_rate"`
	Enabled     bool      `json:"enabled"`
}

const updateReadAuditPolicy = `-- name: UpdateReadAuditPolicy :one
UPDATE read_audit_policies
SET description  = $2,
    path_pattern = $3,
    verbs        = $4,
    sample_rate  = $5,
    enabled      = $6,
    updated_at   = now()
WHERE id = $1
RETURNING ` + readAuditPolicyColumns

func (q *Queries) UpdateReadAuditPolicy(ctx context.Context, arg UpdateReadAuditPolicyParams) (ReadAuditPolicy, error) {
	sample := numericFromFloat(arg.SampleRate)
	row := q.db.QueryRow(ctx, updateReadAuditPolicy,
		arg.ID,
		arg.Description,
		arg.PathPattern,
		arg.Verbs,
		sample,
		arg.Enabled,
	)
	return scanReadAuditPolicy(row)
}

const deleteReadAuditPolicy = `-- name: DeleteReadAuditPolicy :exec
DELETE FROM read_audit_policies WHERE id = $1`

func (q *Queries) DeleteReadAuditPolicy(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteReadAuditPolicy, id)
	return err
}

// numericFromFloat converts a Go float64 into a pgtype.Numeric for the
// sample_rate NUMERIC(3,2) column. We hand-roll this rather than rely on
// pgtype.Numeric.Scan(float64) because pgx requires a string-form input
// for arbitrary precision; the two-decimal format matches the column.
func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	// Use the string-scan path which pgtype understands directly.
	if err := n.Scan(formatRate(f)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}

func formatRate(f float64) string {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	// Two decimals matches NUMERIC(3,2). 1.00 / 0.10 / 0.00 etc.
	return fmtFloat2(f)
}

// fmtFloat2 is a no-allocs alternative to fmt.Sprintf("%.2f", f) for the
// sample-rate hot path. Inputs are bounded to [0,1] by formatRate.
func fmtFloat2(f float64) string {
	// Two-decimal format only — manual to avoid pulling fmt for this one
	// allocation-prone call.
	hundredths := int(f*100 + 0.5)
	if hundredths < 0 {
		hundredths = 0
	}
	if hundredths > 100 {
		hundredths = 100
	}
	whole := hundredths / 100
	frac := hundredths % 100
	b := []byte{byte('0' + whole), '.', byte('0' + frac/10), byte('0' + frac%10)}
	return string(b)
}
