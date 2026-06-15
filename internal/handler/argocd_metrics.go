package handler

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	registerArgoCDMetricsOnce sync.Once

	argoCDApplicationsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "argocd",
			Name:      "applications",
			Help:      "Cached Argo CD application count by sync and health status.",
		},
		observability.MetricLabels("sync_status", "health_status"),
	)
)

type argoCDApplicationMetricsQuerier interface {
	CountArgoCDApplicationsBySyncHealth(ctx context.Context) ([]sqlc.CountArgoCDApplicationsBySyncHealthRow, error)
}

func registerArgoCDMetrics() {
	registerArgoCDMetricsOnce.Do(func() {
		prometheus.MustRegister(argoCDApplicationsGauge)
	})
}

func updateArgoCDApplicationMetrics(rows []sqlc.CountArgoCDApplicationsBySyncHealthRow) {
	registerArgoCDMetrics()
	argoCDApplicationsGauge.Reset()
	for _, row := range rows {
		argoCDApplicationsGauge.WithLabelValues(observability.MetricValues(
			normalizeArgoMetricStatus(row.SyncStatus),
			normalizeArgoMetricStatus(row.HealthStatus),
		)...).Set(float64(row.AppCount))
	}
}

// StartArgoCDApplicationMetricsReporter republishes cached Argo Application
// sync and health state from Postgres. It intentionally does not call Argo CD:
// the Argo operation/reconcile paths already own upstream polling and cache
// refresh; this reporter only exposes that durable state as metrics.
func StartArgoCDApplicationMetricsReporter(ctx context.Context, q argoCDApplicationMetricsQuerier, log *slog.Logger) {
	if q == nil {
		return
	}
	registerArgoCDMetrics()

	record := func() {
		rows, err := q.CountArgoCDApplicationsBySyncHealth(ctx)
		if err != nil {
			if log != nil {
				log.Warn("failed to collect argocd application metrics", "error", err)
			}
			return
		}
		updateArgoCDApplicationMetrics(rows)
	}

	record()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				record()
			}
		}
	}()

	if log != nil {
		log.Debug("started argocd application metrics reporter")
	}
}

func normalizeArgoMetricStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "Unknown"
	}
	return status
}
