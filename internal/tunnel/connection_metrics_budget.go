package tunnel

import (
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Ten times the documented 1,000-cluster extreme-lab target. This is a bounded
// collector engineering limit, not an assertion of qualified fleet capacity.
// Exactly 10,000 rows permit one final empty page; any larger observation is
// refused so a partial collection can never authorize owner retirement.
const (
	ConnectionMetricsPageSize  int32 = 500
	ConnectionMetricsMaxOwners       = 10_000
)

var ErrConnectionMetricsBudget = errors.New("connection metrics collection exceeded its bounded owner budget")

func AppendConnectionMetricsPage(complete, page []sqlc.ListClusterConnectionStatusRow) ([]sqlc.ListClusterConnectionStatusRow, error) {
	if len(page) > int(ConnectionMetricsPageSize) || len(complete) > ConnectionMetricsMaxOwners-len(page) {
		return nil, ErrConnectionMetricsBudget
	}
	return append(complete, page...), nil
}
