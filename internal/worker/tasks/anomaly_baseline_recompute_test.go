package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeAnomalyQuerier implements both baselineRecomputeQuerier and
// anomalyEvalQuerier so a single fake can drive both the recompute
// and the evaluator-side tests.
type fakeAnomalyQuerier struct {
	baselines map[string]sqlc.AnomalyBaseline
	rules     []sqlc.AlertRule
	clusters  map[uuid.UUID]sqlc.Cluster
	health    map[uuid.UUID]sqlc.ClusterHealthStatus
	upserts   int
}

func newFakeAnomalyQuerier() *fakeAnomalyQuerier {
	return &fakeAnomalyQuerier{
		baselines: map[string]sqlc.AnomalyBaseline{},
		clusters:  map[uuid.UUID]sqlc.Cluster{},
		health:    map[uuid.UUID]sqlc.ClusterHealthStatus{},
	}
}

func baselineKey(clusterID uuid.UUID, metric string, window int32) string {
	return clusterID.String() + "|" + metric + "|" + intToStr(window)
}

func intToStr(n int32) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return sign + string(digits)
}

func (f *fakeAnomalyQuerier) ListAnomalyBaselines(_ context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error) {
	out := make([]sqlc.AnomalyBaseline, 0, len(f.baselines))
	for _, b := range f.baselines {
		out = append(out, b)
	}
	if int(arg.Offset) >= len(out) {
		return []sqlc.AnomalyBaseline{}, nil
	}
	out = out[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeAnomalyQuerier) GetAnomalyBaseline(_ context.Context, arg sqlc.GetAnomalyBaselineParams) (sqlc.AnomalyBaseline, error) {
	b, ok := f.baselines[baselineKey(arg.ClusterID, arg.MetricName, arg.WindowSeconds)]
	if !ok {
		return sqlc.AnomalyBaseline{}, pgx.ErrNoRows
	}
	return b, nil
}

func (f *fakeAnomalyQuerier) UpsertAnomalyBaseline(_ context.Context, arg sqlc.UpsertAnomalyBaselineParams) (sqlc.AnomalyBaseline, error) {
	f.upserts++
	key := baselineKey(arg.ClusterID, arg.MetricName, arg.WindowSeconds)
	existing, ok := f.baselines[key]
	if !ok {
		existing = sqlc.AnomalyBaseline{ID: uuid.New(), ClusterID: arg.ClusterID, MetricName: arg.MetricName, WindowSeconds: arg.WindowSeconds}
	}
	existing.SampleCount = arg.SampleCount
	existing.Mean = arg.Mean
	existing.Stddev = arg.Stddev
	existing.MinValue = arg.MinValue
	existing.MaxValue = arg.MaxValue
	existing.P50 = arg.P50
	existing.P95 = arg.P95
	existing.P99 = arg.P99
	existing.LastValue = arg.LastValue
	existing.LastValueAt = arg.LastValueAt
	existing.RecentSamples = arg.RecentSamples
	existing.UpdatedAt = time.Now()
	f.baselines[key] = existing
	return existing, nil
}

func (f *fakeAnomalyQuerier) ListAlertRules(_ context.Context, _ sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error) {
	return f.rules, nil
}

func (f *fakeAnomalyQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeAnomalyQuerier) GetClusterHealthStatus(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	h, ok := f.health[clusterID]
	if !ok {
		return sqlc.ClusterHealthStatus{}, errors.New("not found")
	}
	return h, nil
}

func (f *fakeAnomalyQuerier) ListClusters(_ context.Context, _ sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	out := make([]sqlc.Cluster, 0, len(f.clusters))
	for _, c := range f.clusters {
		out = append(out, c)
	}
	return out, nil
}

// --- Recompute tests ---

func TestBaselineRecompute_UpsertsRow(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 42.0}

	// Pre-existing baseline with a few samples.
	existing := sqlc.AnomalyBaseline{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		MetricName:    "cluster_cpu_percent",
		WindowSeconds: 86400,
		SampleCount:   3,
		RecentSamples: json.RawMessage(`[{"v":40,"t":"2026-01-01T00:00:00Z"},{"v":41,"t":"2026-01-01T00:05:00Z"},{"v":42,"t":"2026-01-01T00:10:00Z"}]`),
	}
	q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)] = existing

	if err := RunAnomalyBaselineRecompute(context.Background(), q, time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RunAnomalyBaselineRecompute: %v", err)
	}

	got := q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)]
	// After recompute, we should have 4 samples (3 existing + 1 new live read).
	if got.SampleCount != 4 {
		t.Fatalf("sample_count: want 4, got %d", got.SampleCount)
	}
	if got.LastValue != 42 {
		t.Fatalf("last_value: want 42, got %v", got.LastValue)
	}
}

func TestBaselineRecompute_PreservesRingBufferCap(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 99.0}

	// Build a ring buffer of exactly anomalyRingBufferCap entries.
	entries := make([]ringBufferEntry, anomalyRingBufferCap)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range entries {
		entries[i] = ringBufferEntry{V: float64(i), T: base.Add(time.Duration(i) * time.Minute)}
	}
	raw, _ := json.Marshal(entries)
	q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)] = sqlc.AnomalyBaseline{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		MetricName:    "cluster_cpu_percent",
		WindowSeconds: 86400,
		SampleCount:   int32(anomalyRingBufferCap),
		RecentSamples: raw,
	}

	if err := RunAnomalyBaselineRecompute(context.Background(), q, time.Now()); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	got := q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)]
	// Sample count must NEVER exceed the cap, even with continuous
	// appends — the bounded ring buffer is the load-bearing invariant
	// that keeps the JSONB column from growing unbounded.
	if got.SampleCount > int32(anomalyRingBufferCap) {
		t.Fatalf("ring buffer cap breached: want <= %d, got %d", anomalyRingBufferCap, got.SampleCount)
	}
	// Decode the ring buffer and verify the OLDEST entry got dropped
	// (i.e. value 0 is gone, value 1 is now the head).
	var decoded []ringBufferEntry
	if err := json.Unmarshal(got.RecentSamples, &decoded); err != nil {
		t.Fatalf("decode ring buffer: %v", err)
	}
	if len(decoded) != anomalyRingBufferCap {
		t.Fatalf("ring buffer length: want %d, got %d", anomalyRingBufferCap, len(decoded))
	}
	if decoded[0].V != 1 {
		t.Fatalf("oldest entry should have been dropped: want head v=1, got v=%v", decoded[0].V)
	}
	if decoded[len(decoded)-1].V != 99 {
		t.Fatalf("newest entry should be the live read (v=99), got %v", decoded[len(decoded)-1].V)
	}
}

func TestBaselineRecompute_SeedsRowsForAnomalyRules(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 50}

	ruleConfig, _ := json.Marshal(map[string]any{
		"rule_kind":              "anomaly",
		"metric":                 "cluster_cpu_percent",
		"anomaly_window_seconds": 86400,
		"anomaly_stddev":         3.0,
		"anomaly_direction":      "above",
		"anomaly_min_samples":    50,
	})
	q.rules = []sqlc.AlertRule{
		{
			ID:            uuid.New(),
			Name:          "cpu anomaly",
			ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
			Configuration: ruleConfig,
			Enabled:       true,
		},
	}

	if err := RunAnomalyBaselineRecompute(context.Background(), q, time.Now()); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	got, ok := q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)]
	if !ok {
		t.Fatalf("baseline not seeded for anomaly rule")
	}
	// The seeded row gets a live read immediately on the same pass,
	// so we expect SampleCount=1.
	if got.SampleCount != 1 {
		t.Fatalf("seeded baseline sample_count: want 1, got %d", got.SampleCount)
	}
}

// --- Alert evaluator branch tests ---
//
// These don't drive the full HandleAlertEvaluation function — that
// requires a complete asynq + leader stack. Instead they call
// evaluateAnomalyRule directly with a stubbed runtimeDeps.Queries.
// The TestAlertEvaluator_Anomaly* names match the spec.

func TestAlertEvaluator_AnomalyFire(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 95.0}

	// Pre-aggregated baseline: mean=50, stddev=5, count=100. A
	// current of 95 is 9σ above the mean — must fire.
	q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)] = sqlc.AnomalyBaseline{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		MetricName:    "cluster_cpu_percent",
		WindowSeconds: 86400,
		SampleCount:   100,
		Mean:          50,
		Stddev:        5,
		LastValue:     50,
		RecentSamples: json.RawMessage("[]"),
	}
	// no-op — EvaluateAnomalyRuleWith doesn't read runtimeDeps

	ruleCfg, _ := json.Marshal(map[string]any{
		"rule_kind":              "anomaly",
		"metric":                 "cluster_cpu_percent",
		"anomaly_window_seconds": 86400,
		"anomaly_stddev":         3.0,
		"anomaly_direction":      "above",
		"anomaly_min_samples":    50,
	})
	rule := sqlc.AlertRule{
		ID:            uuid.New(),
		Name:          "high-cpu-anomaly",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Configuration: ruleCfg,
		Enabled:       true,
		Severity:      "warning",
		RuleType:      "anomaly",
	}
	triggered, msg, _, _, err := EvaluateAnomalyRuleWith(context.Background(), q, rule, map[string]any{"rule_kind": "anomaly"})
	if err != nil {
		t.Fatalf("evaluateAnomalyRule: %v", err)
	}
	if !triggered {
		t.Fatalf("expected fire for current=95 vs mean=50 stddev=5 (9σ)")
	}
	if !strings.Contains(msg, "deviates from baseline") {
		t.Fatalf("message missing diagnostic text: %q", msg)
	}
}

func TestAlertEvaluator_AnomalyResolve(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	// Current=50 = exactly the mean — must not fire (which is what
	// the alert_evaluation main loop reads to mean "resolve any
	// active event for this rule/cluster").
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 50.0}
	q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)] = sqlc.AnomalyBaseline{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		MetricName:    "cluster_cpu_percent",
		WindowSeconds: 86400,
		SampleCount:   100,
		Mean:          50,
		Stddev:        5,
		LastValue:     50,
		RecentSamples: json.RawMessage("[]"),
	}
	// no-op — EvaluateAnomalyRuleWith doesn't read runtimeDeps

	ruleCfg, _ := json.Marshal(map[string]any{
		"rule_kind":              "anomaly",
		"metric":                 "cluster_cpu_percent",
		"anomaly_window_seconds": 86400,
		"anomaly_stddev":         3.0,
		"anomaly_direction":      "above",
		"anomaly_min_samples":    50,
	})
	rule := sqlc.AlertRule{
		ID:            uuid.New(),
		Name:          "high-cpu-anomaly",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Configuration: ruleCfg,
		Enabled:       true,
	}
	triggered, _, _, _, err := EvaluateAnomalyRuleWith(context.Background(), q, rule, map[string]any{"rule_kind": "anomaly"})
	if err != nil {
		t.Fatalf("evaluateAnomalyRule: %v", err)
	}
	if triggered {
		t.Fatalf("expected no-fire (resolve path) when current equals mean")
	}
}

func TestAlertEvaluator_AnomalyNoFireUnderMinSamples(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 9999.0}
	// SampleCount=5 < min_samples=50: cold-start gate must fire and
	// prevent the rule from triggering even on an absurd value.
	q.baselines[baselineKey(clusterID, "cluster_cpu_percent", 86400)] = sqlc.AnomalyBaseline{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		MetricName:    "cluster_cpu_percent",
		WindowSeconds: 86400,
		SampleCount:   5,
		Mean:          50,
		Stddev:        5,
		LastValue:     50,
		RecentSamples: json.RawMessage("[]"),
	}
	// no-op — EvaluateAnomalyRuleWith doesn't read runtimeDeps

	ruleCfg, _ := json.Marshal(map[string]any{
		"rule_kind":              "anomaly",
		"metric":                 "cluster_cpu_percent",
		"anomaly_window_seconds": 86400,
		"anomaly_stddev":         3.0,
		"anomaly_direction":      "above",
		"anomaly_min_samples":    50,
	})
	rule := sqlc.AlertRule{
		ID:            uuid.New(),
		Name:          "high-cpu-anomaly",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Configuration: ruleCfg,
		Enabled:       true,
	}
	triggered, _, _, _, err := EvaluateAnomalyRuleWith(context.Background(), q, rule, map[string]any{"rule_kind": "anomaly"})
	if err != nil {
		t.Fatalf("evaluateAnomalyRule: %v", err)
	}
	if triggered {
		t.Fatalf("cold-start gate must prevent fire when sample_count < min_samples")
	}
}

func TestAlertEvaluator_AnomalyNoFireWhenBaselineMissing(t *testing.T) {
	q := newFakeAnomalyQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "edge-1"}
	q.health[clusterID] = sqlc.ClusterHealthStatus{CpuUsagePercent: 99.0}
	// No baseline row at all — evaluator must treat as no-fire.
	// no-op — EvaluateAnomalyRuleWith doesn't read runtimeDeps

	ruleCfg, _ := json.Marshal(map[string]any{
		"rule_kind":              "anomaly",
		"metric":                 "cluster_cpu_percent",
		"anomaly_window_seconds": 86400,
		"anomaly_stddev":         3.0,
		"anomaly_direction":      "above",
		"anomaly_min_samples":    50,
	})
	rule := sqlc.AlertRule{
		ID:            uuid.New(),
		Name:          "high-cpu-anomaly",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Configuration: ruleCfg,
		Enabled:       true,
	}
	triggered, _, _, _, err := EvaluateAnomalyRuleWith(context.Background(), q, rule, map[string]any{"rule_kind": "anomaly"})
	if err != nil {
		t.Fatalf("evaluateAnomalyRule: %v", err)
	}
	if triggered {
		t.Fatalf("missing baseline must not fire (identical to under-min-samples)")
	}
}
