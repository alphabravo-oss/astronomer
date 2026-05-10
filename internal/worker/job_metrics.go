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
	registerJobMetricsOnce sync.Once

	workerJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "worker",
			Name:      "jobs_total",
			Help:      "Total number of worker task executions by job type and outcome.",
		},
		observability.MetricLabels("job", "status"),
	)
	workerJobDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "worker",
			Name:      "job_duration_seconds",
			Help:      "Task execution latency by worker job type and outcome.",
			Buckets:   prometheus.DefBuckets,
		},
		observability.MetricLabels("job", "status"),
	)
)

func registerJobMetrics() {
	registerJobMetricsOnce.Do(func() {
		prometheus.MustRegister(workerJobsTotal, workerJobDurationSeconds)
	})
}

func instrumentTask(job string, handler func(context.Context, *asynq.Task) error) func(context.Context, *asynq.Task) error {
	registerJobMetrics()
	return func(ctx context.Context, task *asynq.Task) error {
		start := time.Now()
		observability.WithEvent(slog.Default(), "worker_job_started").Info("worker job started",
			"job", job,
		)
		err := handler(ctx, task)
		status := "success"
		if err != nil {
			status = "error"
		}
		observability.WithEvent(slog.Default(), "worker_job_completed").Info("worker job completed",
			"job", job,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		labels := observability.MetricValues(job, status)
		workerJobsTotal.WithLabelValues(labels...).Inc()
		workerJobDurationSeconds.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		return err
	}
}
