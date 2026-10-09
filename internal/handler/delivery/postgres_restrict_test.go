package delivery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSourceDeleteRestrictSQLStatesPreserveScopeAndNoAudit(t *testing.T) {
	for _, item := range []struct {
		code     string
		status   int
		response string
	}{{"23503", http.StatusConflict, "resource_in_use"}, {"23001", http.StatusConflict, "resource_in_use"}, {"08006", http.StatusInternalServerError, "internal_error"}} {
		t.Run(item.code, func(t *testing.T) {
			project, id := uuid.New(), uuid.New()
			calls, commits := 0, 0
			q := &sourceQueryFake{deleteFn: func(_ context.Context, arg sqlc.DeleteDeliverySourceParams) (int64, error) {
				calls++
				if arg.ProjectID != project || arg.ID != id {
					t.Fatal("delete lost tenant scope")
				}
				return 0, fmt.Errorf("delete: %w", &pgconn.PgError{Code: item.code, Detail: "private-fixture"})
			}}
			h := NewSourceHandler(q, nil, 0)
			h.SetRunTx(func(_ context.Context, fn func(SourceMutationTx) error) error {
				if err := fn(q); err != nil {
					return err
				}
				commits++
				return nil
			})
			req := requestWithPathParams(http.MethodDelete, "/api/v1/delivery/sources/"+id.String(), nil, map[string]string{"id": id.String()})
			req.Header.Set("X-Project-ID", project.String())
			recorder := httptest.NewRecorder()
			h.Delete(recorder, req)
			if recorder.Code != item.status || !strings.Contains(recorder.Body.String(), item.response) || strings.Contains(recorder.Body.String(), "private-fixture") {
				t.Fatalf("unexpected error envelope: status=%d", recorder.Code)
			}
			if calls != 1 || commits != 0 || len(q.audits) != 0 {
				t.Fatalf("failed delete produced durable work: calls=%d commits=%d audits=%d", calls, commits, len(q.audits))
			}
		})
	}
}
