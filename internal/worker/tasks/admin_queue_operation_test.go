package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeAdminQueueOperationQuerier struct {
	row           sqlc.AdminQueueOperation
	claimErr      error
	getErr        error
	markFailedErr error
	markSuccess   int
	failedCode    string
	retryingCode  string
	claimParams   sqlc.ClaimAdminQueueOperationParams
}

func (q *fakeAdminQueueOperationQuerier) ClaimAdminQueueOperation(_ context.Context, arg sqlc.ClaimAdminQueueOperationParams) (sqlc.AdminQueueOperation, error) {
	q.claimParams = arg
	if q.claimErr != nil {
		return sqlc.AdminQueueOperation{}, q.claimErr
	}
	q.row.Status = "running"
	q.row.AttemptCount++
	return q.row, nil
}
func (q *fakeAdminQueueOperationQuerier) GetAdminQueueOperation(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	return q.row, q.getErr
}
func (q *fakeAdminQueueOperationQuerier) MarkAdminQueueOperationEffectStarted(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	q.row.EffectStartedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	return q.row, nil
}
func (q *fakeAdminQueueOperationQuerier) MarkAdminQueueOperationSucceeded(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	q.markSuccess++
	q.row.Status = "succeeded"
	return q.row, nil
}
func (q *fakeAdminQueueOperationQuerier) MarkAdminQueueOperationFailed(_ context.Context, arg sqlc.MarkAdminQueueOperationFailedParams) (sqlc.AdminQueueOperation, error) {
	q.failedCode = arg.LastError
	q.row.Status = "failed"
	return q.row, q.markFailedErr
}
func (q *fakeAdminQueueOperationQuerier) MarkAdminQueueOperationRetrying(_ context.Context, arg sqlc.MarkAdminQueueOperationRetryingParams) (sqlc.AdminQueueOperation, error) {
	q.retryingCode = arg.LastError
	q.row.Status = "retrying"
	return q.row, q.markFailedErr
}

type fakeAdminQueueOperationInspector struct {
	states    []asynq.TaskState
	getErrors []error
	gets      int
	runs      int
	deletes   int
	runErr    error
	deleteErr error
	beforeRun func()
}

func (i *fakeAdminQueueOperationInspector) GetTaskInfo(_, _ string) (*asynq.TaskInfo, error) {
	index := i.gets
	i.gets++
	if index < len(i.getErrors) && i.getErrors[index] != nil {
		return nil, i.getErrors[index]
	}
	state := asynq.TaskStatePending
	if index < len(i.states) {
		state = i.states[index]
	}
	return &asynq.TaskInfo{State: state}, nil
}
func (i *fakeAdminQueueOperationInspector) RunTask(_, _ string) error {
	if i.beforeRun != nil {
		i.beforeRun()
	}
	i.runs++
	return i.runErr
}
func (i *fakeAdminQueueOperationInspector) DeleteTask(_, _ string) error {
	i.deletes++
	return i.deleteErr
}

func adminQueueOperationFixture(action string) sqlc.AdminQueueOperation {
	return sqlc.AdminQueueOperation{
		ID: uuid.New(), Action: action, QueueName: "critical", TaskID: "task-123", Status: "pending",
	}
}

func TestAdminQueueOperationTaskPayloadContainsOnlyOperationID(t *testing.T) {
	opID := uuid.New()
	task, err := NewAdminQueueOperationTask(opID)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload["operation_id"] != opID.String() {
		t.Fatalf("payload = %#v, want identifier-only operation receipt", payload)
	}
	for _, forbidden := range []string{"queue", "task_id", "payload", "last_err", "secret", "token"} {
		if strings.Contains(string(task.Payload()), forbidden) {
			t.Fatalf("task payload contains forbidden field %q: %s", forbidden, task.Payload())
		}
	}
}

func TestApplyAdminQueueRetryMutatesThenObserves(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{
		states: []asynq.TaskState{asynq.TaskStateArchived, asynq.TaskStatePending},
		beforeRun: func() {
			if !q.row.EffectStartedAt.Valid {
				t.Fatal("Redis effect ran before durable effect_started_at phase")
			}
		},
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector, Now: func() time.Time { return now }}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.runs != 1 || inspector.deletes != 0 || inspector.gets != 2 || q.markSuccess != 1 {
		t.Fatalf("runs=%d deletes=%d gets=%d successes=%d", inspector.runs, inspector.deletes, inspector.gets, q.markSuccess)
	}
	if !q.claimParams.LockedUntil.Valid || q.claimParams.LockedUntil.Time != now.Add(2*time.Minute) {
		t.Fatalf("claim lease = %+v", q.claimParams.LockedUntil)
	}
}

func TestApplyAdminQueueReplayAfterPhaseAppliesStillArchivedRetry(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	row.Status = "retrying"
	row.EffectStartedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{states: []asynq.TaskState{asynq.TaskStateArchived, asynq.TaskStatePending}}
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.runs != 1 || inspector.gets != 2 || q.markSuccess != 1 {
		t.Fatalf("replay runs=%d gets=%d successes=%d", inspector.runs, inspector.gets, q.markSuccess)
	}
}

func TestApplyAdminQueueReplayAfterPhaseTreatsConsumedRetryAsConverged(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	row.Status = "retrying"
	row.EffectStartedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{getErrors: []error{asynq.ErrTaskNotFound}}
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.runs != 0 || inspector.gets != 1 || q.markSuccess != 1 || q.failedCode != "" {
		t.Fatalf("replay runs=%d gets=%d successes=%d failed=%q", inspector.runs, inspector.gets, q.markSuccess, q.failedCode)
	}
}

func TestApplyAdminQueueRecoveryObservesCompletedEffectWithoutRepeating(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	row.Status = "retrying"
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{states: []asynq.TaskState{asynq.TaskStatePending}}
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.runs != 0 || q.markSuccess != 1 {
		t.Fatalf("recovery repeated effect: runs=%d success=%d", inspector.runs, q.markSuccess)
	}
}

func TestApplyAdminQueueDiscardAlreadyAbsentIsIdempotent(t *testing.T) {
	row := adminQueueOperationFixture("discard")
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{getErrors: []error{asynq.ErrTaskNotFound}}
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.deletes != 0 || q.markSuccess != 1 {
		t.Fatalf("deletes=%d success=%d", inspector.deletes, q.markSuccess)
	}
}

func TestApplyAdminQueueRetryMissingPersistsSanitizedFailure(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{getErrors: []error{asynq.ErrTaskNotFound}}
	err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID)
	if !errors.Is(err, asynq.SkipRetry) || q.failedCode != "target_not_found" || q.retryingCode != "" || q.markSuccess != 0 {
		t.Fatalf("err=%v failed_code=%q successes=%d", err, q.failedCode, q.markSuccess)
	}
}

func TestApplyAdminQueueDoesNotPersistRawExternalErrors(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	q := &fakeAdminQueueOperationQuerier{row: row}
	inspector := &fakeAdminQueueOperationInspector{
		states: []asynq.TaskState{asynq.TaskStateArchived}, runErr: errors.New("redis://user:super-secret@example"),
	}
	err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID)
	if err == nil {
		t.Fatal("expected external effect failure")
	}
	if q.retryingCode != "effect_failed" || q.failedCode != "" || strings.Contains(q.retryingCode, "secret") {
		t.Fatalf("persisted retry=%q failure=%q", q.retryingCode, q.failedCode)
	}
	if got := err.Error(); got != "admin queue operation failed: effect_failed" || strings.Contains(got, "super-secret") || strings.Contains(got, "redis://") {
		t.Fatalf("archive-visible error was not sanitized: %q", got)
	}
}

func TestApplyAdminQueueNeverReturnsRawInspectorErrors(t *testing.T) {
	secretErr := errors.New("redis://operator:archive-visible-secret@example.internal/0")
	tests := []struct {
		name      string
		inspector *fakeAdminQueueOperationInspector
		wantCode  string
	}{
		{name: "inspect", inspector: &fakeAdminQueueOperationInspector{getErrors: []error{secretErr}}, wantCode: "inspect_failed"},
		{name: "effect", inspector: &fakeAdminQueueOperationInspector{states: []asynq.TaskState{asynq.TaskStateArchived}, runErr: secretErr}, wantCode: "effect_failed"},
		{name: "observe", inspector: &fakeAdminQueueOperationInspector{states: []asynq.TaskState{asynq.TaskStateArchived}, getErrors: []error{nil, secretErr}}, wantCode: "observe_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := adminQueueOperationFixture("retry")
			q := &fakeAdminQueueOperationQuerier{row: row}
			err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: test.inspector}, row.ID)
			if err == nil {
				t.Fatal("expected inspector failure")
			}
			if got := err.Error(); got != "admin queue operation failed: "+test.wantCode || strings.Contains(got, "archive-visible-secret") || strings.Contains(got, "redis://") {
				t.Fatalf("archive-visible error = %q", got)
			}
			if q.retryingCode != test.wantCode || q.failedCode != "" {
				t.Fatalf("persisted retry=%q failure=%q", q.retryingCode, q.failedCode)
			}
		})
	}
}

func TestApplyAdminQueueHeldLeaseDoesNotExecuteEffect(t *testing.T) {
	row := adminQueueOperationFixture("discard")
	row.Status = "running"
	q := &fakeAdminQueueOperationQuerier{row: row, claimErr: pgx.ErrNoRows}
	inspector := &fakeAdminQueueOperationInspector{}
	err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID)
	if err == nil || inspector.gets != 0 || inspector.deletes != 0 || q.markSuccess != 0 {
		t.Fatalf("err=%v gets=%d deletes=%d success=%d", err, inspector.gets, inspector.deletes, q.markSuccess)
	}
}

func TestApplyAdminQueueSucceededReceiptIsNoOp(t *testing.T) {
	row := adminQueueOperationFixture("retry")
	row.Status = "succeeded"
	q := &fakeAdminQueueOperationQuerier{row: row, claimErr: pgx.ErrNoRows}
	inspector := &fakeAdminQueueOperationInspector{}
	if err := applyAdminQueueOperation(context.Background(), AdminQueueOperationDeps{Queries: q, Inspector: inspector}, row.ID); err != nil {
		t.Fatal(err)
	}
	if inspector.gets != 0 || inspector.runs != 0 {
		t.Fatalf("completed receipt touched Redis: gets=%d runs=%d", inspector.gets, inspector.runs)
	}
}
