// Cluster-registration wizard metrics (sprint 22 / migration 078).
//
//   - astronomer_cluster_registration_phase{cluster,phase}
//     — one series for the cluster's current phase. Lets a
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
			Help:      "Current registration phase per cluster; only the active phase is retained.",
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

// RecordPhaseTransition publishes the captured transition immediately. The
// registration transaction buffer uses CapturePhaseTransition to defer it until
// commit without allowing a late flush to recreate a retired owner.
func (h *RegistrationMetricsHook) RecordPhaseTransition(clusterID, from, to string) {
	h.CapturePhaseTransition(clusterID, from, to)()
}

// RecordDuration emits the histogram observation when a cluster lands
// on a terminal phase.
func (h *RegistrationMetricsHook) RecordDuration(clusterID, outcome string, baseline bool, seconds float64) {
	if h == nil {
		return
	}
	_ = clusterID // omitted from the histogram labels to keep cardinality bounded
	clusterRegistrationDuration.WithLabelValues(MetricValues(registrationOutcomeLabel(outcome), strconv.FormatBool(baseline))...).Observe(seconds)
}
