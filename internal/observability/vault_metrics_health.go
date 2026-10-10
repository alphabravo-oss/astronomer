package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var vaultMetricObservations = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Name: "vault_metric_observations_total",
	Help: "Vault current-owner observations by complete, owner_budget, invalid, canceled or failed result.",
}, MetricLabels("result"))
var vaultMetricLastComplete = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "astronomer", Name: "vault_metric_last_complete_timestamp_seconds",
	Help: "Collection-start timestamp of the last complete published Vault owner observation; failed or refused observations preserve it.",
}, MetricLabels())

func init() { prometheus.MustRegister(vaultMetricObservations, vaultMetricLastComplete) }

func RecordVaultMetricFailure(result string) {
	switch result {
	case "owner_budget", "invalid", "canceled":
	default:
		result = "failed"
	}
	vaultMetricObservations.WithLabelValues(MetricValues(result)...).Inc()
}

func RecordVaultMetricComplete(started time.Time) {
	vaultMetricObservations.WithLabelValues(MetricValues("complete")...).Inc()
	vaultMetricLastComplete.WithLabelValues(MetricValues()...).Set(float64(started.UnixNano()) / 1e9)
}
