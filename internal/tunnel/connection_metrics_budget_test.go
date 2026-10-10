package tunnel

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type budgetConnectionLister struct {
	rows      int
	oversized bool
	queries   int
	cancel    context.CancelFunc
}

func (l *budgetConnectionLister) ListClusterConnectionStatus(_ context.Context, arg sqlc.ListClusterConnectionStatusParams) ([]sqlc.ListClusterConnectionStatusRow, error) {
	l.queries++
	if l.cancel != nil {
		l.cancel() // Deliberately ignore context: collector must check it itself.
	}
	if l.oversized {
		return make([]sqlc.ListClusterConnectionStatusRow, int(arg.QueryLimit)+1), nil
	}
	n := min(max(0, l.rows-int(arg.QueryOffset)), int(arg.QueryLimit))
	return make([]sqlc.ListClusterConnectionStatusRow, n), nil
}

func TestConnectionMetricsCollectionBudget(t *testing.T) {
	for _, test := range []struct {
		name        string
		rows        int
		oversized   bool
		cancel      bool
		wantQueries int
		wantError   error
	}{
		{name: "empty", rows: 0, wantQueries: 1},
		{name: "planned-extreme-lab", rows: 1000, wantQueries: 3},
		{name: "exact-ceiling-with-empty-sentinel", rows: ConnectionMetricsMaxOwners, wantQueries: 21},
		{name: "ceiling-plus-one", rows: ConnectionMetricsMaxOwners + 1, wantQueries: 21, wantError: ErrConnectionMetricsBudget},
		{name: "endless-full-pages", rows: 1 << 30, wantQueries: 21, wantError: ErrConnectionMetricsBudget},
		{name: "oversized-driver-page", oversized: true, wantQueries: 1, wantError: ErrConnectionMetricsBudget},
		{name: "driver-ignores-cancellation", rows: 500, cancel: true, wantQueries: 1, wantError: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lister := &budgetConnectionLister{rows: test.rows, oversized: test.oversized}
			if test.cancel {
				lister.cancel = cancel
			}
			rows, err := collectConnectionMetricsPages(ctx, lister)
			if !errors.Is(err, test.wantError) || lister.queries != test.wantQueries {
				t.Fatalf("queries=%d error=%v; want queries=%d error=%v", lister.queries, err, test.wantQueries, test.wantError)
			}
			if err != nil && rows != nil {
				t.Fatal("refused observation exposed partial rows")
			}
			if err == nil && len(rows) != test.rows {
				t.Fatalf("rows=%d want%d", len(rows), test.rows)
			}
		})
	}
}
