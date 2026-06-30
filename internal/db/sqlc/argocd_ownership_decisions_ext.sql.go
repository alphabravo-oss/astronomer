package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type UpsertArgoCDBaselineOwnershipDecisionParams struct {
	ClusterID     uuid.UUID          `json:"cluster_id"`
	ComponentSlug string             `json:"component_slug"`
	Decision      string             `json:"decision"`
	Reason        string             `json:"reason"`
	ExpiresAt     pgtype.Timestamptz `json:"expires_at"`
	DecidedByID   pgtype.UUID        `json:"decided_by_id"`
}

func scanArgoCDBaselineOwnershipDecision(row pgx.Row) (ArgocdBaselineOwnershipDecision, error) {
	var i ArgocdBaselineOwnershipDecision
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.ComponentSlug,
		&i.Decision,
		&i.Reason,
		&i.ExpiresAt,
		&i.DecidedByID,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const argoCDBaselineOwnershipDecisionColumns = `
    id,
    cluster_id,
    component_slug,
    decision,
    reason,
    expires_at,
    decided_by_id,
    created_at,
    updated_at`

const listArgoCDBaselineOwnershipDecisions = `-- name: ListArgoCDBaselineOwnershipDecisions :many
SELECT ` + argoCDBaselineOwnershipDecisionColumns + `
FROM argocd_baseline_ownership_decisions
WHERE cluster_id = $1
ORDER BY component_slug ASC`

func (q *Queries) ListArgoCDBaselineOwnershipDecisions(ctx context.Context, clusterID uuid.UUID) ([]ArgocdBaselineOwnershipDecision, error) {
	rows, err := q.db.Query(ctx, listArgoCDBaselineOwnershipDecisions, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ArgocdBaselineOwnershipDecision{}
	for rows.Next() {
		item, err := scanArgoCDBaselineOwnershipDecision(rows)
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

const listArgoCDBaselineOwnershipDecisionsByDecision = `-- name: ListArgoCDBaselineOwnershipDecisionsByDecision :many
SELECT ` + argoCDBaselineOwnershipDecisionColumns + `
FROM argocd_baseline_ownership_decisions
WHERE decision = $1
ORDER BY cluster_id ASC, component_slug ASC`

// ListArgoCDBaselineOwnershipDecisionsByDecision returns every (cluster,
// component) ownership row with the given decision across ALL clusters. The
// Argo push baseline generator uses it to fetch all "leave_local" rows in one
// query and exclude those clusters from the per-component fan-out.
func (q *Queries) ListArgoCDBaselineOwnershipDecisionsByDecision(ctx context.Context, decision string) ([]ArgocdBaselineOwnershipDecision, error) {
	rows, err := q.db.Query(ctx, listArgoCDBaselineOwnershipDecisionsByDecision, decision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ArgocdBaselineOwnershipDecision{}
	for rows.Next() {
		item, err := scanArgoCDBaselineOwnershipDecision(rows)
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

const upsertArgoCDBaselineOwnershipDecision = `-- name: UpsertArgoCDBaselineOwnershipDecision :one
INSERT INTO argocd_baseline_ownership_decisions (
    cluster_id,
    component_slug,
    decision,
    reason,
    expires_at,
    decided_by_id
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id, component_slug) DO UPDATE SET
    decision = EXCLUDED.decision,
    reason = EXCLUDED.reason,
    expires_at = EXCLUDED.expires_at,
    decided_by_id = EXCLUDED.decided_by_id,
    updated_at = now()
RETURNING ` + argoCDBaselineOwnershipDecisionColumns

func (q *Queries) UpsertArgoCDBaselineOwnershipDecision(ctx context.Context, arg UpsertArgoCDBaselineOwnershipDecisionParams) (ArgocdBaselineOwnershipDecision, error) {
	row := q.db.QueryRow(ctx, upsertArgoCDBaselineOwnershipDecision,
		arg.ClusterID,
		arg.ComponentSlug,
		arg.Decision,
		arg.Reason,
		arg.ExpiresAt,
		arg.DecidedByID,
	)
	return scanArgoCDBaselineOwnershipDecision(row)
}
