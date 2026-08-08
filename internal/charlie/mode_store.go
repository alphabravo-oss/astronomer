package charlie

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// PGModeStore uses revision-checked updates and makes emergency disable atomic
// with cancellation/fencing of all outstanding Charlie work.
type PGModeStore struct{ Pool *pgxpool.Pool }

func (p PGModeStore) LoadModeState(ctx context.Context) (ModeState, error) {
	row, err := sqlc.New(p.Pool).GetActiveCharlieConnection(ctx)
	if err != nil {
		return ModeState{}, err
	}
	return dbModeState(row), nil
}
func (p PGModeStore) SetRequestedMode(ctx context.Context, connectionID string, mode Mode, expected int64) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET requested_mode=$1, verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$2 AND active=true AND emergency_disabled=false AND verified_mode_revision=$3`, string(mode), id, expected)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func (p PGModeStore) SetVerifiedMode(ctx context.Context, connectionID string, mode Mode, expected, next int64, digest string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET verified_mode=$1, verified_mode_revision=$2, disclosure_digest=$3::VARCHAR(128), acknowledged_disclosure_digest=CASE WHEN disclosure_digest=$3::VARCHAR(128) THEN acknowledged_disclosure_digest ELSE '' END, last_verified_at=now(), updated_at=now() WHERE id=$4 AND active=true AND emergency_disabled=false AND verified_mode_revision=$5`, string(mode), next, digest, id, expected)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func (p PGModeStore) SetEmergencyDisabled(ctx context.Context, connectionID, actorID string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ModeState{}, err
	}
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ModeState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE charlie_connections SET emergency_disabled=true, emergency_disabled_by_id=$1, emergency_disabled_at=now(), requested_mode='disabled', verified_mode='disabled', verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$2 AND active=true`, actor, id)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	for _, statement := range []string{
		`UPDATE charlie_sessions SET state='aborted', completed_at=now(), updated_at=now() WHERE connection_id=$1 AND state IN ('creating','active','waiting_approval')`,
		`UPDATE charlie_action_approvals SET state='rejected', updated_at=now() WHERE connection_id=$1 AND state='approved'`,
		`UPDATE charlie_action_receipts SET state='fenced', updated_at=now() WHERE connection_id=$1 AND state IN ('claimed','waiting_approval','dispatched','ambiguous','verifying')`,
		`UPDATE charlie_trigger_events SET state='suppressed', updated_at=now() WHERE state IN ('pending','retry','dispatching') AND rule_id IN (SELECT id FROM charlie_trigger_rules WHERE connection_id=$1)`,
		`UPDATE charlie_delegations SET revoked_at=now() WHERE revoked_at IS NULL AND session_id IN (SELECT id FROM charlie_sessions WHERE connection_id=$1)`,
	} {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return ModeState{}, err
		}
	}
	row, err := sqlc.New(tx).GetCharlieConnection(ctx, id)
	if err != nil {
		return ModeState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModeState{}, err
	}
	return dbModeState(row), nil
}
func (p PGModeStore) ClearEmergencyDisabled(ctx context.Context, connectionID, actorID string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET emergency_disabled=false, emergency_disabled_by_id=NULL, emergency_disabled_at=NULL, requested_mode='disabled', verified_mode='disabled', verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$1 AND active=true AND emergency_disabled=true AND verified_mode='disabled'`, id)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func dbModeState(row sqlc.CharlieConnection) ModeState {
	return ModeState{ConnectionID: row.ID.String(), Active: row.Active, EmergencyDisabled: row.EmergencyDisabled, Requested: Mode(row.RequestedMode), Verified: Mode(row.VerifiedMode), Revision: row.VerifiedModeRevision, DisclosureDigest: row.DisclosureDigest, UpdatedAt: row.UpdatedAt}
}
