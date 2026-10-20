package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type restrictedLoggingDeleteTx struct {
	*stagedLoggingMutationTx
	err         error
	deleteCalls int
}

func (tx *restrictedLoggingDeleteTx) DeleteLoggingOutput(context.Context, uuid.UUID) error {
	tx.deleteCalls++
	return tx.err
}

func TestLoggingDeleteRestrictSQLStatesRollbackWithoutAudit(t *testing.T) {
	for _, item := range []struct {
		code   string
		status int
	}{{"23503", http.StatusConflict}, {"23001", http.StatusConflict}, {"08006", http.StatusInternalServerError}} {
		t.Run(item.code, func(t *testing.T) {
			q := newLoggingFakeQuerier()
			id, cluster := uuid.New(), uuid.New()
			q.outputs[id] = sqlc.LoggingOutput{ID: id, Name: "referenced", ClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, Configuration: []byte(`{}`)}
			tx := &restrictedLoggingDeleteTx{stagedLoggingMutationTx: newStagedLoggingMutationTx(), err: fmt.Errorf("delete: %w", &pgconn.PgError{Code: item.code})}
			h := NewLoggingHandler(q)
			commits := 0
			h.SetRunTx(func(_ context.Context, fn func(LoggingMutationTx) error) error {
				if err := fn(tx); err != nil {
					return err
				}
				commits++
				return nil
			})
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/logging/outputs/"+id.String(), nil)
			req.Header.Set("Idempotency-Key", "restrict-fixture")
			rc := chi.NewRouteContext()
			rc.URLParams.Add("id", id.String())
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			recorder := httptest.NewRecorder()
			h.DeleteOutput(recorder, req)
			if recorder.Code != item.status {
				t.Fatalf("SQLSTATE %s status=%d want=%d", item.code, recorder.Code, item.status)
			}
			if tx.deleteCalls != 1 || len(tx.operations) != 1 || commits != 0 || len(tx.audits) != 0 || len(q.operations) != 0 || len(q.outputs) != 1 {
				t.Fatalf("failed delete committed state/operation/audit: delete=%d attemptedOps=%d commits=%d audits=%d", tx.deleteCalls, len(tx.operations), commits, len(tx.audits))
			}
		})
	}
}
