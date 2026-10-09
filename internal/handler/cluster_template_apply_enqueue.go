package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type clusterTemplateApplicationTaskOutboxQuerier interface {
	UpsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error)
}

// A template reapply is a new convergence intent even when the cluster and
// template body are unchanged (the point may be to repair runtime drift).
// Give each intent its own outbox identity; reusing one permanent per-cluster
// key turns every reapply after the first delivered task into a silent no-op.
func clusterTemplateApplyDedupeKey(clusterID uuid.UUID) string {
	return fmt.Sprintf("cluster_template_apply:%s:%s", clusterID.String(), uuid.NewString())
}

func clusterTemplateRequestDedupeKey(r *http.Request, clusterID uuid.UUID) string {
	if r == nil {
		return clusterTemplateApplyDedupeKey(clusterID)
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return clusterTemplateApplyDedupeKey(clusterID)
	}
	return "cluster_template_apply:" + audit.MutationDedupeKey(key, r.Method+":"+r.URL.Path, "cluster", clusterID.String())
}

func upsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, q any, outbox tasks.TaskOutboxWriter, app sqlc.UpsertClusterTemplateApplicationParams, task *asynq.Task, opts tasks.TaskOutboxOptions) (sqlc.ClusterTemplateApplication, bool, error) {
	atomicQ, ok := q.(clusterTemplateApplicationTaskOutboxQuerier)
	if !ok || outbox == nil || task == nil {
		return sqlc.ClusterTemplateApplication{}, false, nil
	}
	// This atomic path historically rejected negative retries through the SQL
	// check; retain that behavior before using the generic writer's v1 defaults.
	if opts.MaxRetry < 0 {
		return sqlc.ClusterTemplateApplication{}, true, &jsoncontract.Error{Field: "task_options", Category: "out_of_range"}
	}
	prepared, err := tasks.PrepareTaskOutbox(ctx, task, opts)
	if err != nil {
		return sqlc.ClusterTemplateApplication{}, true, err
	}
	row, err := atomicQ.UpsertClusterTemplateApplicationWithTaskOutbox(ctx, sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams{
		ClusterID:           app.ClusterID,
		TemplateID:          app.TemplateID,
		SpecSnapshot:        app.SpecSnapshot,
		DedupeKey:           prepared.DedupeKey,
		TaskType:            prepared.TaskType,
		Payload:             prepared.Payload,
		QueueName:           prepared.QueueName,
		MaxRetry:            prepared.MaxRetry,
		TimeoutSeconds:      prepared.TimeoutSeconds,
		UniqueSeconds:       prepared.UniqueSeconds,
		MaxDeliveryAttempts: prepared.MaxDeliveryAttempts,
		NextAttemptAt:       prepared.NextAttemptAt,
	})
	return row, true, err
}
