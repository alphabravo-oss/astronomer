package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const repairJobGlobalScope = "global"

type repairJobStateRecorder interface {
	RecordRepairJobSuccess(ctx context.Context, arg sqlc.RecordRepairJobSuccessParams) (sqlc.RepairJobState, error)
	RecordRepairJobFailure(ctx context.Context, arg sqlc.RecordRepairJobFailureParams) (sqlc.RepairJobState, error)
}

func recordRepairJobSuccess(ctx context.Context, q any, jobName string, metadata map[string]any) {
	recorder, ok := q.(repairJobStateRecorder)
	if !ok {
		return
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		payload = []byte(`{}`)
	}
	if _, err := recorder.RecordRepairJobSuccess(ctx, sqlc.RecordRepairJobSuccessParams{
		JobName:  strings.TrimSpace(jobName),
		Scope:    repairJobGlobalScope,
		Metadata: payload,
	}); err != nil {
		runtimeLogger().WarnContext(ctx, "record repair job success failed", "job", jobName, "error", err)
	}
}

func recordRepairJobFailure(ctx context.Context, q any, jobName string, runErr error, metadata map[string]any) {
	if runErr == nil {
		return
	}
	recorder, ok := q.(repairJobStateRecorder)
	if !ok {
		return
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		payload = []byte(`{}`)
	}
	if _, err := recorder.RecordRepairJobFailure(ctx, sqlc.RecordRepairJobFailureParams{
		JobName:   strings.TrimSpace(jobName),
		Scope:     repairJobGlobalScope,
		LastError: fmt.Sprint(runErr),
		Metadata:  payload,
	}); err != nil {
		runtimeLogger().WarnContext(ctx, "record repair job failure failed", "job", jobName, "error", err)
	}
}
