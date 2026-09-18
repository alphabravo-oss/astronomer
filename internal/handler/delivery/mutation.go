package delivery

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

// executeMutation commits domain state and delivery audit intent atomically.
// Missing wiring fails before invoking the mutation; failed transactions never
// expose a result that could be mistaken for committed state.
func executeMutation[Q audit.OutboxQuerier, T any](r *http.Request, runTx func(context.Context, func(Q) error) error, mutate func(Q) (T, error), describe func(T) deliveryAuditEvent) (T, error) {
	var zero T
	if r == nil || runTx == nil || mutate == nil || describe == nil {
		return zero, audit.ErrOutboxUnavailable
	}
	var result T
	err := runTx(r.Context(), func(q Q) error {
		var err error
		result, err = mutate(q)
		if err != nil {
			return err
		}
		return recordAuditOutbox(r, q, describe(result))
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}
