package tasks

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type auditExportTaskStore struct {
	row       sqlc.AuditExportOperation
	succeeded *sqlc.MarkAuditExportOperationSucceededParams
}

func (s *auditExportTaskStore) ClaimAuditExportOperation(context.Context, sqlc.ClaimAuditExportOperationParams) (sqlc.AuditExportOperation, error) {
	s.row.Status = "running"
	return s.row, nil
}

func (s *auditExportTaskStore) GetAuditExportOperation(context.Context, uuid.UUID) (sqlc.GetAuditExportOperationRow, error) {
	return sqlc.GetAuditExportOperationRow{ID: s.row.ID, Status: s.row.Status, ExpiresAt: s.row.ExpiresAt}, nil
}

func (s *auditExportTaskStore) MarkAuditExportOperationSucceeded(_ context.Context, arg sqlc.MarkAuditExportOperationSucceededParams) (sqlc.AuditExportOperation, error) {
	s.succeeded = &arg
	s.row.Status = "succeeded"
	return s.row, nil
}

func (s *auditExportTaskStore) MarkAuditExportOperationRetrying(context.Context, sqlc.MarkAuditExportOperationRetryingParams) (sqlc.AuditExportOperation, error) {
	return s.row, nil
}

func (s *auditExportTaskStore) MarkAuditExportOperationFailed(context.Context, sqlc.MarkAuditExportOperationFailedParams) (sqlc.AuditExportOperation, error) {
	return s.row, nil
}

func (s *auditExportTaskStore) RecoverAuditExportOperationOutbox(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type auditExportGeneratorFunc func(context.Context, audit.ExportSpec, io.Writer) error

func (fn auditExportGeneratorFunc) GenerateAuditExport(ctx context.Context, spec audit.ExportSpec, output io.Writer) error {
	return fn(ctx, spec, output)
}

func TestAuditExportRuntimePersistsCSVArtifact(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	store := &auditExportTaskStore{row: sqlc.AuditExportOperation{
		ID: id, Status: "pending", ExpiresAt: now.Add(time.Hour),
		RequestSpec: []byte(`{"filter":{"action":"cluster.delete"}}`),
	}}
	runtime := AuditExportRuntime{Queries: store, Now: func() time.Time { return now }, Generator: auditExportGeneratorFunc(
		func(_ context.Context, spec audit.ExportSpec, output io.Writer) error {
			if spec.Filter.Action != "cluster.delete" {
				t.Fatalf("filter action = %q", spec.Filter.Action)
			}
			_, err := io.WriteString(output, "id,action\n1,cluster.delete\n")
			return err
		},
	)}
	task, err := NewAuditExportOperationTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleGenerate(context.Background(), task); err != nil {
		t.Fatalf("HandleGenerate: %v", err)
	}
	if store.succeeded == nil || string(store.succeeded.Artifact) != "id,action\n1,cluster.delete\n" {
		t.Fatalf("persisted artifact = %#v", store.succeeded)
	}
	if !store.succeeded.ArtifactSha256.Valid || store.succeeded.ArtifactSha256 == (pgtype.Text{}) {
		t.Fatal("persisted artifact has no digest")
	}
}
