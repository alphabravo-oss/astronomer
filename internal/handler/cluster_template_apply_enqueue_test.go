package handler

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

type invalidTemplateTaskWriter struct{ calls int }

func (w *invalidTemplateTaskWriter) UpsertTaskOutbox(context.Context, sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	w.calls++
	return sqlc.TaskOutbox{}, nil
}
func (w *invalidTemplateTaskWriter) UpsertClusterTemplateApplicationWithTaskOutbox(context.Context, sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error) {
	w.calls++
	return sqlc.ClusterTemplateApplication{}, nil
}

func TestAtomicClusterTemplateTaskRejectsInvalidPayloadAndOptionsBeforeSQL(t *testing.T) {
	for _, fixture := range []struct {
		raw  string
		opts tasks.TaskOutboxOptions
	}{
		{`null`, tasks.TaskOutboxOptions{}},
		{`{} {}`, tasks.TaskOutboxOptions{}},
		{`{}`, tasks.TaskOutboxOptions{MaxRetry: 1 << 31}},
		{`{}`, tasks.TaskOutboxOptions{MaxRetry: -1}},
		{`{}`, tasks.TaskOutboxOptions{MaxDeliveryAttempts: 1 << 31}},
		{`{}`, tasks.TaskOutboxOptions{Timeout: time.Duration(1<<31) * time.Second}},
		{`{}`, tasks.TaskOutboxOptions{Unique: time.Duration(1<<31) * time.Second}},
	} {
		w := &invalidTemplateTaskWriter{}
		_, atomic, err := upsertClusterTemplateApplicationWithTaskOutbox(t.Context(), w, w, sqlc.UpsertClusterTemplateApplicationParams{}, asynq.NewTask("example:work", []byte(fixture.raw)), fixture.opts)
		if !atomic || err == nil || w.calls != 0 {
			t.Errorf("invalid atomic task reached SQL: atomic=%t err=%v calls=%d", atomic, err, w.calls)
		}
	}
}
