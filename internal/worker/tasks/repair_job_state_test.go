package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeRepairJobStateRecorder struct {
	success []sqlc.RecordRepairJobSuccessParams
	failure []sqlc.RecordRepairJobFailureParams
}

func (f *fakeRepairJobStateRecorder) RecordRepairJobSuccess(_ context.Context, arg sqlc.RecordRepairJobSuccessParams) (sqlc.RepairJobState, error) {
	f.success = append(f.success, arg)
	return sqlc.RepairJobState{JobName: arg.JobName, Scope: arg.Scope, Status: "success", Metadata: arg.Metadata}, nil
}

func (f *fakeRepairJobStateRecorder) RecordRepairJobFailure(_ context.Context, arg sqlc.RecordRepairJobFailureParams) (sqlc.RepairJobState, error) {
	f.failure = append(f.failure, arg)
	return sqlc.RepairJobState{JobName: arg.JobName, Scope: arg.Scope, Status: "failed", LastError: arg.LastError, Metadata: arg.Metadata}, nil
}

func TestRecordRepairJobSuccessWritesGlobalState(t *testing.T) {
	recorder := &fakeRepairJobStateRecorder{}

	recordRepairJobSuccess(context.Background(), recorder, "crd:ownership_drift_check", map[string]any{"checked_clusters": 3})

	if len(recorder.success) != 1 {
		t.Fatalf("success records = %d, want 1", len(recorder.success))
	}
	got := recorder.success[0]
	if got.JobName != "crd:ownership_drift_check" || got.Scope != repairJobGlobalScope {
		t.Fatalf("success params = %+v", got)
	}
	var metadata map[string]int
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	if metadata["checked_clusters"] != 3 {
		t.Fatalf("metadata = %+v, want checked_clusters=3", metadata)
	}
}

func TestRecordRepairJobFailureWritesLastError(t *testing.T) {
	recorder := &fakeRepairJobStateRecorder{}

	recordRepairJobFailure(context.Background(), recorder, "argocd:auto_register_cluster", errors.New("list clusters: database unavailable"), map[string]any{"mode": "sweep"})

	if len(recorder.failure) != 1 {
		t.Fatalf("failure records = %d, want 1", len(recorder.failure))
	}
	got := recorder.failure[0]
	if got.JobName != "argocd:auto_register_cluster" || got.Scope != repairJobGlobalScope {
		t.Fatalf("failure params = %+v", got)
	}
	if !strings.Contains(got.LastError, "database unavailable") {
		t.Fatalf("last error = %q", got.LastError)
	}
}

func TestRecordRepairJobIgnoresNonRecorder(t *testing.T) {
	recordRepairJobSuccess(context.Background(), struct{}{}, "job", nil)
	recordRepairJobFailure(context.Background(), struct{}{}, "job", errors.New("boom"), nil)
}
