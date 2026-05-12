// Maintenance window metrics (migration 057).
//
// Three series:
//
//   astronomer_maintenance_blocked_total{op_type, mode}
//     Counter incremented every time IsBlocked() reports true and the
//     mutation handler short-circuits. The op_type label lets operators
//     spot which destructive ops their windows are catching; mode lets
//     them see blackout vs permitted at a glance.
//
//   astronomer_maintenance_deferred_total{op_type}
//     Counter incremented when on_block=defer fires (i.e. a deferred_
//     operations row was inserted).
//
//   astronomer_maintenance_active_windows
//     Gauge with the count of currently-active windows. Updated by the
//     evaluator on every Windows() refresh. Operators read this from
//     the dashboard widget so they can confirm the configuration is
//     having the intended effect.

package maintenance

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	blockedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "maintenance_blocked_total",
			Help:      "Mutations refused or deferred by maintenance windows.",
		},
		[]string{"op_type", "mode"},
	)
	deferredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "maintenance_deferred_total",
			Help:      "Mutations deferred (rather than refused) by maintenance windows.",
		},
		[]string{"op_type"},
	)
	activeWindows = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "maintenance_active_windows",
			Help:      "Count of currently-active maintenance windows.",
		},
	)
)

func init() {
	prometheus.MustRegister(blockedTotal)
	prometheus.MustRegister(deferredTotal)
	prometheus.MustRegister(activeWindows)
}

// RecordBlocked is called by the gating helper when a mutation is
// refused or deferred. mode is the matched window's mode (blackout /
// permitted).
func RecordBlocked(opType, mode string) {
	blockedTotal.WithLabelValues(opType, mode).Inc()
}

// RecordDeferred is called when on_block=defer fires.
func RecordDeferred(opType string) {
	deferredTotal.WithLabelValues(opType).Inc()
}

// SetActiveWindowCount updates the active_windows gauge. The evaluator
// calls this opportunistically — we don't run a separate ticker
// because every Windows() call (~30s in steady-state) is a fine cadence
// for a dashboard gauge.
func SetActiveWindowCount(n int) {
	activeWindows.Set(float64(n))
}
