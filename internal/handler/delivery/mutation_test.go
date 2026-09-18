package delivery

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

func TestMutationFailsClosedWhenTransactionIsUnavailable(t *testing.T) {
	called := false
	var runTx func(context.Context, func(*sourceQueryFake) error) error
	result, err := executeMutation(httptest.NewRequest("POST", "/delivery/sources", nil), runTx,
		func(*sourceQueryFake) (int, error) { called = true; return 1, nil },
		func(int) deliveryAuditEvent { return deliveryAuditEvent{action: "delivery.source.created"} })
	if !errors.Is(err, audit.ErrOutboxUnavailable) || called || result != 0 {
		t.Fatalf("missing transaction: result=%d error=%v called=%v", result, err, called)
	}
}

func TestMutationDoesNotReturnStateAfterCommitFailure(t *testing.T) {
	failure := errors.New("commit failed")
	runTx := func(_ context.Context, fn func(*sourceQueryFake) error) error {
		if err := fn(&sourceQueryFake{}); err != nil {
			return err
		}
		return failure
	}
	result, err := executeMutation(httptest.NewRequest("POST", "/delivery/sources", nil), runTx,
		func(*sourceQueryFake) (int, error) { return 1, nil },
		func(int) deliveryAuditEvent {
			return deliveryAuditEvent{action: "delivery.source.created", resourceType: "delivery_source", resourceID: "s"}
		})
	if !errors.Is(err, failure) || result != 0 {
		t.Fatalf("failed commit exposed result: result=%d error=%v", result, err)
	}
}
