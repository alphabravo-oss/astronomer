package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedClusterTemplateMutationTx struct {
	ClusterTemplateMutationTx
	application sqlc.ClusterTemplateApplication
	tasks       []sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams
	audits      []sqlc.UpsertAuditOutboxParams
	auditErr    error
}

func (tx *stagedClusterTemplateMutationTx) UpsertClusterTemplateApplicationWithTaskOutbox(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error) {
	tx.tasks = append(tx.tasks, arg)
	tx.application = sqlc.ClusterTemplateApplication{
		ClusterID: arg.ClusterID, TemplateID: arg.TemplateID,
		SpecSnapshot: arg.SpecSnapshot, Status: ClusterTemplateStatusPending,
	}
	return tx.application, nil
}

func (tx *stagedClusterTemplateMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestClusterTemplateApplicationTaskAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit application task and audit", wantCommit: 1},
		{name: "audit failure rolls back application and task", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedApplications, committedTasks, committedAudits := 0, 0, 0
			h := NewClusterTemplateHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(ClusterTemplateMutationTx) error) error {
				tx := &stagedClusterTemplateMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.application.ClusterID != uuid.Nil {
					committedApplications++
				}
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/template/", nil)
			params := sqlc.UpsertClusterTemplateApplicationParams{
				ClusterID: uuid.New(), TemplateID: uuid.New(), SpecSnapshot: []byte(`{"environment":"production"}`),
			}

			_, err := executeMutation(r, h.runTx,
				func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplateApplication, error) {
					return upsertClusterTemplateApplicationAndTask(r, q, params)
				},
				func(row sqlc.ClusterTemplateApplication) mutationAuditEvent {
					return mutationAuditEvent{action: "cluster.template_applied", resourceType: "cluster", resourceID: row.ClusterID.String(), status: http.StatusAccepted}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedApplications != tc.wantCommit || committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed application/task/audit = %d/%d/%d, want %d each", committedApplications, committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryClusterTemplateMutationUsesTransactionalExecutor(t *testing.T) {
	assertHandlerMutationsUseExecutor(t, "ClusterTemplateHandler", []string{
		"Create", "Update", "Delete", "Apply", "Reapply", "Detach",
	})
}
