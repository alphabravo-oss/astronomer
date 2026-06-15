package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"

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
	workerJobRetryAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "worker",
			Name:      "job_retry_attempts_total",
			Help:      "Total number of worker task executions that Asynq identified as retry attempts by job type.",
		},
		observability.MetricLabels("job"),
	)
)

func registerJobMetrics() {
	registerJobMetricsOnce.Do(func() {
		prometheus.MustRegister(workerJobsTotal, workerJobDurationSeconds, workerJobRetryAttemptsTotal)
	})
}

// EnqueueWithCorrelation wraps client.Enqueue and merges the supplied
// correlation ID into the task payload as `_correlation_id`. Pulled out
// to one helper so all 5 enqueue sites in the codebase use the same
// convention; the matching dequeue extraction lives in instrumentTask.
func EnqueueWithCorrelation(client *asynq.Client, task *asynq.Task, correlationID string, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if task == nil || client == nil {
		return nil, errAsynqEnqueueNil
	}
	if correlationID != "" {
		wrapped := observability.WithCorrelationPayload(task.Payload(), correlationID)
		task = asynq.NewTask(task.Type(), wrapped, opts...)
	}
	return client.Enqueue(task, opts...)
}

// errAsynqEnqueueNil is the sentinel for "you passed a nil task or
// client". Kept as a typed package-level err so test cases can assert
// against it.
var errAsynqEnqueueNil = errors.New("worker: enqueue called with nil task or client")

func instrumentTask(job string, handler func(context.Context, *asynq.Task) error) func(context.Context, *asynq.Task) error {
	registerJobMetrics()
	tracer := otel.Tracer("astronomer/worker")
	return func(ctx context.Context, task *asynq.Task) error {
		start := time.Now()
		// Pull `_correlation_id` (if any) out of
		// the task payload and stamp it on the per-job logger. This
		// stitches worker logs back to the HTTP request that enqueued
		// the task — previously the worker side was a dead-end for
		// correlation tracing.
		logger := slog.Default()
		if cid := observability.ExtractAsynqCorrelationID(task.Payload()); cid != "" {
			logger = observability.WithCorrelationID(logger, cid)
		}
		logger = observability.WithLogIdentifiers(logger, observability.ExtractLogIdentifiers(task.Payload()))
		// Rejoin the originating trace if traceparent rode the
		// payload, then open a child span for the worker execution.
		// When tracing is disabled at the SDK level, Start returns a
		// no-op span and the ctx is unchanged in any meaningful way.
		ctx = observability.ContextWithAsynqTracing(ctx, task.Payload())
		ctx, span := tracer.Start(ctx, "asynq.job "+job)
		defer span.End()
		logger = observability.WithTraceID(logger, ctx)
		if taskID, ok := asynq.GetTaskID(ctx); ok {
			logger = logger.With("task_id", taskID)
		}
		if retryCount, ok := asynq.GetRetryCount(ctx); ok {
			logger = logger.With("retry_count", retryCount)
			if retryCount > 0 {
				workerJobRetryAttemptsTotal.WithLabelValues(observability.MetricValues(job)...).Inc()
			}
		}
		if maxRetry, ok := asynq.GetMaxRetry(ctx); ok {
			logger = logger.With("max_retry", maxRetry)
		}

		observability.WithEvent(logger, "worker_job_started").Info("worker job started",
			"job", job,
		)
		err := handler(ctx, task)
		status := "success"
		if err != nil {
			status = "error"
			span.RecordError(err)
		}
		observability.WithEvent(logger, "worker_job_completed").Info("worker job completed",
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
