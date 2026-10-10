// Vault resolver metrics (migration 067).
//
//   - astronomer_vault_resolves_total{connection, outcome}
//   - astronomer_vault_resolve_duration_seconds{connection}
//   - astronomer_vault_connection_health{connection}
//
// "outcome" is one of "succeeded" | "failed". Connection label is the
// immutable verified vault_connections.id; unresolved names share unknown. Histogram buckets cover the
// realistic range of a Vault GET — most are <100ms, slow ones hit a few
// seconds, anything beyond that is a misconfigured Vault that the
// install path should fail fast on.

package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	VaultOutcomeSucceeded = "succeeded"
	VaultOutcomeFailed    = "failed"
)

var (
	vaultResolvesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "vault",
			Name:      "resolves_total",
			Help:      "Total Vault reference resolutions by connection and outcome.",
		},
		MetricLabels("connection", "outcome"),
	)

	vaultResolveDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "vault",
			Name:      "resolve_duration_seconds",
			Help:      "Latency of one Vault FetchSecret call, by connection.",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		MetricLabels("connection"),
	)

	vaultConnectionHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "vault",
			Name:      "connection_health",
			Help:      "1 when the connection's last health check passed, 0 when it failed.",
		},
		MetricLabels("connection"),
	)
)

func init() {
	prometheus.MustRegister(vaultResolvesTotal)
	prometheus.MustRegister(vaultResolveDuration)
	prometheus.MustRegister(vaultConnectionHealth)
}

// RecordVaultResolve emits the resolves_total + duration metrics for
// one unbound reference. Unverified names share unknown; the production
// resolver binds a verified connection ID with VaultMetricLookup instead.
// Safe to call from multiple goroutines.
func RecordVaultResolve(_ string, outcome string, durationSeconds float64) {
	connection := "unknown"
	vaultResolvesTotal.WithLabelValues(MetricValues(connection, vaultOutcomeLabel(outcome))...).Inc()
	if outcome == VaultOutcomeSucceeded && durationSeconds > 0 {
		vaultResolveDuration.WithLabelValues(MetricValues(connection)...).Observe(durationSeconds)
	}
}

// RecordVaultHealth records unbound health as unknown. Production probes use
// a verified VaultMetricLease captured before their database lookup.
func RecordVaultHealth(_ string, ok bool) {
	connection := "unknown"
	v := 0.0
	if ok {
		v = 1.0
	}
	vaultConnectionHealth.WithLabelValues(MetricValues(connection)...).Set(v)
}
