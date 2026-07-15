package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	EventRelayResultEnqueued  = "enqueued"
	EventRelayResultPublished = "published"
	EventRelayResultDropped   = "dropped"
)

var (
	eventRelayQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "queue_depth",
			Help:      "Current number of local events waiting for Redis fan-out.",
		},
		MetricLabels(),
	)
	eventRelayQueueCapacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "queue_capacity",
			Help:      "Configured bounded capacity of the Redis event relay queue.",
		},
		MetricLabels(),
	)
	eventRelayEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "events_total",
			Help:      "Total Redis relay events by enqueue, publish, or drop result.",
		},
		MetricLabels("result"),
	)
	eventRelayPublishDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "publish_duration_seconds",
			Help:      "Latency of individual Redis Publish attempts from the event relay.",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2},
		},
		MetricLabels(),
	)
	eventRelayHealthy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "healthy",
			Help:      "Whether the Redis event relay worker is running and its most recent publish succeeded.",
		},
		MetricLabels(),
	)
	eventRelayLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "event_relay",
			Name:      "last_success_timestamp_seconds",
			Help:      "Unix timestamp of the most recent successful Redis event publish.",
		},
		MetricLabels(),
	)
)

func init() {
	prometheus.MustRegister(eventRelayQueueDepth)
	prometheus.MustRegister(eventRelayQueueCapacity)
	prometheus.MustRegister(eventRelayEventsTotal)
	prometheus.MustRegister(eventRelayPublishDuration)
	prometheus.MustRegister(eventRelayHealthy)
	prometheus.MustRegister(eventRelayLastSuccess)
}

func SetEventRelayQueue(depth, capacity int) {
	eventRelayQueueDepth.WithLabelValues(MetricValues()...).Set(float64(depth))
	eventRelayQueueCapacity.WithLabelValues(MetricValues()...).Set(float64(capacity))
}

func RecordEventRelayResult(result string) {
	if result == "" {
		result = EventRelayResultDropped
	}
	eventRelayEventsTotal.WithLabelValues(MetricValues(result)...).Inc()
}

func ObserveEventRelayPublish(start time.Time) {
	eventRelayPublishDuration.WithLabelValues(MetricValues()...).Observe(time.Since(start).Seconds())
}

func SetEventRelayHealth(healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	eventRelayHealthy.WithLabelValues(MetricValues()...).Set(value)
}

func SetEventRelayLastSuccess(at time.Time) {
	if at.IsZero() {
		return
	}
	eventRelayLastSuccess.WithLabelValues(MetricValues()...).Set(float64(at.Unix()))
}
