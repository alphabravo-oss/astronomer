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
	dbDeadlocksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_deadlocks_total",
			Help:      "Total PostgreSQL deadlocks observed for the current database.",
		},
		observability.MetricLabels(),
	)
	dbLongestTransactionSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "db_longest_transaction_seconds",
			Help:      "Age in seconds of the longest currently open transaction in the current database.",
		},
		observability.MetricLabels(),
	)
	taskOutboxRows = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "task_outbox_rows",
			Help:      "Number of durable task outbox rows by delivery status.",
		},
		observability.MetricLabels("status"),
	)
	taskOutboxOldestDueSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: dbMetricsNamespace,
			Name:      "task_outbox_oldest_due_seconds",
			Help:      "Age in seconds of the oldest due durable task outbox row by delivery status.",
		},
		observability.MetricLabels("status"),
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

type databaseRuntimeSnapshot struct {
	deadlocks                 int64
	longestTransactionSeconds float64
}

type taskOutboxStatusSnapshot struct {
	status           string
	rows             int64
	oldestDueSeconds float64
}

var taskOutboxStatuses = []string{"pending", "delivering", "failed", "dead"}

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
			dbDeadlocksTotal,
			dbLongestTransactionSeconds,
			taskOutboxRows,
			taskOutboxOldestDueSeconds,
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

func updateDatabaseRuntimeMetrics(prev, cur databaseRuntimeSnapshot) {
	labels := observability.MetricValues()
	if delta := cur.deadlocks - prev.deadlocks; delta > 0 {
		dbDeadlocksTotal.WithLabelValues(labels...).Add(float64(delta))
	}
	dbLongestTransactionSeconds.WithLabelValues(labels...).Set(cur.longestTransactionSeconds)
}

func updateTaskOutboxMetrics(rows []taskOutboxStatusSnapshot) {
	byStatus := make(map[string]taskOutboxStatusSnapshot, len(rows))
	for _, row := range rows {
		byStatus[row.status] = row
	}
	for _, status := range taskOutboxStatuses {
		row := byStatus[status]
		labels := observability.MetricValues(status)
		taskOutboxRows.WithLabelValues(labels...).Set(float64(row.rows))
		taskOutboxOldestDueSeconds.WithLabelValues(labels...).Set(row.oldestDueSeconds)
	}
}

func snapshotDatabaseRuntimeMetrics(ctx context.Context, pool *pgxpool.Pool) (databaseRuntimeSnapshot, error) {
	var snap databaseRuntimeSnapshot
	err := pool.QueryRow(ctx, `
SELECT
  COALESCE((SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()), 0)::bigint,
  COALESCE((
    SELECT EXTRACT(EPOCH FROM max(clock_timestamp() - xact_start))
    FROM pg_stat_activity
    WHERE datname = current_database()
      AND xact_start IS NOT NULL
  ), 0)::float8
`).Scan(&snap.deadlocks, &snap.longestTransactionSeconds)
	return snap, err
}

func snapshotTaskOutboxMetrics(ctx context.Context, pool *pgxpool.Pool) ([]taskOutboxStatusSnapshot, error) {
	rows, err := pool.Query(ctx, `
SELECT
  status,
  COUNT(*)::bigint,
  COALESCE(EXTRACT(EPOCH FROM clock_timestamp() - MIN(next_attempt_at)) FILTER (WHERE next_attempt_at <= clock_timestamp()), 0)::float8
FROM task_outbox
WHERE status IN ('pending', 'delivering', 'failed', 'dead')
GROUP BY status
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []taskOutboxStatusSnapshot{}
	for rows.Next() {
		var snap taskOutboxStatusSnapshot
		if err := rows.Scan(&snap.status, &snap.rows, &snap.oldestDueSeconds); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// StartMetricsReporter publishes pgxpool statistics into Prometheus on a fixed
// interval. Metrics are process-wide and safe to call once per process.
func StartMetricsReporter(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	if pool == nil {
		return
	}

	registerDBMetrics()
	prev := poolMetricsSnapshot{}
	prevRuntime := databaseRuntimeSnapshot{}

	record := func() {
		cur := snapshotPoolMetrics(pool)
		updatePoolMetrics(prev, cur)
		prev = cur

		runtimeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		curRuntime, err := snapshotDatabaseRuntimeMetrics(runtimeCtx, pool)
		cancel()
		if err != nil {
			if log != nil {
				log.DebugContext(ctx, "failed to collect database runtime metrics", "error", err)
			}
			return
		}
		updateDatabaseRuntimeMetrics(prevRuntime, curRuntime)
		prevRuntime = curRuntime

		outboxCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		outboxRows, err := snapshotTaskOutboxMetrics(outboxCtx, pool)
		cancel()
		if err != nil {
			if log != nil {
				log.DebugContext(ctx, "failed to collect task outbox metrics", "error", err)
			}
			return
		}
		updateTaskOutboxMetrics(outboxRows)
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
