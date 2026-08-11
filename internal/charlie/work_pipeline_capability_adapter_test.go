package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type workPipelineQueriesFake struct {
	workPipelineQueries
	row      sqlc.TaskOutbox
	filtered sqlc.ListTaskOutboxFilteredParams
	retries  int
}

func (f *workPipelineQueriesFake) ListTaskOutboxFiltered(_ context.Context, input sqlc.ListTaskOutboxFilteredParams) ([]sqlc.TaskOutbox, error) {
	f.filtered = input
	return []sqlc.TaskOutbox{f.row}, nil
}

func (f *workPipelineQueriesFake) GetTaskOutbox(context.Context, uuid.UUID) (sqlc.TaskOutbox, error) {
	return f.row, nil
}

func (f *workPipelineQueriesFake) RetryTaskOutbox(_ context.Context, _ sqlc.RetryTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.retries++
	f.row.Status = "pending"
	f.row.LastError = ""
	f.row.LockedUntil = pgtype.Timestamptz{}
	return f.row, nil
}

func TestWorkPipelineOutboxFilteringAndSanitization(t *testing.T) {
	now := time.Now().UTC()
	fake := &workPipelineQueriesFake{row: sqlc.TaskOutbox{
		ID: uuid.New(), DedupeKey: pgtype.Text{String: "secret=SENTINEL", Valid: true},
		TaskType: "catalog:sync", Payload: []byte(`{"api_key":"SENTINEL","repository_id":"repo-a"}`),
		QueueName: "default", Status: "failed", LastError: "password=SENTINEL connection refused",
		CreatedAt: now, UpdatedAt: now,
	}}
	adapter, err := NewWorkPipelineCapabilityAdapter(fake)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ := capabilityByName("astronomer.task_outbox.list")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{
		"status": "failed", "task_type": "catalog:sync", "page": 2, "page_size": 10,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if fake.filtered.Status != "failed" || fake.filtered.TaskType != "catalog:sync" || fake.filtered.Limit != 10 || fake.filtered.Offset != 10 {
		t.Fatalf("filter=%+v", fake.filtered)
	}
	text := string(result)
	if strings.Contains(text, "SENTINEL") || strings.Contains(text, "connection refused") || !strings.Contains(text, `"payload_fields":["api_key","repository_id"]`) || !strings.Contains(text, `"failure_code"`) {
		t.Fatalf("unsafe or incomplete outbox result: %s", result)
	}
}

func TestWorkPipelineRetryDeliveryRequiresTerminalFailureAndVerifies(t *testing.T) {
	fake := &workPipelineQueriesFake{row: sqlc.TaskOutbox{ID: uuid.New(), TaskType: "catalog:sync", QueueName: "default", Status: "dead", LastError: "timeout", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	adapter, _ := NewWorkPipelineCapabilityAdapter(fake)
	adapter.now = func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) }
	descriptor, _ := capabilityByName("astronomer.task_outbox.retry_delivery")
	arguments := rawArguments(t, map[string]any{"resource_id": "local", "outbox_id": fake.row.ID.String(), "operation_id": "retry-a"})
	result, err := adapter.Execute(context.Background(), descriptor, arguments)
	if err != nil || !json.Valid(result) || fake.retries != 1 {
		t.Fatalf("result=%s retries=%d err=%v", result, fake.retries, err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, arguments, result)
	if err != nil || !verified {
		t.Fatalf("verified=%t err=%v", verified, err)
	}
	fake.row.Status = "pending"
	if _, err := adapter.Execute(context.Background(), descriptor, arguments); err == nil || fake.retries != 1 {
		t.Fatal("non-terminal outbox record was retried")
	}
}
