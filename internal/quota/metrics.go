package quota

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Metric labels follow the project-wide convention surfaced by
// observability.MetricLabels — instance gets prepended transparently so
// every metric inherits the same global identification dimensions used
// by the rest of the platform's Prometheus scrape.
var (
	registerOnce sync.Once

	// quotaViolationsTotal counts every Check* call that would have
	// rejected, including the soft-mode path where the call still
	// returned nil. Hot operational signal: a spike means somebody is
	// repeatedly bumping a real ceiling, not just a noisy login flow.
	quotaViolationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "quota_violations_total",
			Help:      "Per-tenant quota cap reached; counts both hard rejects and soft warnings.",
		},
		observability.MetricLabels("subject", "limit", "enforcement"),
	)

	// quotaUsagePct is a gauge updated on each Check* call (live) and
	// refreshed every 5m by the background reporter so dashboards still
	// show "current load" for tenants who aren't actively writing.
	quotaUsagePct = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "quota_usage_pct",
			Help:      "Per-tenant quota usage as percent of cap; 0-100 (or higher in soft-mode overrun).",
		},
		observability.MetricLabels("subject", "limit"),
	)
)

// MustRegister wires the package metrics into the default Prometheus
// registry. Safe to call multiple times — the underlying sync.Once
// guards the registration.
func MustRegister() {
	registerOnce.Do(func() {
		prometheus.MustRegister(quotaViolationsTotal, quotaUsagePct)
	})
}
