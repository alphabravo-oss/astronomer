package catalog

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	catalogSyncs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_catalog_sync_total",
		Help: "Catalog synchronization attempts by bounded transport and result.",
	}, []string{"transport", "result"})
	catalogSyncDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_catalog_sync_duration_seconds",
		Help:    "Catalog retrieval, verification, and persistence duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"transport", "result"})
	catalogEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_catalog_entries",
		Help: "Entries persisted by the most recent successful catalog synchronization.",
	})
	catalogLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_catalog_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful catalog synchronization.",
	})
)

func init() {
	prometheus.MustRegister(catalogSyncs, catalogSyncDuration, catalogEntries, catalogLastSuccess)
}

func observeCatalogSync(source string, entries int, err error, elapsed time.Duration) {
	transport := "https"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "oci://") {
		transport = "oci"
	}
	result := "success"
	if err != nil {
		result = "failure"
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "digest"):
			result = "digest_mismatch"
		case strings.Contains(message, "parse"), strings.Contains(message, "invalid"), strings.Contains(message, "unsupported"):
			result = "validation_failed"
		case strings.Contains(message, "fetch"), strings.Contains(message, "http"):
			result = "fetch_failed"
		}
	}
	catalogSyncs.WithLabelValues(transport, result).Inc()
	catalogSyncDuration.WithLabelValues(transport, result).Observe(max(0, elapsed.Seconds()))
	if err == nil {
		catalogEntries.Set(float64(max(0, entries)))
		catalogLastSuccess.SetToCurrentTime()
	}
}
