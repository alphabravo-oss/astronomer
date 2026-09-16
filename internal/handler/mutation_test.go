package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type mutationTestTx struct {
	audit.OutboxQuerier
	state       int
	events      []sqlc.UpsertAuditOutboxParams
	failAuditAt int
}

func (q *mutationTestTx) UpsertAuditOutbox(_ context.Context, p sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.failAuditAt == len(q.events)+1 {
		return sqlc.AuditOutbox{}, errors.New("outbox unavailable")
	}
	q.events = append(q.events, p)
	return sqlc.AuditOutbox{ID: p.ID}, nil
}

func TestExecuteMutationRequiresTransactionBeforeSideEffects(t *testing.T) {
	called := false
	var runTx func(context.Context, func(*mutationTestTx) error) error
	result, err := executeMutation(httptest.NewRequest("POST", "/mutation", nil), runTx,
		func(q *mutationTestTx) (int, error) { called = true; return 7, nil },
		func(int) mutationAuditEvent { return mutationAuditEvent{action: "mutated"} })
	if !errors.Is(err, audit.ErrOutboxUnavailable) || called || result != 0 {
		t.Fatalf("result=%d err=%v mutation called=%v", result, err, called)
	}
}

func TestExecuteMutationCommitsAllEvidenceOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mutationErr bool
		failAuditAt int
		commitErr   bool
		wantCommit  bool
	}{
		{name: "all events", wantCommit: true},
		{name: "mutation error", mutationErr: true},
		{name: "second audit error", failAuditAt: 2},
		{name: "commit error", commitErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedState, committedEvents := 0, 0
			tx := &mutationTestTx{failAuditAt: tc.failAuditAt}
			runTx := func(_ context.Context, fn func(*mutationTestTx) error) error {
				if err := fn(tx); err != nil {
					return err
				}
				if tc.commitErr {
					return errors.New("commit failed")
				}
				committedState = tx.state
				committedEvents = len(tx.events)
				return nil
			}
			result, err := executeMutation(httptest.NewRequest("POST", "/mutation", nil), runTx,
				func(q *mutationTestTx) (int, error) {
					q.state = 7
					if tc.mutationErr {
						return 7, errors.New("mutation failed")
					}
					return 7, nil
				},
				func(int) []mutationAuditEvent {
					return []mutationAuditEvent{{action: "group.created", resourceType: "group", resourceID: "g"}, {action: "group.member.added", resourceType: "member", resourceID: "m"}}
				})
			if tc.wantCommit {
				if err != nil || result != 7 || committedState != 7 || committedEvents != 2 {
					t.Fatalf("result=%d err=%v committed=%d/%d", result, err, committedState, committedEvents)
				}
			} else {
				if err == nil || result != 0 || committedState != 0 || committedEvents != 0 {
					t.Fatalf("failed transaction exposed state: result=%d err=%v committed=%d/%d", result, err, committedState, committedEvents)
				}
			}
			if tc.mutationErr && len(tx.events) != 0 {
				t.Fatal("audit emitted after failed mutation")
			}
		})
	}
}
