package sqlc

import (
	"context"

	"github.com/google/uuid"
)

// SetMustChangePassword marks a newly bootstrapped administrator for forced
// credential rotation when the Helm install explicitly requests it.
func (q *Queries) SetMustChangePassword(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE users SET must_change_password = true, updated_at = now() WHERE id = $1`, id)
	return err
}
