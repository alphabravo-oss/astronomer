package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	registerQueueMetricsOnce sync.Once

	workerQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "worker",
			Name:      "queue_depth",
			Help:      "Current Asynq queue depth by queue and task state.",
		},
		observability.MetricLabels("queue", "state"),
	)
	workerQueueLatencySeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "worker",
			Name:      "queue_latency_seconds",
			Help:      "Age of the oldest pending task in each Asynq queue.",
		},
		observability.MetricLabels("queue"),
	)
)

type queueInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
}

func registerQueueMetrics() {
	registerQueueMetricsOnce.Do(func() {
		prometheus.MustRegister(workerQueueDepth, workerQueueLatencySeconds)
	})
}

func updateQueueMetrics(infos []*asynq.QueueInfo) {
	workerQueueDepth.Reset()
	workerQueueLatencySeconds.Reset()

	for _, info := range infos {
		if info == nil {
			continue
		}
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "pending")...).Set(float64(info.Pending))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "active")...).Set(float64(info.Active))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "scheduled")...).Set(float64(info.Scheduled))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "retry")...).Set(float64(info.Retry))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "archived")...).Set(float64(info.Archived))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "completed")...).Set(float64(info.Completed))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "aggregating")...).Set(float64(info.Aggregating))
		workerQueueDepth.WithLabelValues(observability.MetricValues(info.Queue, "total")...).Set(float64(info.Size))
		workerQueueLatencySeconds.WithLabelValues(observability.MetricValues(info.Queue)...).Set(info.Latency.Seconds())
	}
}

// StartQueueMetricsReporter periodically scrapes Asynq queue state and
// publishes it to Prometheus from the worker process.
func StartQueueMetricsReporter(ctx context.Context, inspector queueInspector, log *slog.Logger) {
	if inspector == nil {
		return
	}
	registerQueueMetrics()

	record := func() {
		queues, err := inspector.Queues()
		if err != nil {
			if log != nil {
				log.Warn("failed to list worker queues for metrics", "error", err)
			}
			return
		}
		infos := make([]*asynq.QueueInfo, 0, len(queues))
		for _, queue := range queues {
			info, err := inspector.GetQueueInfo(queue)
			if err != nil {
				if log != nil {
					log.Warn("failed to inspect worker queue for metrics", "queue", queue, "error", err)
				}
				continue
			}
			infos = append(infos, info)
		}
		updateQueueMetrics(infos)
	}

	record()

	go func() {
		ticker := time.NewTicker(15 * time.Second)
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
		log.Debug("started worker queue metrics reporter")
	}
}
