package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

// mutationAuditEvent describes the durable evidence committed with a mutation.
type mutationAuditEvent struct {
	action       string
	resourceType string
	resourceID   string
	resourceName string
	status       int
	detail       map[string]any
}

// executeMutation commits domain state and its audit evidence through the same
// transaction-bound querier. Missing transaction wiring fails before mutation.
// A failed transaction never returns a result that a caller could mistake for
// committed state. Descriptors may return multiple events for batch mutations.
func executeMutation[Q audit.OutboxQuerier, T any, E mutationAuditEvent | []mutationAuditEvent](
	r *http.Request,
	runTx func(context.Context, func(Q) error) error,
	mutate func(Q) (T, error),
	describe func(T) E,
) (T, error) {
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
		var events []mutationAuditEvent
		switch value := any(describe(result)).(type) {
		case mutationAuditEvent:
			events = []mutationAuditEvent{value}
		case []mutationAuditEvent:
			events = value
		}
		for _, event := range events {
			// An empty descriptor denotes an idempotent replay with no state change.
			if event.action == "" {
				continue
			}
			if err := recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}
