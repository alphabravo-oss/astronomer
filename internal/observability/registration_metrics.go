// Cluster-registration wizard metrics (sprint 22 / migration 078).
//
//   - astronomer_cluster_registration_phase{cluster,phase}
//     — 1 for the cluster's current phase, 0 for the others. Lets a
//     dashboard graph "how many clusters are stuck in awaiting_agent?".
//   - astronomer_cluster_registration_duration_seconds{outcome,baseline}
//     — observed when a cluster reaches ready or failed.
//
// The hook is registered package-init so callers don't have to wire it.
// Implementations of registration.MetricsHook live here too.
package observability

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	clusterRegistrationPhase = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "cluster_registration",
			Name:      "phase",
			Help:      "Current registration phase per cluster — 1 for the active phase, 0 for the others.",
		},
		MetricLabels("cluster", "phase"),
	)

	clusterRegistrationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "cluster_registration",
			Name:      "duration_seconds",
			Help:      "Time from POST /clusters/ to terminal phase (ready or failed).",
			Buckets:   []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
		},
		MetricLabels("outcome", "baseline"),
	)
)

func init() {
	prometheus.MustRegister(clusterRegistrationPhase)
	prometheus.MustRegister(clusterRegistrationDuration)
}

// RegistrationMetricsHook is the registration.MetricsHook implementation.
// Constructed once; nil-safe receiver methods so callers don't need
// to guard the field.
type RegistrationMetricsHook struct{}

// NewRegistrationMetricsHook returns the shared hook value.
func NewRegistrationMetricsHook() *RegistrationMetricsHook { return &RegistrationMetricsHook{} }

// RecordPhaseTransition zeroes the `from` series and sets the `to`
// series to 1. Used so a stuck-in-awaiting_agent cluster lights up the
// gauge for that one label combination only.
func (h *RegistrationMetricsHook) RecordPhaseTransition(clusterID, from, to string) {
	if h == nil {
		return
	}
	if from != "" {
		clusterRegistrationPhase.WithLabelValues(MetricValues(clusterID, from)...).Set(0)
	}
	if to != "" {
		clusterRegistrationPhase.WithLabelValues(MetricValues(clusterID, to)...).Set(1)
	}
}

// RecordDuration emits the histogram observation when a cluster lands
// on a terminal phase.
func (h *RegistrationMetricsHook) RecordDuration(clusterID, outcome string, baseline bool, seconds float64) {
	if h == nil {
		return
	}
	_ = clusterID // omitted from the histogram labels to keep cardinality bounded
	clusterRegistrationDuration.WithLabelValues(MetricValues(outcome, strconv.FormatBool(baseline))...).Observe(seconds)
}
