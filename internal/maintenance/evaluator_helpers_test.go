package maintenance

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// countingQuerier is a WindowQuerier that returns no rows but counts
// how many times the cache misses through to the DB. Used by the
// caching tests.
type countingQuerier struct {
	calls int
	rows  []sqlc.MaintenanceWindow
}

func (c *countingQuerier) ListEnabledMaintenanceWindows(ctx context.Context) ([]sqlc.MaintenanceWindow, error) {
	c.calls++
	return c.rows, nil
}
