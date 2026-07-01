package tasks

// Sprint 072 — anomaly-rule branch of the alert evaluator.
//
// The path is small: pull the baseline row, gate on min-samples,
// hand to anomaly.IsAnomaly, and report. The single biggest reason
// this file exists separately from alert_evaluation.go is the runtime
// querier type assertion — we don't want to widen RuntimeQuerier for
// every new feature, so we mirror the pattern from
// HandleCleanupAlertEvents.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/anomaly"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
)

// anomalyEvalQuerier is the narrow interface needed by
// evaluateAnomalyRule. Mirrors baselineRecomputeQuerier — same
// methods, different package-internal name to keep the dependency
// graph explicit per-task.
type anomalyEvalQuerier interface {
	GetAnomalyBaseline(ctx context.Context, arg sqlc.GetAnomalyBaselineParams) (sqlc.AnomalyBaseline, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
}

// anomalyEvalSweepPageSize bounds each ListClusters batch for a global anomaly
// rule's fan-out; mirrors alertEvalSweepPageSize so a fleet larger than one
// page is fully evaluated.
const anomalyEvalSweepPageSize int32 = 500

// evaluateAnomalyRule is the dispatcher for rule_kind='anomaly'
// rules. Cluster-scoped rules evaluate against the single baseline
// row keyed by (cluster, metric, window). Global rules fan out one
// evaluation PER cluster so every cluster's anomaly/recovery is tracked
// independently — matching the threshold fan-out in evaluateRule — instead of
// collapsing to the first triggering cluster and stranding recovered clusters'
// events as perpetually firing.
func evaluateAnomalyRule(ctx context.Context, rule sqlc.AlertRule, config map[string]any) ([]ruleClusterEval, error) {
	q, ok := runtimeDeps.Queries.(anomalyEvalQuerier)
	if !ok {
		// Runtime querier doesn't expose anomaly methods — this
		// can happen in unit tests using a narrow fake. Treat
		// it identically to "no baseline row yet": a single
		// non-triggering eval so stale events still resolve.
		return []ruleClusterEval{{}}, nil
	}
	return EvaluateAnomalyRuleEvals(ctx, q, rule, config)
}

// EvaluateAnomalyRuleEvals is the testable core of evaluateAnomalyRule. It
// returns one ruleClusterEval per cluster the rule covers (exactly one for a
// cluster-scoped rule; one per fleet cluster for a global rule) so
// processRuleEvaluation can fire and resolve each cluster independently.
// Exported so tests can drive it with a narrow fake querier without having to
// satisfy the full RuntimeQuerier interface.
func EvaluateAnomalyRuleEvals(ctx context.Context, q anomalyEvalQuerier, rule sqlc.AlertRule, config map[string]any) ([]ruleClusterEval, error) {
	cfg := decodeAnomalyRuleConfig(rule.Configuration)
	if cfg.Metric == "" {
		// Misconfigured rule — operator forgot to pick a metric.
		// Don't fire; the UI surfaces the validation gap when the
		// rule was created.
		return []ruleClusterEval{{}}, nil
	}

	if rule.ClusterID.Valid {
		triggered, msg, blob, clusterID, err := evaluateAnomalyForCluster(ctx, q, rule, config, cfg, uuid.UUID(rule.ClusterID.Bytes))
		if err != nil {
			return nil, err
		}
		return []ruleClusterEval{{triggered: triggered, message: msg, details: blob, clusterID: clusterID}}, nil
	}
	// Global rule: page the whole fleet and emit one eval per cluster.
	evals := make([]ruleClusterEval, 0)
	for offset := int32(0); ; offset += anomalyEvalSweepPageSize {
		page, err := q.ListClusters(ctx, sqlc.ListClustersParams{Limit: anomalyEvalSweepPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, c := range page {
			triggered, msg, blob, clusterID, err := evaluateAnomalyForCluster(ctx, q, rule, config, cfg, c.ID)
			if err != nil {
				return nil, err
			}
			evals = append(evals, ruleClusterEval{triggered: triggered, message: msg, details: blob, clusterID: clusterID})
		}
		if int32(len(page)) < anomalyEvalSweepPageSize {
			break
		}
	}
	// No clusters: a single non-triggering, cluster-less eval so any stale
	// active events for this rule still resolve.
	if len(evals) == 0 {
		return []ruleClusterEval{{}}, nil
	}
	return evals, nil
}

// EvaluateAnomalyRuleWith preserves the pre-fan-out single-result contract for
// callers (and tests) that only need the first triggering cluster. It collapses
// the per-cluster evals: the first triggering cluster wins, otherwise the last
// non-triggering eval (carrying its details) is returned.
func EvaluateAnomalyRuleWith(ctx context.Context, q anomalyEvalQuerier, rule sqlc.AlertRule, config map[string]any) (bool, string, []byte, pgtype.UUID, error) {
	evals, err := EvaluateAnomalyRuleEvals(ctx, q, rule, config)
	if err != nil {
		return false, "", nil, pgtype.UUID{}, err
	}
	for _, e := range evals {
		if e.triggered {
			return true, e.message, e.details, e.clusterID, nil
		}
	}
	if len(evals) > 0 {
		last := evals[len(evals)-1]
		return false, last.message, last.details, last.clusterID, nil
	}
	return false, "", nil, pgtype.UUID{}, nil
}

func evaluateAnomalyForCluster(ctx context.Context, q anomalyEvalQuerier, rule sqlc.AlertRule, config map[string]any, cfg anomalyRuleConfig, clusterID uuid.UUID) (bool, string, []byte, pgtype.UUID, error) {
	cluster, err := q.GetClusterByID(ctx, clusterID)
	if err != nil {
		return false, "", nil, pgtype.UUID{}, err
	}

	window := cfg.WindowSeconds
	if window <= 0 {
		window = anomalyDefaultWindowSeconds
	}
	baseline, err := q.GetAnomalyBaseline(ctx, sqlc.GetAnomalyBaselineParams{
		ClusterID:     clusterID,
		MetricName:    cfg.Metric,
		WindowSeconds: window,
	})
	// No baseline row yet = treat exactly the same as
	// "not enough samples". The recompute worker provisions
	// the row on its next tick and the next evaluation will
	// have something to look at.
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return falseAnomaly(rule, cfg, cluster.ID, cluster.Name, "missing_baseline"), "", nil, pgtype.UUID{Bytes: clusterID, Valid: true}, nil
		}
		return false, "", nil, pgtype.UUID{}, err
	}

	// Pull the current observation. The baseline's last_value
	// is the most recent canonical sample (whatever the recompute
	// last wrote); the cluster health table gives a fresher live
	// value. We prefer live, fall back to baseline.last_value
	// when the live read fails.
	current, hasCurrent := liveMetricValue(ctx, q, cluster.ID, cfg.Metric)
	if !hasCurrent {
		current = baseline.LastValue
	}

	stats := anomaly.Stats{
		Count:       int(baseline.SampleCount),
		Mean:        baseline.Mean,
		Stddev:      baseline.Stddev,
		Min:         baseline.MinValue,
		Max:         baseline.MaxValue,
		P50:         baseline.P50,
		P95:         baseline.P95,
		P99:         baseline.P99,
		LastValue:   baseline.LastValue,
		LastValueAt: pgtimeOrZero(baseline.LastValueAt),
	}
	minSamples := int(cfg.MinSamples)
	if minSamples <= 0 {
		minSamples = 50
	}
	triggered := anomaly.IsAnomaly(stats, current, cfg.StddevMult, cfg.Direction, minSamples)

	details := baseRuleDetails(rule, config)
	details["cluster_id"] = cluster.ID.String()
	details["cluster_name"] = cluster.Name
	details["metric"] = cfg.Metric
	details["metric_value"] = current
	details["baseline_mean"] = baseline.Mean
	details["baseline_stddev"] = baseline.Stddev
	details["baseline_count"] = baseline.SampleCount
	details["window_seconds"] = window
	details["anomaly_direction"] = cfg.Direction
	details["anomaly_stddev_mult"] = cfg.StddevMult
	details["anomaly_min_samples"] = minSamples
	details["evaluation_source"] = "anomaly_baseline"

	pgClusterID := pgtype.UUID{Bytes: cluster.ID, Valid: true}
	if !triggered {
		blob, _ := json.Marshal(details)
		return false, "", blob, pgClusterID, nil
	}
	displayName := strutil.FirstNonBlank(cluster.DisplayName, cluster.Name)
	message := fmt.Sprintf(
		"Cluster %s %s=%.3f deviates from baseline mean=%.3f stddev=%.3f (threshold=%.1fσ, direction=%s)",
		displayName, cfg.Metric, current, baseline.Mean, baseline.Stddev, cfg.StddevMult, cfg.Direction,
	)
	blob, _ := json.Marshal(details)
	return true, message, blob, pgClusterID, nil
}

// liveMetricValue reads the current observed value for a metric on
// a cluster. Returns (_, false) when the metric is unknown or the
// cluster has no health snapshot.
func liveMetricValue(ctx context.Context, q anomalyEvalQuerier, clusterID uuid.UUID, metric string) (float64, bool) {
	health, err := q.GetClusterHealthStatus(ctx, clusterID)
	if err != nil {
		return 0, false
	}
	switch metric {
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

// falseAnomaly is the bool returned from a no-fire anomaly path. It
// exists so the call site can attach a "reason" tag we'll later
// surface in details. Returning a plain false is fine; this helper
// keeps the call-site readable and parameterizable.
func falseAnomaly(_ sqlc.AlertRule, _ anomalyRuleConfig, _ uuid.UUID, _, _ string) bool {
	return false
}

func pgtimeOrZero(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}
