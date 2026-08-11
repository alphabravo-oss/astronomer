package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

type runtimeDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Stat() *pgxpool.Stat
}

type runtimeRedis interface {
	Ping(context.Context) *redis.StatusCmd
	Info(context.Context, ...string) *redis.StringCmd
}

type RuntimeCapabilityConfig struct {
	Database           runtimeDatabase
	Redis              runtimeRedis
	Gatherer           prometheus.Gatherer
	EncryptionKeyCount int
	JWTKeyCount        int
	InsecureDevKeys    []string
}

type RuntimeCapabilityAdapter struct{ config RuntimeCapabilityConfig }

func NewRuntimeCapabilityAdapter(config RuntimeCapabilityConfig) (*RuntimeCapabilityAdapter, error) {
	if config.Database == nil {
		return nil, fmt.Errorf("Charlie runtime capability dependencies are unavailable")
	}
	if config.Gatherer == nil {
		config.Gatherer = prometheus.DefaultGatherer
	}
	return &RuntimeCapabilityAdapter{config: config}, nil
}

func RuntimeCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	return map[string]CapabilityExecutor{
		"astronomer.database.performance":   adapter,
		"astronomer.redis.health":           adapter,
		"astronomer.runtime.http_health":    adapter,
		"astronomer.runtime.process_health": adapter,
		"astronomer.security.key_status":    adapter,
	}
}

func (a *RuntimeCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, _ map[string]json.RawMessage) (json.RawMessage, error) {
	var value any
	var err error
	switch capability.Name {
	case "astronomer.database.performance":
		value, err = a.databasePerformance(ctx)
	case "astronomer.redis.health":
		value, err = a.redisHealth(ctx)
	case "astronomer.runtime.http_health":
		value, err = a.httpHealth()
	case "astronomer.runtime.process_health":
		value, err = a.processHealth()
	case "astronomer.security.key_status":
		value = a.keyStatus()
	default:
		return nil, fmt.Errorf("unsupported runtime capability")
	}
	if err != nil {
		return nil, err
	}
	return marshalBounded(value, capability.MaxResponseBytes)
}

func (a *RuntimeCapabilityAdapter) keyStatus() map[string]any {
	insecure := append([]string(nil), a.config.InsecureDevKeys...)
	if insecure == nil {
		insecure = []string{}
	}
	return map[string]any{
		"encryption_keys_loaded":          a.config.EncryptionKeyCount,
		"jwt_signing_keys_loaded":         a.config.JWTKeyCount,
		"encryption_rotation_in_progress": a.config.EncryptionKeyCount > 1,
		"jwt_rotation_in_progress":        a.config.JWTKeyCount > 1,
		"insecure_development_sentinels":  insecure,
	}
}

func (a *RuntimeCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func (a *RuntimeCapabilityAdapter) databasePerformance(ctx context.Context) (map[string]any, error) {
	const snapshot = `
WITH activity AS (
  SELECT
    count(*) FILTER (WHERE pid <> pg_backend_pid())::bigint AS connections,
    count(*) FILTER (WHERE state = 'active' AND pid <> pg_backend_pid())::bigint AS active,
    count(*) FILTER (WHERE wait_event IS NOT NULL AND pid <> pg_backend_pid())::bigint AS waiting,
    count(*) FILTER (WHERE xact_start IS NOT NULL AND now() - xact_start > interval '30 seconds' AND pid <> pg_backend_pid())::bigint AS long_transactions,
    COALESCE(EXTRACT(EPOCH FROM (max(now() - xact_start) FILTER (WHERE xact_start IS NOT NULL AND pid <> pg_backend_pid()))), 0)::double precision AS oldest_transaction_seconds
  FROM pg_stat_activity
  WHERE datname = current_database()
)
SELECT
  activity.connections,
  activity.active,
  activity.waiting,
  activity.long_transactions,
  activity.oldest_transaction_seconds,
  pg_database_size(current_database())::bigint,
  pg_is_in_recovery(),
  COALESCE(CASE WHEN pg_is_in_recovery() THEN EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp()) ELSE 0 END, 0)::double precision
FROM activity`
	var connections, active, waiting, longTransactions, databaseBytes int64
	var oldestTransactionSeconds, replicationLagSeconds float64
	var recovery bool
	if err := a.config.Database.QueryRow(ctx, snapshot).Scan(
		&connections, &active, &waiting, &longTransactions, &oldestTransactionSeconds,
		&databaseBytes, &recovery, &replicationLagSeconds,
	); err != nil {
		return nil, err
	}
	var lockWaiters int64
	if err := a.config.Database.QueryRow(ctx, `SELECT count(*)::bigint FROM pg_locks WHERE NOT granted`).Scan(&lockWaiters); err != nil {
		return nil, err
	}
	stats := a.config.Database.Stat()
	return map[string]any{
		"database_connections": connections, "active_connections": active, "waiting_connections": waiting,
		"lock_waiters": lockWaiters, "long_transactions_over_30s": longTransactions,
		"oldest_transaction_seconds": oldestTransactionSeconds, "database_bytes": databaseBytes,
		"in_recovery": recovery, "replication_lag_seconds": replicationLagSeconds,
		"pool": map[string]any{
			"maximum": stats.MaxConns(), "total": stats.TotalConns(), "acquired": stats.AcquiredConns(),
			"idle": stats.IdleConns(), "empty_acquires": stats.EmptyAcquireCount(),
			"acquire_duration_seconds": stats.AcquireDuration().Seconds(), "canceled_acquires": stats.CanceledAcquireCount(),
		},
	}, nil
}

func (a *RuntimeCapabilityAdapter) redisHealth(ctx context.Context) (map[string]any, error) {
	if a.config.Redis == nil {
		return map[string]any{"reachable": false, "configured": false, "failure_code": "client_unavailable"}, nil
	}
	started := time.Now()
	if err := a.config.Redis.Ping(ctx).Err(); err != nil {
		return map[string]any{"reachable": false, "latency_seconds": time.Since(started).Seconds(), "failure_code": classifyTaskFailure(err.Error())}, nil
	}
	latency := time.Since(started).Seconds()
	info, err := a.config.Redis.Info(ctx, "server", "clients", "memory", "stats", "persistence", "replication").Result()
	if err != nil {
		return map[string]any{"reachable": true, "latency_seconds": latency, "info_available": false, "failure_code": classifyTaskFailure(err.Error())}, nil
	}
	allowed := map[string]bool{
		"redis_version": true, "uptime_in_seconds": true, "connected_clients": true, "blocked_clients": true,
		"used_memory": true, "used_memory_rss": true, "maxmemory": true, "maxmemory_policy": true,
		"evicted_keys": true, "expired_keys": true, "rejected_connections": true,
		"rdb_last_bgsave_status": true, "aof_last_bgrewrite_status": true,
		"role": true, "connected_slaves": true, "master_link_status": true,
	}
	fields := map[string]any{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok || !allowed[key] {
			continue
		}
		fields[key] = safeRedisValue(key, strings.TrimSpace(raw))
	}
	return map[string]any{"reachable": true, "latency_seconds": latency, "info_available": true, "metrics": fields}, nil
}

func safeRedisValue(key, value string) any {
	for _, numeric := range []string{"uptime_in_seconds", "connected_clients", "blocked_clients", "used_memory", "used_memory_rss", "maxmemory", "evicted_keys", "expired_keys", "rejected_connections", "connected_slaves"} {
		if key == numeric {
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				return parsed
			}
			return int64(0)
		}
	}
	return boundedDiagnosticText(value, 64)
}

func (a *RuntimeCapabilityAdapter) httpHealth() (map[string]any, error) {
	families, err := a.config.Gatherer.Gather()
	if err != nil {
		return nil, err
	}
	statusTotals := map[string]float64{}
	var requestCount uint64
	var durationSum float64
	var inflight float64
	for _, family := range families {
		switch family.GetName() {
		case "astronomer_http_requests_total":
			for _, metric := range family.Metric {
				value := metric.GetCounter().GetValue()
				statusTotals[metricLabel(metric, "status_class")] += value
			}
		case "astronomer_http_request_duration_seconds":
			for _, metric := range family.Metric {
				h := metric.GetHistogram()
				requestCount += h.GetSampleCount()
				durationSum += h.GetSampleSum()
			}
		case "astronomer_http_in_flight_requests":
			for _, metric := range family.Metric {
				inflight += metric.GetGauge().GetValue()
			}
		}
	}
	mean := float64(0)
	if requestCount > 0 {
		mean = durationSum / float64(requestCount)
	}
	return map[string]any{
		"requests_by_status_class": statusTotals, "observed_request_count": requestCount,
		"request_duration_sum_seconds": durationSum, "request_duration_mean_seconds": mean, "in_flight": inflight,
	}, nil
}

func (a *RuntimeCapabilityAdapter) processHealth() (map[string]any, error) {
	families, err := a.config.Gatherer.Gather()
	if err != nil {
		return nil, err
	}
	wanted := map[string]string{
		"go_goroutines": "goroutines", "go_memstats_alloc_bytes": "allocated_bytes",
		"go_memstats_heap_alloc_bytes": "heap_allocated_bytes", "go_memstats_heap_inuse_bytes": "heap_in_use_bytes",
		"go_memstats_gc_cycles_total": "gc_cycles", "process_cpu_seconds_total": "cpu_seconds_total",
		"process_resident_memory_bytes": "resident_memory_bytes", "process_open_fds": "open_file_descriptors",
		"process_max_fds": "max_file_descriptors", "process_start_time_seconds": "start_time_unix_seconds",
	}
	metrics := map[string]float64{}
	for _, family := range families {
		name, ok := wanted[family.GetName()]
		if !ok {
			continue
		}
		var value float64
		for _, metric := range family.Metric {
			value += scalarMetric(metric, family.GetType())
		}
		metrics[name] = value
	}
	if started := metrics["start_time_unix_seconds"]; started > 0 {
		metrics["uptime_seconds"] = float64(time.Now().Unix()) - started
	}
	metrics["runtime_cpu_count"] = float64(runtime.NumCPU())
	return map[string]any{"metrics": metrics}, nil
}

func scalarMetric(metric *dto.Metric, kind dto.MetricType) float64 {
	switch kind {
	case dto.MetricType_COUNTER:
		return metric.GetCounter().GetValue()
	case dto.MetricType_GAUGE:
		return metric.GetGauge().GetValue()
	case dto.MetricType_UNTYPED:
		return metric.GetUntyped().GetValue()
	default:
		return 0
	}
}

func metricLabel(metric *dto.Metric, name string) string {
	for _, pair := range metric.Label {
		if pair.GetName() == name {
			return pair.GetValue()
		}
	}
	return "unknown"
}
