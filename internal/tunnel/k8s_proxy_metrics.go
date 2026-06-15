package tunnel

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var k8sProxyErrorsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "k8s_proxy",
		Name:      "errors_total",
		Help:      "Total Kubernetes proxy failures by proxy mode and bounded reason.",
	},
	observability.MetricLabels("mode", "reason"),
)

func init() {
	prometheus.MustRegister(k8sProxyErrorsTotal)
}

func recordK8sProxyError(mode, reason string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "normal"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	k8sProxyErrorsTotal.WithLabelValues(observability.MetricValues(mode, reason)...).Inc()
}
