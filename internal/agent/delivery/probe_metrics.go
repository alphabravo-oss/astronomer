package delivery

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var observationRefreshDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "astronomer", Subsystem: "agent", Name: "delivery_observation_refresh_duration_seconds", Help: "Duration of coalesced discovery and dynamic inventory refresh work, excluding cache reads.", Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 15}}, observability.MetricLabels("source", "outcome"))
var observationSourceAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "astronomer", Subsystem: "agent", Name: "delivery_observation_source_age_seconds", Help: "Age of verified delivery inventory source evidence; -1 when no source timestamp is known."}, observability.MetricLabels("source"))
var observationSourceAvailable = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "astronomer", Subsystem: "agent", Name: "delivery_observation_source_available", Help: "One when the sampled delivery source is current, zero for unavailable, partial, or stale evidence."}, observability.MetricLabels("source"))

var observationSampledAt = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "astronomer", Subsystem: "agent", Name: "delivery_observation_sampled_at_timestamp_seconds", Help: "Unix time when the producer sampled this fixed delivery source; allows scrapers to detect a stopped producer."}, observability.MetricLabels("source"))
var observationObservedAt = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "astronomer", Subsystem: "agent", Name: "delivery_observation_observed_at_timestamp_seconds", Help: "Unix time of verified source observation; zero when unknown. This is never scrape or cache-access time."}, observability.MetricLabels("source"))

func init() {
	prometheus.MustRegister(observationRefreshDuration, observationSourceAge, observationSourceAvailable, observationSampledAt, observationObservedAt)
}
func recordSource(source string, value protocol.DeliveryObservation, now time.Time) {
	observationSampledAt.WithLabelValues(observability.MetricValues(source)...).Set(float64(now.UnixNano()) / 1e9)
	observed := 0.0
	if value.ObservedAt != nil {
		observed = float64(value.ObservedAt.UnixNano()) / 1e9
	}
	observationObservedAt.WithLabelValues(observability.MetricValues(source)...).Set(observed)
	age := -1.0
	if value.ObservedAt != nil {
		age = max(0, now.Sub(*value.ObservedAt).Seconds())
	}
	observationSourceAge.WithLabelValues(observability.MetricValues(source)...).Set(age)
	available := 0.0
	if value.State == protocol.ObservationCurrent {
		available = 1
	}
	observationSourceAvailable.WithLabelValues(observability.MetricValues(source)...).Set(available)
}
func recordRefresh(source string, start time.Time, ok bool) {
	outcome := "success"
	if !ok {
		outcome = "unavailable"
	}
	observationRefreshDuration.WithLabelValues(observability.MetricValues(source, outcome)...).Observe(time.Since(start).Seconds())
}
