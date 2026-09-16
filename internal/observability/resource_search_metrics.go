package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	resourceSearchRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "resource_search",
			Name:      "requests_total",
			Help:      "Cross-cluster resource search requests by complete or partial outcome.",
		},
		MetricLabels("outcome"),
	)
	resourceSearchClustersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "resource_search",
			Name:      "clusters_total",
			Help:      "Clusters attempted or failed by cross-cluster resource search.",
		},
		MetricLabels("outcome"),
	)
	resourceSearchDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "resource_search",
			Name:      "duration_seconds",
			Help:      "End-to-end cross-cluster resource search latency.",
			Buckets:   []float64{.05, .1, .25, .5, 1, 2, 5, 10, 15},
		},
		MetricLabels("outcome"),
	)
)

func init() {
	prometheus.MustRegister(resourceSearchRequestsTotal)
	prometheus.MustRegister(resourceSearchClustersTotal)
	prometheus.MustRegister(resourceSearchDuration)
}

func RecordResourceSearch(outcome string, attempted, failed int, startedAt time.Time) {
	if outcome != "complete" && outcome != "partial" {
		outcome = "partial"
	}
	resourceSearchRequestsTotal.WithLabelValues(MetricValues(outcome)...).Inc()
	resourceSearchClustersTotal.WithLabelValues(MetricValues("attempted")...).Add(float64(max(attempted, 0)))
	resourceSearchClustersTotal.WithLabelValues(MetricValues("failed")...).Add(float64(max(failed, 0)))
	resourceSearchDuration.WithLabelValues(MetricValues(outcome)...).Observe(time.Since(startedAt).Seconds())
}
