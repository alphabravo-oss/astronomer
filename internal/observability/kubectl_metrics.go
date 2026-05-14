// Kubectl-shell session metrics (T6.065).
//
// One gauge:
//
//   astronomer_kubectl_active_sessions
//     — current count of kubectl_sessions rows in status='active'.
//     The reaper task sets this on every tick so the value tracks
//     reality without needing a counter-of-creates/deletes pair.
//
// Why a gauge rather than a counter: the operator question "is the
// shell feature getting any traffic right now?" is answered by a
// current-value reading, not a rate. Counters of opens/closes already
// exist on the reaper-tick path; the gauge fills the live-state gap.

package observability

import "github.com/prometheus/client_golang/prometheus"

var kubectlActiveSessions = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "kubectl",
		Name:      "active_sessions",
		Help:      "Number of kubectl_sessions rows currently in status='active'.",
	},
	MetricLabels(),
)

func init() {
	prometheus.MustRegister(kubectlActiveSessions)
}

// SetKubectlActiveSessions records the current active-session count.
// Called by the reaper at the end of each tick.
func SetKubectlActiveSessions(n int) {
	kubectlActiveSessions.WithLabelValues(MetricValues()...).Set(float64(n))
}
