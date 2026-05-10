package db

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

const dbMetricsNamespace = "astronomer"

var (
	registerDBMetricsOnce sync.Once

	dbPoolAcquiredConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_acquired_connections",
			Help:      "Number of currently acquired PostgreSQL pool connections.",
		},
		observability.MetricLabels(),
	)
	dbPoolIdleConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_idle_connections",
			Help:      "Number of currently idle PostgreSQL pool connections.",
		},
		observability.MetricLabels(),
	)
	dbPoolTotalConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_total_connections",
			Help:      "Total number of PostgreSQL pool connections.",
		},
		observability.MetricLabels(),
	)
	dbPoolMaxConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_max_connections",
			Help:      "Configured maximum number of PostgreSQL pool connections.",
		},
		observability.MetricLabels(),
	)
	dbPoolAcquireCountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_acquire_count_total",
			Help:      "Total number of PostgreSQL pool acquire operations.",
		},
		observability.MetricLabels(),
	)
	dbPoolEmptyAcquireCountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_empty_acquire_count_total",
			Help:      "Total number of PostgreSQL pool acquires that had to wait for a connection.",
		},
		observability.MetricLabels(),
	)
	dbPoolCanceledAcquireCountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_canceled_acquire_count_total",
			Help:      "Total number of PostgreSQL pool acquires canceled by context.",
		},
		observability.MetricLabels(),
	)
	dbPoolAcquireDurationSecondsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_pool_acquire_duration_seconds_total",
			Help:      "Cumulative time spent acquiring PostgreSQL pool connections.",
		},
		observability.MetricLabels(),
	)
	dbQueryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_query_duration_seconds",
			Help:      "Latency of PostgreSQL queries by normalized operation and outcome.",
			Buckets:   prometheus.DefBuckets,
		},
		observability.MetricLabels("operation", "status"),
	)
)

type poolMetricsSnapshot struct {
	acquiredConnections    int32
	idleConnections        int32
	totalConnections       int32
	maxConnections         int32
	acquireCount           int64
	emptyAcquireCount      int64
	canceledAcquireCount   int64
	acquireDurationSeconds float64
}

func registerDBMetrics() {
	registerDBMetricsOnce.Do(func() {
		prometheus.MustRegister(
			dbPoolAcquiredConnections,
			dbPoolIdleConnections,
			dbPoolTotalConnections,
			dbPoolMaxConnections,
			dbPoolAcquireCountTotal,
			dbPoolEmptyAcquireCountTotal,
			dbPoolCanceledAcquireCountTotal,
			dbPoolAcquireDurationSecondsTotal,
			dbQueryDurationSeconds,
		)
	})
}

type queryTraceContextKey string

const (
	queryStartedAtKey queryTraceContextKey = "db_query_started_at"
	queryOperationKey queryTraceContextKey = "db_query_operation"
)

type queryTracer struct{}

func NewQueryTracer() pgx.QueryTracer {
	registerDBMetrics()
	return queryTracer{}
}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = context.WithValue(ctx, queryStartedAtKey, time.Now())
	ctx = context.WithValue(ctx, queryOperationKey, classifySQLOperation(data.SQL))
	return ctx
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(queryStartedAtKey).(time.Time)
	if !ok || start.IsZero() {
		return
	}
	operation, _ := ctx.Value(queryOperationKey).(string)
	if operation == "" {
		operation = "other"
	}
	status := "success"
	if data.Err != nil {
		status = "error"
	}
	dbQueryDurationSeconds.WithLabelValues(observability.MetricValues(operation, status)...).Observe(time.Since(start).Seconds())
}

func classifySQLOperation(sql string) string {
	fields := strings.Fields(strings.TrimSpace(sql))
	if len(fields) == 0 {
		return "other"
	}
	switch strings.ToUpper(fields[0]) {
	case "SELECT":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	case "WITH":
		return "cte"
	default:
		return "other"
	}
}

func snapshotPoolMetrics(pool *pgxpool.Pool) poolMetricsSnapshot {
	stat := pool.Stat()
	return poolMetricsSnapshot{
		acquiredConnections:    stat.AcquiredConns(),
		idleConnections:        stat.IdleConns(),
		totalConnections:       stat.TotalConns(),
		maxConnections:         stat.MaxConns(),
		acquireCount:           stat.AcquireCount(),
		emptyAcquireCount:      stat.EmptyAcquireCount(),
		canceledAcquireCount:   stat.CanceledAcquireCount(),
		acquireDurationSeconds: stat.AcquireDuration().Seconds(),
	}
}

func updatePoolMetrics(prev, cur poolMetricsSnapshot) {
	labels := observability.MetricValues()
	dbPoolAcquiredConnections.WithLabelValues(labels...).Set(float64(cur.acquiredConnections))
	dbPoolIdleConnections.WithLabelValues(labels...).Set(float64(cur.idleConnections))
	dbPoolTotalConnections.WithLabelValues(labels...).Set(float64(cur.totalConnections))
	dbPoolMaxConnections.WithLabelValues(labels...).Set(float64(cur.maxConnections))

	if delta := cur.acquireCount - prev.acquireCount; delta > 0 {
		dbPoolAcquireCountTotal.WithLabelValues(labels...).Add(float64(delta))
	}
	if delta := cur.emptyAcquireCount - prev.emptyAcquireCount; delta > 0 {
		dbPoolEmptyAcquireCountTotal.WithLabelValues(labels...).Add(float64(delta))
	}
	if delta := cur.canceledAcquireCount - prev.canceledAcquireCount; delta > 0 {
		dbPoolCanceledAcquireCountTotal.WithLabelValues(labels...).Add(float64(delta))
	}
	if delta := cur.acquireDurationSeconds - prev.acquireDurationSeconds; delta > 0 {
		dbPoolAcquireDurationSecondsTotal.WithLabelValues(labels...).Add(delta)
	}
}

// StartMetricsReporter publishes pgxpool statistics into Prometheus on a fixed
// interval. Metrics are process-wide and safe to call once per process.
func StartMetricsReporter(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	if pool == nil {
		return
	}

	registerDBMetrics()
	prev := poolMetricsSnapshot{}

	record := func() {
		cur := snapshotPoolMetrics(pool)
		updatePoolMetrics(prev, cur)
		prev = cur
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
		log.Debug("started db metrics reporter")
	}
}
