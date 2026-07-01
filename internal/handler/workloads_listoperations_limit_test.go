package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// listOpsLimitQuerier captures the params ListOperations issues so the test can
// assert the ?limit query param is clamped before it reaches SQL.
type listOpsLimitQuerier struct {
	WorkloadQuerier
	captured sqlc.ListWorkloadOperationsParams
}

func (q *listOpsLimitQuerier) ListWorkloadOperations(_ context.Context, arg sqlc.ListWorkloadOperationsParams) ([]sqlc.WorkloadOperation, error) {
	q.captured = arg
	return nil, nil
}

// TestListOperations_ClampsLimit verifies a hostile / overflowing ?limit is
// clamped to a sane bound and floored at 1, instead of being handed straight to
// SQL LIMIT (unbounded materialization, or int32 overflow into a negative LIMIT
// that Postgres rejects).
func TestListOperations_ClampsLimit(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantLimit int32
	}{
		{"unbounded", "?limit=2000000000", 200},
		{"int32-overflow", "?limit=2147483648", 200},
		{"negative-floored", "?limit=-5", 50},
		{"in-range", "?limit=10", 10},
		{"default", "", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &listOpsLimitQuerier{}
			h := NewWorkloadHandlerWithDeps(q, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/workloads/operations/"+tc.query, nil)
			rec := httptest.NewRecorder()
			h.ListOperations(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if q.captured.Limit != tc.wantLimit {
				t.Fatalf("SQL LIMIT = %d, want %d", q.captured.Limit, tc.wantLimit)
			}
		})
	}
}
