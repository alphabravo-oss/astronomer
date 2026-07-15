package tasks

// Sprint 072 — rolling-window anomaly baseline recompute.
//
// Cadence: every 5m. The handler is leader-elected (same pattern as
// other periodic sweeps in this package). The recompute walks every
// row in anomaly_baselines and, for each, pulls the latest sample
// from the cluster health table, pushes it onto the bounded ring
// buffer in the row's recent_samples JSONB column, recomputes the
// aggregate stats (mean/stddev/percentiles via internal/anomaly),
// and writes them back via UpsertAnomalyBaseline.
//
// We additionally walk anomaly-kind alert_rules and ensure a
// baseline row exists for each (cluster, metric, window) tuple they
// reference. Newly-inserted rows start at sample_count=0; the
// evaluator's min-samples gate short-circuits to no-fire until
// enough datapoints accumulate (default 50, which is ~4h at a 5m
// cadence).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/anomaly"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// AnomalyBaselineRecomputeType is the periodic-task identifier.
const AnomalyBaselineRecomputeType = "anomaly:baseline_recompute"

// anomalyRingBufferCap bounds the per-baseline recent_samples JSONB
// array. The recompute task pulls the canonical history from the
// metric table whenever it can; this ring buffer is the fast-path
// the evaluator can fall back to between recomputes.
const anomalyRingBufferCap = 1000

// anomalyMaxSamplesPerRecompute caps the recompute's sort+stat cost.
// Sort-based percentiles are O(N log N); 5000 keeps a single
// recompute < 1ms per baseline even on the slowest deployment we
// support, and keeps the working set tiny for the worker's heap.
const anomalyMaxSamplesPerRecompute = 5000

// anomalyDefaultWindowSeconds is the baseline window we materialize
// for any (cluster, metric) tuple that doesn't have a more specific
// rule attached. 24h is a useful default for cluster-level CPU /
// memory rollups — long enough to absorb a normal diurnal cycle.
const anomalyDefaultWindowSeconds = int32(86400)

// baselineRecomputeQuerier is the narrow interface the recompute
// task needs from the runtime querier. We define it locally and do
// a type assertion at runtime (same pattern as
// HandleCleanupAlertEvents) so the broader RuntimeQuerier doesn't
// have to know about anomaly baselines.
type baselineRecomputeQuerier interface {
	ListAnomalyBaselines(ctx context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error)
	GetAnomalyBaseline(ctx context.Context, arg sqlc.GetAnomalyBaselineParams) (sqlc.AnomalyBaseline, error)
	UpsertAnomalyBaseline(ctx context.Context, arg sqlc.UpsertAnomalyBaselineParams) (sqlc.AnomalyBaseline, error)
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
}

// NewAnomalyBaselineRecomputeTask returns the asynq task that drives
// the periodic recompute.
func NewAnomalyBaselineRecomputeTask() *asynq.Task {
	return asynq.NewTask(AnomalyBaselineRecomputeType, nil, asynq.MaxRetry(2))
}

// HandleAnomalyBaselineRecompute is the asynq handler. It refreshes
// every baseline row and provisions empty rows for newly-seen
// (cluster, metric, window) tuples referenced by anomaly rules.
func HandleAnomalyBaselineRecompute(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, AnomalyBaselineRecomputeType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "anomaly baseline recompute runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(baselineRecomputeQuerier)
		if !ok {
			return fmt.Errorf("anomaly baseline recompute not supported by runtime querier")
		}
		return RunAnomalyBaselineRecompute(ctx, q, time.Now().UTC())
	})
}

// RunAnomalyBaselineRecompute is the testable core of the periodic
// handler. It is exported so tests can drive it without spinning up
// a leader-elector or the full runtime config.
func RunAnomalyBaselineRecompute(ctx context.Context, q baselineRecomputeQuerier, now time.Time) error {
	// 1. Ensure every anomaly rule has a baseline row for its
	//    (cluster, metric, window) tuple. Without this, the first
	//    evaluator tick after rule creation would have to do its
	//    own bootstrap and we'd risk a race where the evaluator
	//    fires from a stale baseline.
	rules, err := q.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return fmt.Errorf("list alert rules: %w", err)
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		cfg := decodeAnomalyRuleConfig(rule.Configuration)
		if !cfg.IsAnomaly {
			continue
		}
		if !rule.ClusterID.Valid {
			// A global anomaly rule with no cluster scope is
			// allowed by the schema but doesn't map to a single
			// baseline row. The evaluator handles this by
			// fanning out across clusters, so nothing to seed.
			continue
		}
		clusterID := uuid.UUID(rule.ClusterID.Bytes)
		metric := cfg.Metric
		if metric == "" {
			continue
		}
		window := cfg.WindowSeconds
		if window <= 0 {
			window = anomalyDefaultWindowSeconds
		}
		existing, err := q.GetAnomalyBaseline(ctx, sqlc.GetAnomalyBaselineParams{
			ClusterID:     clusterID,
			MetricName:    metric,
			WindowSeconds: window,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get baseline (seed): %w", err)
		}
		if err == nil {
			_ = existing
			continue
		}
		// Seed an empty baseline so the evaluator's min-samples
		// gate fires correctly and the next recompute tick can
		// pick it up.
		if _, err := q.UpsertAnomalyBaseline(ctx, sqlc.UpsertAnomalyBaselineParams{
			ClusterID:     clusterID,
			MetricName:    metric,
			WindowSeconds: window,
			SampleCount:   0,
			RecentSamples: json.RawMessage("[]"),
		}); err != nil {
			return fmt.Errorf("seed empty baseline: %w", err)
		}
	}

	// 2. Walk every existing baseline and recompute its
	//    aggregate. We page in batches of 200 to keep the heap
	//    bounded under a large fleet.
	const pageSize = int32(200)
	recomputedClusters := map[uuid.UUID]bool{}
	for offset := int32(0); ; offset += pageSize {
		page, err := q.ListAnomalyBaselines(ctx, sqlc.ListAnomalyBaselinesParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("list baselines: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, baseline := range page {
			if err := recomputeOneBaseline(ctx, q, baseline, now); err != nil {
				runtimeLogger().WarnContext(ctx, "anomaly baseline recompute failed for row",
					"baseline_id", baseline.ID.String(),
					"cluster_id", baseline.ClusterID.String(),
					"metric", baseline.MetricName,
					"err", err.Error(),
				)
				// Continue with the next row — one bad row
				// shouldn't kill the whole recompute pass.
				continue
			}
			recomputedClusters[baseline.ClusterID] = true
		}
		if int32(len(page)) < pageSize {
			break
		}
	}
	// P4.9 — one alerting.changed (kind: baseline) per distinct cluster per
	// pass (a pass rewrites every row, so per-row events would be spam). In
	// the dedicated worker process the runtime bus is Redis-attached and
	// fans out to the server pods' SSE relays. Nil-safe when unwired.
	for clusterID := range recomputedClusters {
		events.PublishChanged(runtimeDeps.Bus, "alerting", clusterID.String(), "", map[string]any{"kind": "baseline"})
	}
	return nil
}

// recomputeOneBaseline pulls a single fresh observation for the
// baseline, appends to the bounded ring buffer, recomputes stats,
// and upserts. The "fresh observation" comes from
// GetClusterHealthStatus — the cluster-level rollup table is the
// only metric source available in-process today. When sprint 072+N
// wires up TimescaleDB ingest, this is the single function that
// needs swapping.
func recomputeOneBaseline(ctx context.Context, q baselineRecomputeQuerier, b sqlc.AnomalyBaseline, now time.Time) error {
	value, ok := latestMetricValueForBaseline(ctx, q, b)
	samples := decodeRingBuffer(b.RecentSamples)
	if ok {
		samples = appendBounded(samples, value, now)
	}
	if len(samples) > anomalyMaxSamplesPerRecompute {
		samples = samples[len(samples)-anomalyMaxSamplesPerRecompute:]
	}

	stats := anomaly.Compute(ringBufferToAnomalySamples(samples))
	encoded, err := encodeRingBuffer(samples)
	if err != nil {
		return fmt.Errorf("encode ring buffer: %w", err)
	}

	lastValueAt := pgtype.Timestamptz{}
	if !stats.LastValueAt.IsZero() {
		lastValueAt = pgtype.Timestamptz{Time: stats.LastValueAt, Valid: true}
	}
	_, err = q.UpsertAnomalyBaseline(ctx, sqlc.UpsertAnomalyBaselineParams{
		ClusterID:     b.ClusterID,
		MetricName:    b.MetricName,
		WindowSeconds: b.WindowSeconds,
		SampleCount:   int32(stats.Count),
		Mean:          stats.Mean,
		Stddev:        stats.Stddev,
		MinValue:      stats.Min,
		MaxValue:      stats.Max,
		P50:           stats.P50,
		P95:           stats.P95,
		P99:           stats.P99,
		LastValue:     stats.LastValue,
		LastValueAt:   lastValueAt,
		RecentSamples: encoded,
	})
	if err != nil {
		return fmt.Errorf("upsert baseline: %w", err)
	}
	return nil
}

// latestMetricValueForBaseline returns the latest observed value
// for the baseline's metric. Returns (_, false) if no fresh sample
// is available (cluster has no health snapshot yet, or the metric
// name doesn't map to a known rollup). Callers don't fail in that
// case — they just leave the ring buffer untouched.
func latestMetricValueForBaseline(ctx context.Context, q baselineRecomputeQuerier, b sqlc.AnomalyBaseline) (float64, bool) {
	health, err := q.GetClusterHealthStatus(ctx, b.ClusterID)
	if err != nil {
		return 0, false
	}
	switch b.MetricName {
	case "cluster_cpu_percent", "cpu_usage_percent":
		return health.CpuUsagePercent, true
	case "cluster_memory_percent", "memory_usage_percent":
		return health.MemoryUsagePercent, true
	case "pod_count":
		return float64(health.PodCount), true
	case "node_count":
		return float64(health.NodeCount), true
	}
	return 0, false
}

// ringBufferEntry is the on-disk shape inside the recent_samples
// JSONB array. Using {v,t} (rather than [v,t]) is slightly more
// verbose but plays nicely with future schema evolution (e.g. adding
// per-sample labels).
type ringBufferEntry struct {
	V float64   `json:"v"`
	T time.Time `json:"t"`
}

func decodeRingBuffer(raw json.RawMessage) []ringBufferEntry {
	if len(raw) == 0 {
		return nil
	}
	var out []ringBufferEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func encodeRingBuffer(entries []ringBufferEntry) (json.RawMessage, error) {
	if entries == nil {
		entries = []ringBufferEntry{}
	}
	return json.Marshal(entries)
}

func appendBounded(entries []ringBufferEntry, value float64, when time.Time) []ringBufferEntry {
	entries = append(entries, ringBufferEntry{V: value, T: when})
	if len(entries) > anomalyRingBufferCap {
		// Drop oldest. Copying is cheap (small slices) and
		// avoids a memory-retention pitfall where the
		// underlying array stays alive forever.
		out := make([]ringBufferEntry, anomalyRingBufferCap)
		copy(out, entries[len(entries)-anomalyRingBufferCap:])
		entries = out
	}
	return entries
}

func ringBufferToAnomalySamples(entries []ringBufferEntry) []anomaly.Sample {
	out := make([]anomaly.Sample, 0, len(entries))
	for _, e := range entries {
		out = append(out, anomaly.Sample{Value: e.V, Time: e.T})
	}
	return out
}

// anomalyRuleConfig is the subset of the rule's configuration JSONB
// blob that the recompute + evaluator need.
type anomalyRuleConfig struct {
	IsAnomaly     bool
	Metric        string
	WindowSeconds int32
	StddevMult    float64
	Direction     string
	MinSamples    int32
}

// decodeAnomalyRuleConfig pulls anomaly-rule fields out of the rule's
// configuration JSONB. The rule's first-class "rule_kind" column is
// also represented in the JSONB as `rule_kind` so the handler can
// round-trip without us needing to extend the AlertRule sqlc struct
// (which would force a 7-site scan update for a 5-field addition).
func decodeAnomalyRuleConfig(raw json.RawMessage) anomalyRuleConfig {
	if len(raw) == 0 {
		return anomalyRuleConfig{}
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return anomalyRuleConfig{}
	}
	out := anomalyRuleConfig{}
	if kind, ok := cfg["rule_kind"].(string); ok && kind == "anomaly" {
		out.IsAnomaly = true
	}
	if metric, ok := cfg["metric"].(string); ok {
		out.Metric = metric
	}
	if v, ok := cfg["anomaly_window_seconds"]; ok {
		out.WindowSeconds = int32(floatFromAny(v))
	}
	if v, ok := cfg["anomaly_stddev"]; ok {
		out.StddevMult = floatFromAny(v)
	}
	if v, ok := cfg["anomaly_direction"].(string); ok {
		out.Direction = v
	}
	if v, ok := cfg["anomaly_min_samples"]; ok {
		out.MinSamples = int32(floatFromAny(v))
	}
	if out.Direction == "" {
		out.Direction = "above"
	}
	if out.MinSamples <= 0 {
		out.MinSamples = 50
	}
	if out.StddevMult <= 0 {
		out.StddevMult = 3.0
	}
	return out
}
