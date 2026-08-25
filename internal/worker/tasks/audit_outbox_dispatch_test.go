package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeAuditOutboxQuerier struct {
	rows          []sqlc.AuditOutbox
	resetCalls    int
	claimArg      sqlc.ClaimDueAuditOutboxParams
	deliverArgs   []sqlc.DeliverAuditOutboxParams
	failedArgs    []sqlc.MarkAuditOutboxFailedParams
	deliverErr    error
	markErr       error
	returnedState string
}

func (f *fakeAuditOutboxQuerier) ResetExpiredAuditOutboxLeases(context.Context, time.Time) (int64, error) {
	f.resetCalls++
	return 0, nil
}

func (f *fakeAuditOutboxQuerier) ClaimDueAuditOutbox(_ context.Context, arg sqlc.ClaimDueAuditOutboxParams) ([]sqlc.AuditOutbox, error) {
	f.claimArg = arg
	return f.rows, nil
}

func (f *fakeAuditOutboxQuerier) DeliverAuditOutbox(_ context.Context, arg sqlc.DeliverAuditOutboxParams) (sqlc.DeliverAuditOutboxRow, error) {
	f.deliverArgs = append(f.deliverArgs, arg)
	if f.deliverErr != nil {
		return sqlc.DeliverAuditOutboxRow{}, f.deliverErr
	}
	row := f.rows[0]
	return sqlc.DeliverAuditOutboxRow{
		ID: row.ID, EventCreatedAt: row.EventCreatedAt, Action: row.Action,
		ResourceType: row.ResourceType, Detail: row.Detail, ActionClass: row.ActionClass,
		Source: row.Source, Status: "delivered",
	}, nil
}

func (f *fakeAuditOutboxQuerier) MarkAuditOutboxFailed(_ context.Context, arg sqlc.MarkAuditOutboxFailedParams) (sqlc.AuditOutbox, error) {
	f.failedArgs = append(f.failedArgs, arg)
	if f.markErr != nil {
		return sqlc.AuditOutbox{}, f.markErr
	}
	state := f.returnedState
	if state == "" {
		state = "failed"
	}
	return sqlc.AuditOutbox{ID: arg.OutboxID, Status: state, AttemptCount: 1}, nil
}

func auditOutboxTestRow() sqlc.AuditOutbox {
	return sqlc.AuditOutbox{
		ID: uuid.New(), EventCreatedAt: time.Now().UTC(), Action: "cluster.delete",
		ResourceType: "cluster", Detail: []byte(`{"phase":"committed"}`),
		ActionClass: "mutation", Source: "service", AttemptCount: 1, MaxAttempts: 20,
	}
}

func TestDispatchAuditOutboxOnceDeliversClaimedRows(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	q := &fakeAuditOutboxQuerier{rows: []sqlc.AuditOutbox{auditOutboxTestRow()}}
	if err := DispatchAuditOutboxOnce(context.Background(), AuditOutboxDispatchDeps{
		Queries: q, Now: func() time.Time { return now },
	}); err != nil {
		t.Fatalf("DispatchAuditOutboxOnce: %v", err)
	}
	if q.resetCalls != 1 || len(q.deliverArgs) != 1 || len(q.failedArgs) != 0 {
		t.Fatalf("dispatch calls reset=%d deliver=%d failed=%d", q.resetCalls, len(q.deliverArgs), len(q.failedArgs))
	}
	if q.claimArg.BatchLimit != auditOutboxDispatchBatchSize || q.claimArg.LockedUntil.Time != now.Add(auditOutboxLease) {
		t.Fatalf("claim args = %#v", q.claimArg)
	}
}

func TestDispatchAuditOutboxOnceSchedulesRetry(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	q := &fakeAuditOutboxQuerier{
		rows: []sqlc.AuditOutbox{auditOutboxTestRow()}, deliverErr: errors.New("postgres write failed"),
	}
	if err := DispatchAuditOutboxOnce(context.Background(), AuditOutboxDispatchDeps{
		Queries: q, Now: func() time.Time { return now },
	}); err != nil {
		t.Fatalf("DispatchAuditOutboxOnce should isolate row failures: %v", err)
	}
	if len(q.failedArgs) != 1 || q.failedArgs[0].LastError != "postgres write failed" {
		t.Fatalf("failed args = %#v", q.failedArgs)
	}
	if q.failedArgs[0].NextAttemptAt != now.Add(2*time.Second) {
		t.Fatalf("next attempt = %s", q.failedArgs[0].NextAttemptAt)
	}
}

func TestDispatchAuditOutboxOnceFailsClosedWhenUnconfigured(t *testing.T) {
	if err := DispatchAuditOutboxOnce(context.Background(), AuditOutboxDispatchDeps{}); err == nil {
		t.Fatal("unconfigured audit outbox dispatcher returned nil")
	}
}

func TestAuditOutboxBackoffCaps(t *testing.T) {
	if got := auditOutboxBackoff(1); got != 2*time.Second {
		t.Fatalf("backoff(1) = %s", got)
	}
	if got := auditOutboxBackoff(99); got != 256*time.Second {
		t.Fatalf("backoff(99) = %s", got)
	}
}
