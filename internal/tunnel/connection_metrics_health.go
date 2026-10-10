package tunnel

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	connectionMetricAdmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "astronomer", Name: "connection_metrics_admissions_total",
		Help: "Tunnel metrics handle admissions by recorded or budget_dropped result; dropping metrics never rejects the connection.",
	}, observability.MetricLabels("result"))
	connectionMetricObservations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "astronomer", Name: "connection_metrics_observations_total",
		Help: "Connection metrics collection/publication attempts by complete, page_budget, owner_budget, canceled or failed result.",
	}, observability.MetricLabels("result"))
	connectionMetricLastComplete = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "astronomer", Name: "connection_metrics_last_complete_timestamp_seconds",
		Help: "Collection-start timestamp of the last complete consistent owner observation; absent before the first complete observation. Failure never refreshes it.",
	}, observability.MetricLabels())
)

func recordConnectionMetricsFailure(err error) {
	result := "failed"
	switch {
	case errors.Is(err, ErrConnectionMetricsBudget):
		result = "page_budget"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result = "canceled"
	}
	connectionMetricObservations.WithLabelValues(observability.MetricValues(result)...).Inc()
}
