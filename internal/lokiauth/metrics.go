package lokiauth

import "github.com/prometheus/client_golang/prometheus"

var (
	ingestRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "loki_ingest_requests_total",
			Help:      "Hosted Loki ingest/query requests handled by loki-auth.",
		},
		[]string{"result"},
	)
	ingestBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "loki_ingest_bytes_total",
			Help:      "Bytes admitted by loki-auth on the push path.",
		},
		[]string{"result"},
	)
)

func init() {
	prometheus.MustRegister(ingestRequests, ingestBytes)
}
