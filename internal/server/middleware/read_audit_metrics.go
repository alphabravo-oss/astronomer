// Migration 063 — read-side audit metrics.

package middleware

import (
	"encoding/json"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// readAuditEmissionsTotal counts read-side audit emissions by policy +
// outcome. Outcomes are: "recorded" (handed off to the DB worker
// successfully), "sampled_out" (policy matched but sample_rate filtered
// the event), "dropped" (queue was full), "error" (DB insert failed).
var readAuditEmissionsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "read_audit",
		Name:      "emissions_total",
		Help:      "Read-side audit emissions by action and outcome.",
	},
	observability.MetricLabels("policy", "outcome"),
)

// readAuditQueueDepth is the in-process queue depth gauge.
var readAuditQueueDepth = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "read_audit",
		Name:      "queue_depth",
		Help:      "Current depth of the read-side audit emission queue.",
	},
	observability.MetricLabels(),
)

func init() {
	prometheus.MustRegister(readAuditEmissionsTotal)
	prometheus.MustRegister(readAuditQueueDepth)
}

func recordReadAuditEmission(policy, outcome string) {
	if policy == "" {
		policy = "unknown"
	}
	if outcome == "" {
		outcome = "recorded"
	}
	readAuditEmissionsTotal.WithLabelValues(observability.MetricValues(policy, outcome)...).Inc()
}

func updateReadAuditQueueDepth(n int64) {
	readAuditQueueDepth.WithLabelValues(observability.MetricValues()...).Set(float64(n))
}

// mustMarshalDetail renders the detail map as a json.RawMessage,
// falling back to an empty object on any marshalling failure. Callers
// rely on a non-nil JSON byte slice (the audit_log column is NOT NULL).
func mustMarshalDetail(in map[string]any) json.RawMessage {
	if len(in) == 0 {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
