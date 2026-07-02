package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
)

// AlertEvaluationPayload contains parameters for alert evaluation.
type AlertEvaluationPayload struct {
	RuleID string `json:"rule_id,omitempty"` // empty = evaluate all rules
}

// NewAlertEvaluationTask creates a new alert evaluation task.
func NewAlertEvaluationTask(payload AlertEvaluationPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal alert evaluation payload: %w", err)
	}
	return asynq.NewTask("alert:evaluate", data), nil
}

// HandleAlertEvaluation evaluates all enabled alert rules against current metrics.
func HandleAlertEvaluation(ctx context.Context, t *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, "alert:evaluate", func() error {
		var p AlertEvaluationPayload
		if len(t.Payload()) > 0 {
			if err := json.Unmarshal(t.Payload(), &p); err != nil {
				return fmt.Errorf("unmarshal alert evaluation payload: %w", err)
			}
		}

		if p.RuleID != "" {
			slog.InfoContext(ctx, "evaluating alert rule", "rule_id", p.RuleID)
		} else {
			slog.InfoContext(ctx, "evaluating all alert rules")
		}

		if runtimeDeps.Queries == nil {
			slog.InfoContext(ctx, "alert evaluation runtime not configured, skipping DB evaluation")
			return nil
		}

		rules, err := alertRulesForEvaluation(ctx, p.RuleID)
		if err != nil {
			return err
		}
		// The active-silence set is identical for the whole tick, so list it
		// once here and filter in-memory per (rule, cluster) rather than
		// re-querying ListAlertSilences for every evaluation (R*C queries/tick
		// on a global-rule fan-out).
		silences, err := listActiveSilences(ctx)
		if err != nil {
			return err
		}
		// Hoist the fleet list + per-cluster health ONCE per tick and share it
		// across every global rule. Without this each global rule re-paged the
		// whole fleet and re-read GetClusterHealthStatus per cluster — G scans +
		// G×C point reads/tick even though the rows are identical within a tick.
		// Only built when a global (cluster-less) rule exists; cluster-scoped and
		// anomaly rules don't touch it.
		var fleet *fleetHealthSnapshot
		for _, rule := range rules {
			if !rule.ClusterID.Valid {
				fleet, err = buildFleetHealthSnapshot(ctx)
				if err != nil {
					return err
				}
				break
			}
		}
		for _, rule := range rules {
			// A global (rule.ClusterID invalid) rule produces one evaluation
			// PER cluster; a cluster-scoped or anomaly rule produces exactly
			// one. Fetch the rule's events once, then process each (rule,
			// cluster) evaluation independently so concurrent outages each
			// fire their own event and a recovered cluster is resolved even
			// while other clusters are still triggering.
			evaluations, err := evaluateRule(ctx, rule, fleet)
			if err != nil {
				return err
			}
			existingEvents, err := listAllAlertEventsByRule(ctx, rule.ID)
			if err != nil {
				return err
			}
			for _, eval := range evaluations {
				if err := processRuleEvaluation(ctx, rule, eval, existingEvents, silences); err != nil {
					return err
				}
			}
		}

		slog.InfoContext(ctx, "alert evaluation complete")
		return nil
	})
}

// dispatchAlertNotifications enqueues a notification:send task for every
// enabled channel bound to the rule. resolved=true marks it as a
// recovery notification so the formatters render the resolved variant
// (green swatch, PagerDuty event_action=resolve). Errors are logged but
// not returned: a single channel/enqueue failure must not abort the
// evaluation loop for the remaining rules.
func dispatchAlertNotifications(ctx context.Context, rule sqlc.AlertRule, event sqlc.AlertEvent, subject, body string, resolved bool) {
	channels, err := runtimeDeps.Queries.ListChannelsForAlertRule(ctx, rule.ID)
	if err != nil {
		runtimeLogger().ErrorContext(ctx, "failed to list channels for alert rule",
			"event_id", event.ID.String(), "rule_id", rule.ID.String(), "error", err)
		return
	}
	// Prefer the cluster the event actually fired on. Global rules have an
	// empty rule.ClusterID, so without this the operator could not tell which
	// cluster triggered the alert. Fall back to the rule's cluster when the
	// event carries none.
	clusterStr := ""
	if event.ClusterID.Valid {
		clusterStr = uuid.UUID(event.ClusterID.Bytes).String()
	} else if rule.ClusterID.Valid {
		clusterStr = uuid.UUID(rule.ClusterID.Bytes).String()
	}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		task, err := NewNotificationSendTask(NotificationSendPayload{
			Channel:    channel.ChannelType,
			Subject:    subject,
			Body:       body,
			Recipients: notificationRecipients(channel),
			// Plumb severity/cluster/rule through so the
			// Slack / PagerDuty / Teams formatters can render
			// colours + dedup keys + facts instead of just a
			// dumb text dump.
			Severity:  rule.Severity,
			ClusterID: clusterStr,
			RuleID:    rule.ID.String(),
			Resolved:  resolved,
		})
		if err != nil || task == nil {
			runtimeLogger().ErrorContext(ctx, "failed to build alert notification task",
				"event_id", event.ID.String(),
				"channel_id", channel.ID.String(),
				"error", err)
			continue
		}
		if runtimeDeps.Enqueuer == nil {
			runtimeLogger().WarnContext(ctx, "alert notification not delivered: enqueuer not configured",
				"event_id", event.ID.String(),
				"channel_id", channel.ID.String())
			continue
		}
		if _, enqErr := runtimeDeps.Enqueuer.Enqueue(task); enqErr != nil {
			runtimeLogger().ErrorContext(ctx, "failed to enqueue alert notification",
				"event_id", event.ID.String(),
				"channel_id", channel.ID.String(),
				"channel_type", channel.ChannelType,
				"error", enqErr)
			continue
		}
		runtimeLogger().InfoContext(ctx, "enqueued alert notification",
			"event_id", event.ID.String(),
			"channel_id", channel.ID.String(),
			"channel_type", channel.ChannelType,
			"severity", rule.Severity,
			"resolved", resolved,
			"recipient_count", len(notificationRecipients(channel)))
	}
}

// alertEvalSweepPageSize bounds each ListClusters/ListAlertRules/ListAlertSilences
// batch when the evaluator pages a full fleet. Mirrors
// argoCDAutoRegisterSweepPageSize so a fleet/rule set larger than one page is
// fully evaluated instead of silently truncated at the first 500 rows.
const alertEvalSweepPageSize int32 = 500

// listAllAlertRules pages through every alert rule. A single Limit:500 query
// silently dropped the 501st rule from evaluation; page until a short batch so
// every rule is evaluated each tick.
func listAllAlertRules(ctx context.Context) ([]sqlc.AlertRule, error) {
	var all []sqlc.AlertRule
	for offset := int32(0); ; offset += alertEvalSweepPageSize {
		page, err := runtimeDeps.Queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: alertEvalSweepPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if int32(len(page)) < alertEvalSweepPageSize {
			break
		}
	}
	return all, nil
}

// listActiveSilences pages through every alert silence once per tick so the
// evaluator can filter in-memory instead of re-issuing a ListAlertSilences
// query for every (rule, cluster) pair.
func listActiveSilences(ctx context.Context) ([]sqlc.AlertSilence, error) {
	var all []sqlc.AlertSilence
	for offset := int32(0); ; offset += alertEvalSweepPageSize {
		page, err := runtimeDeps.Queries.ListAlertSilences(ctx, sqlc.ListAlertSilencesParams{Limit: alertEvalSweepPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if int32(len(page)) < alertEvalSweepPageSize {
			break
		}
	}
	return all, nil
}

// listAllAlertEventsByRule pages through every alert event for a rule until a
// short batch, mirroring listAllAlertRules/listActiveSilences. A single
// Limit:200 read silently dropped every event past the 200 most-recently-fired
// rows. Since global rules now fan out one event PER cluster, a fleet larger
// than 200 firing clusters left a currently-firing cluster's event outside the
// window: it was never transitioned to resolved on recovery (stuck-firing) and,
// while still triggering, filterActiveEventsForCluster saw len==0 so a fresh
// event fired every tick (alert storm). Paging considers every cluster's active
// event regardless of fleet size.
func listAllAlertEventsByRule(ctx context.Context, ruleID uuid.UUID) ([]sqlc.AlertEvent, error) {
	var all []sqlc.AlertEvent
	for offset := int32(0); ; offset += alertEvalSweepPageSize {
		page, err := runtimeDeps.Queries.ListAlertEventsByRule(ctx, sqlc.ListAlertEventsByRuleParams{
			RuleID: ruleID,
			Limit:  alertEvalSweepPageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if int32(len(page)) < alertEvalSweepPageSize {
			break
		}
	}
	return all, nil
}

func alertRulesForEvaluation(ctx context.Context, ruleID string) ([]sqlc.AlertRule, error) {
	if ruleID != "" {
		id, err := uuid.Parse(ruleID)
		if err != nil {
			return nil, fmt.Errorf("invalid rule_id: %w", err)
		}
		rules, err := listAllAlertRules(ctx)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			if rule.ID == id {
				return []sqlc.AlertRule{rule}, nil
			}
		}
		return nil, fmt.Errorf("alert rule %s not found", ruleID)
	}
	return listAllAlertRules(ctx)
}

// ruleClusterEval is one (rule, cluster) evaluation outcome. Cluster-scoped
// and anomaly rules yield a single value; a global rule yields one per
// matching cluster so each cluster's firing/recovery is tracked
// independently instead of collapsing to the first triggering cluster.
type ruleClusterEval struct {
	triggered bool
	message   string
	details   []byte
	clusterID pgtype.UUID
}

// processRuleEvaluation applies a single (rule, cluster) evaluation: it
// resolves the cluster's active events when no longer triggering, silences
// them under an active silence, dedups against an already-active event, and
// otherwise fires a fresh event (subject to cooldown). Events are keyed by
// (rule, cluster) via filterActiveEventsForCluster / cooldownElapsed, so a
// global rule's clusters never clobber one another.
func processRuleEvaluation(ctx context.Context, rule sqlc.AlertRule, eval ruleClusterEval, existingEvents []sqlc.AlertEvent, silences []sqlc.AlertSilence) error {
	targetClusterID := eval.clusterID
	message := eval.message
	details := eval.details

	silence := matchActiveSilence(silences, rule, targetClusterID)
	activeEvents := filterActiveEventsForCluster(existingEvents, targetClusterID)
	if !eval.triggered {
		for _, event := range activeEvents {
			if err := runtimeDeps.Queries.UpdateAlertEventStatus(ctx, sqlc.UpdateAlertEventStatusParams{
				ID:         event.ID,
				Status:     "resolved",
				ResolvedAt: pgTime(time.Now()),
			}); err != nil {
				return err
			}
			// Only "firing"/"acknowledged" events represent an
			// alert that actually paged someone; "silenced" ones
			// never notified on trigger, so we don't notify on
			// resolve either.
			if event.Status == "firing" || event.Status == "acknowledged" {
				dispatchAlertNotifications(ctx, rule, event, "Astronomer alert resolved: "+rule.Name,
					fmt.Sprintf("Alert %q has resolved.", rule.Name), true)
			}
		}
		return nil
	}
	if silence != nil && len(activeEvents) > 0 {
		for _, event := range activeEvents {
			if event.Status == "silenced" {
				continue
			}
			if err := runtimeDeps.Queries.UpdateAlertEventStatus(ctx, sqlc.UpdateAlertEventStatusParams{
				ID:     event.ID,
				Status: "silenced",
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if len(activeEvents) > 0 {
		runtimeLogger().InfoContext(ctx, "alert already active, skipping duplicate event", "rule_id", rule.ID.String())
		return nil
	}
	if !cooldownElapsed(rule, existingEvents, targetClusterID) {
		runtimeLogger().InfoContext(ctx, "alert cooldown active, skipping event", "rule_id", rule.ID.String())
		return nil
	}
	status := "firing"
	if silence != nil {
		status = "silenced"
		detailMap := decodeWorkerJSONMap(details)
		detailMap["silence_reason"] = silence.Reason
		detailMap["silence_id"] = silence.ID.String()
		details, _ = json.Marshal(detailMap)
		message = fmt.Sprintf("%s (silenced: %s)", message, silence.Reason)
	}
	event, err := runtimeDeps.Queries.CreateAlertEvent(ctx, sqlc.CreateAlertEventParams{
		RuleID:    rule.ID,
		ClusterID: targetClusterID,
		Status:    status,
		Message:   message,
		Details:   details,
	})
	if err != nil {
		return err
	}
	if silence != nil {
		runtimeLogger().InfoContext(ctx, "alert matched active silence", "event_id", event.ID.String(), "rule_id", rule.ID.String())
		return nil
	}
	dispatchAlertNotifications(ctx, rule, event, "Astronomer alert: "+rule.Name, message, false)
	return nil
}

// evaluateRule evaluates a rule and returns one ruleClusterEval per cluster
// the rule covers. Cluster-scoped and anomaly rules return exactly one
// element; a global rule returns one per cluster so every currently-triggering
// cluster fires and every recovered cluster resolves within the same tick.
func evaluateRule(ctx context.Context, rule sqlc.AlertRule, fleet *fleetHealthSnapshot) ([]ruleClusterEval, error) {
	if !rule.Enabled {
		return []ruleClusterEval{{}}, nil
	}
	config := decodeWorkerJSONMap(rule.Configuration)
	// Sprint 072 — anomaly-kind rules use the rolling baseline
	// pre-aggregated by the anomaly_baseline_recompute worker, not
	// the existing static-threshold path. Branch here before the
	// cluster/global fan-out so the anomaly branch can short-circuit
	// to no-fire when no baseline row exists yet (identical to
	// "not enough samples").
	if stringFromWorkerMap(config, "rule_kind") == "anomaly" {
		return evaluateAnomalyRule(ctx, rule, config)
	}
	if rule.ClusterID.Valid {
		details := baseRuleDetails(rule, config)
		cluster, err := runtimeDeps.Queries.GetClusterByID(ctx, uuid.UUID(rule.ClusterID.Bytes))
		if err != nil {
			return nil, err
		}
		health, healthErr := runtimeDeps.Queries.GetClusterHealthStatus(ctx, cluster.ID)
		healthKnown := healthErr == nil
		if healthErr != nil {
			health = sqlc.ClusterHealthStatus{}
		}
		details["cluster_id"] = cluster.ID.String()
		details["cluster_name"] = cluster.Name
		details["cluster_status"] = cluster.Status
		details["last_heartbeat"] = nullableWorkerTime(cluster.LastHeartbeat)
		details["node_count"] = health.NodeCount
		details["pod_count"] = health.PodCount
		details["cpu_usage_percent"] = health.CpuUsagePercent
		details["memory_usage_percent"] = health.MemoryUsagePercent
		if triggered, message, payload, clusterID, ok, err := evaluatePromQLRule(ctx, rule, config, cluster, details); err != nil {
			return nil, err
		} else if ok {
			return []ruleClusterEval{{triggered: triggered, message: message, details: payload, clusterID: clusterID}}, nil
		}
		triggered, message, payload, clusterID, err := evaluateClusterRule(rule, config, cluster, health, healthKnown, details)
		if err != nil {
			return nil, err
		}
		return []ruleClusterEval{{triggered: triggered, message: message, details: payload, clusterID: clusterID}}, nil
	}
	// Global rule: evaluate every cluster in the fleet. The fleet list + a
	// cluster_id→health map are hoisted ONCE per tick (buildFleetHealthSnapshot,
	// like the silence hoist) and shared across all global rules — the rows are
	// identical across rules within a tick, so re-paging the fleet + re-reading
	// GetClusterHealthStatus per rule (G full-fleet scans + G×C point reads)
	// was pure redundancy. When fleet is nil (defensive: caller couldn't build
	// it) fall back to paging the fleet inline so behavior is preserved.
	evaluations := make([]ruleClusterEval, 0)
	if fleet != nil {
		for _, cluster := range fleet.clusters {
			eval, err := evaluateGlobalClusterRow(ctx, rule, config, cluster, fleet.health[cluster.ID], fleet.known[cluster.ID])
			if err != nil {
				return nil, err
			}
			evaluations = append(evaluations, eval)
		}
	} else {
		for offset := int32(0); ; offset += alertEvalSweepPageSize {
			clusters, err := runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: alertEvalSweepPageSize, Offset: offset})
			if err != nil {
				return nil, err
			}
			if len(clusters) == 0 {
				break
			}
			for _, cluster := range clusters {
				health, healthErr := runtimeDeps.Queries.GetClusterHealthStatus(ctx, cluster.ID)
				eval, evalErr := evaluateGlobalClusterRow(ctx, rule, config, cluster, health, healthErr == nil)
				if evalErr != nil {
					return nil, evalErr
				}
				evaluations = append(evaluations, eval)
			}
			if int32(len(clusters)) < alertEvalSweepPageSize {
				break
			}
		}
	}
	// No clusters: emit a single non-triggering, cluster-less evaluation so
	// any stale active events for this rule still resolve (preserves the old
	// "resolve everything when nothing triggers" behavior).
	if len(evaluations) == 0 {
		return []ruleClusterEval{{}}, nil
	}
	return evaluations, nil
}

// evaluateGlobalClusterRow evaluates a single cluster row for a global rule,
// using the pre-fetched (health, healthKnown) so the caller can share one
// per-tick fleet+health snapshot across every global rule instead of
// re-querying per rule. Mirrors the per-cluster body of the global fan-out.
func evaluateGlobalClusterRow(ctx context.Context, rule sqlc.AlertRule, config map[string]any, cluster sqlc.Cluster, health sqlc.ClusterHealthStatus, healthKnown bool) (ruleClusterEval, error) {
	details := baseRuleDetails(rule, config)
	details["scope"] = "global"
	details["cluster_id"] = cluster.ID.String()
	details["cluster_name"] = cluster.Name
	details["cluster_status"] = cluster.Status
	details["last_heartbeat"] = nullableWorkerTime(cluster.LastHeartbeat)
	details["node_count"] = health.NodeCount
	details["pod_count"] = health.PodCount
	details["cpu_usage_percent"] = health.CpuUsagePercent
	details["memory_usage_percent"] = health.MemoryUsagePercent
	if triggered, message, payload, clusterID, ok, evalErr := evaluatePromQLRule(ctx, rule, config, cluster, details); evalErr != nil {
		return ruleClusterEval{}, evalErr
	} else if ok {
		return ruleClusterEval{triggered: triggered, message: message, details: payload, clusterID: clusterID}, nil
	}
	triggered, message, payload, clusterID, evalErr := evaluateClusterRule(rule, config, cluster, health, healthKnown, details)
	if evalErr != nil {
		return ruleClusterEval{}, evalErr
	}
	return ruleClusterEval{triggered: triggered, message: message, details: payload, clusterID: clusterID}, nil
}

// fleetHealthSnapshot is the once-per-tick fleet view shared across all global
// rule evaluations: the full non-decommissioned cluster list plus a
// cluster_id→health map (and a "was the health read known" set). Building it
// once collapses the old G full-fleet scans + G×C GetClusterHealthStatus reads
// (G global rules, C clusters) down to one scan + C reads per tick.
type fleetHealthSnapshot struct {
	clusters []sqlc.Cluster
	health   map[uuid.UUID]sqlc.ClusterHealthStatus
	known    map[uuid.UUID]bool
}

// buildFleetHealthSnapshot pages the entire fleet once and reads each cluster's
// health once, returning the shared snapshot the global-rule fan-out reads from.
func buildFleetHealthSnapshot(ctx context.Context) (*fleetHealthSnapshot, error) {
	snap := &fleetHealthSnapshot{
		health: map[uuid.UUID]sqlc.ClusterHealthStatus{},
		known:  map[uuid.UUID]bool{},
	}
	for offset := int32(0); ; offset += alertEvalSweepPageSize {
		clusters, err := runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: alertEvalSweepPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(clusters) == 0 {
			break
		}
		for _, cluster := range clusters {
			snap.clusters = append(snap.clusters, cluster)
			health, healthErr := runtimeDeps.Queries.GetClusterHealthStatus(ctx, cluster.ID)
			if healthErr != nil {
				snap.health[cluster.ID] = sqlc.ClusterHealthStatus{}
				snap.known[cluster.ID] = false
				continue
			}
			snap.health[cluster.ID] = health
			snap.known[cluster.ID] = true
		}
		if int32(len(clusters)) < alertEvalSweepPageSize {
			break
		}
	}
	return snap, nil
}

func filterActiveEvents(events []sqlc.AlertEvent) []sqlc.AlertEvent {
	out := make([]sqlc.AlertEvent, 0, len(events))
	for _, event := range events {
		if event.Status == "firing" || event.Status == "acknowledged" || event.Status == "silenced" {
			out = append(out, event)
		}
	}
	return out
}

func filterActiveEventsForCluster(events []sqlc.AlertEvent, clusterID pgtype.UUID) []sqlc.AlertEvent {
	filtered := filterActiveEvents(events)
	if !clusterID.Valid {
		return filtered
	}
	out := make([]sqlc.AlertEvent, 0, len(filtered))
	for _, event := range filtered {
		if event.ClusterID.Valid && uuid.UUID(event.ClusterID.Bytes) == uuid.UUID(clusterID.Bytes) {
			out = append(out, event)
		}
	}
	return out
}

// matchActiveSilence selects the silence covering (rule, cluster) from a
// pre-fetched silence list. The list is gathered once per tick by
// listActiveSilences so this is a pure in-memory filter — no per-call DB query.
func matchActiveSilence(silences []sqlc.AlertSilence, rule sqlc.AlertRule, clusterID pgtype.UUID) *sqlc.AlertSilence {
	now := time.Now().UTC()
	for i := range silences {
		silence := silences[i]
		if now.Before(silence.StartsAt.UTC()) || now.After(silence.EndsAt.UTC()) {
			continue
		}
		if !silence.RuleID.Valid && !silence.ClusterID.Valid {
			return &silences[i]
		}
		if silence.RuleID.Valid && uuid.UUID(silence.RuleID.Bytes) == rule.ID {
			if silence.ClusterID.Valid && (!clusterID.Valid || uuid.UUID(silence.ClusterID.Bytes) != uuid.UUID(clusterID.Bytes)) {
				continue
			}
			return &silences[i]
		}
		if clusterID.Valid && silence.ClusterID.Valid && uuid.UUID(silence.ClusterID.Bytes) == uuid.UUID(clusterID.Bytes) {
			return &silences[i]
		}
	}
	return nil
}

func evaluateClusterRule(rule sqlc.AlertRule, config map[string]any, cluster sqlc.Cluster, health sqlc.ClusterHealthStatus, healthKnown bool, details map[string]any) (bool, string, []byte, pgtype.UUID, error) {
	displayName := strutil.FirstNonBlank(cluster.DisplayName, cluster.Name)
	clusterID := pgtype.UUID{Bytes: cluster.ID, Valid: true}
	comparison := stringFromWorkerMap(config, "comparison")
	if comparison == "" {
		comparison = defaultComparisonForRule(rule.RuleType, config)
	}
	threshold := floatFromAny(config["threshold"])
	metricName, metricValue := metricForRule(config, health)
	details["comparison"] = comparison
	if metricName != "" {
		details["metric"] = metricName
		details["metric_value"] = metricValue
	}
	if threshold > 0 {
		details["threshold"] = threshold
	}

	switch rule.RuleType {
	case "absence", "deadman":
		expected := expectedInterval(config)
		details["expected_interval_seconds"] = int(expected.Seconds())
		if cluster.Status == "disconnected" || !cluster.LastHeartbeat.Valid || cluster.LastHeartbeat.Time.UTC().Before(time.Now().UTC().Add(-expected)) {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s heartbeat is missing", displayName), blob, clusterID, nil
		}
		return false, "", nil, clusterID, nil
	case "change":
		if threshold <= 0 || metricName == "" {
			return false, "", nil, clusterID, nil
		}
		baseline := baselineForChange(metricName, health)
		delta := math.Abs(metricValue - baseline)
		details["baseline"] = baseline
		details["change"] = delta
		if compareMetric(delta, threshold, comparison) {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s %s changed by %.1f", displayName, metricName, delta), blob, clusterID, nil
		}
		return false, "", nil, clusterID, nil
	case "anomaly":
		if threshold <= 0 || metricName == "" {
			return false, "", nil, clusterID, nil
		}
		score := anomalyScore(metricName, metricValue)
		details["anomaly_score"] = score
		if compareMetric(score, threshold, comparison) {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s %s anomaly score %.1f crossed %.1f", displayName, metricName, score, threshold), blob, clusterID, nil
		}
		return false, "", nil, clusterID, nil
	default:
		if cluster.Status == "disconnected" {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s is disconnected", displayName), blob, clusterID, nil
		}
		if threshold > 0 && metricName != "" && compareMetric(metricValue, threshold, comparison) {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s %s %.1f matched %s %.1f", displayName, metricName, metricValue, comparison, threshold), blob, clusterID, nil
		}
		// Only fire "zero nodes" when we actually HAVE a health row saying so.
		// A missing health row (no metrics yet — onboarding / metrics lag / a
		// transient DB read error) collapses to a zero-value struct whose
		// NodeCount is 0; firing on that produced spurious "reports zero nodes"
		// pages for freshly-adopted clusters.
		if healthKnown && health.NodeCount == 0 {
			blob, _ := json.Marshal(details)
			return true, fmt.Sprintf("Cluster %s reports zero nodes", displayName), blob, clusterID, nil
		}
		return false, "", nil, clusterID, nil
	}
}

func cooldownElapsed(rule sqlc.AlertRule, events []sqlc.AlertEvent, clusterID pgtype.UUID) bool {
	if rule.CooldownMinutes <= 0 {
		return true
	}
	cutoff := time.Now().UTC().Add(-time.Duration(rule.CooldownMinutes) * time.Minute)
	for _, event := range events {
		if clusterID.Valid && (!event.ClusterID.Valid || uuid.UUID(event.ClusterID.Bytes) != uuid.UUID(clusterID.Bytes)) {
			continue
		}
		if event.FiredAt.UTC().Before(cutoff) {
			continue
		}
		// ANY alert that fired within the cooldown window suppresses a re-fire —
		// including one that already RESOLVED. The flap case (fire -> resolve ->
		// fire) is exactly what the cooldown exists to damp; previously only
		// still-active events blocked, so by the time this ran the firing event
		// was usually already resolved and every flap re-paged.
		return false
	}
	return true
}

func baseRuleDetails(rule sqlc.AlertRule, config map[string]any) map[string]any {
	return map[string]any{
		"severity":  rule.Severity,
		"rule_type": rule.RuleType,
		"query":     stringFromWorkerMap(config, "query"),
	}
}

func metricForRule(config map[string]any, health sqlc.ClusterHealthStatus) (string, float64) {
	metric := strings.ToLower(stringFromWorkerMap(config, "metric"))
	query := strings.ToLower(stringFromWorkerMap(config, "query"))
	switch {
	case strings.Contains(metric, "cpu") || strings.Contains(query, "cpu"):
		return "cpu_usage_percent", health.CpuUsagePercent
	case strings.Contains(metric, "memory") || strings.Contains(query, "memory"):
		return "memory_usage_percent", health.MemoryUsagePercent
	case strings.Contains(metric, "pod") || strings.Contains(query, "pod"):
		return "pod_count", float64(health.PodCount)
	case strings.Contains(metric, "node") || strings.Contains(query, "node"):
		return "node_count", float64(health.NodeCount)
	default:
		return "", 0
	}
}

func defaultComparisonForRule(ruleType string, config map[string]any) string {
	if comparison := stringFromWorkerMap(config, "comparison"); comparison != "" {
		return comparison
	}
	switch ruleType {
	case "absence", "deadman":
		return "gt"
	case "change":
		return "gte"
	default:
		return "gte"
	}
}

func compareMetric(value, threshold float64, comparison string) bool {
	switch comparison {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	case "eq":
		return value == threshold
	case "ne":
		return value != threshold
	default:
		return value >= threshold
	}
}

func expectedInterval(config map[string]any) time.Duration {
	if seconds := floatFromAny(config["expected_interval_seconds"]); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if duration := stringFromWorkerMap(config, "duration"); duration != "" {
		if parsed, err := time.ParseDuration(duration); err == nil {
			return parsed
		}
	}
	return 5 * time.Minute
}

func baselineForChange(metric string, health sqlc.ClusterHealthStatus) float64 {
	switch metric {
	case "cpu_usage_percent":
		return 50
	case "memory_usage_percent":
		return 50
	case "pod_count":
		return math.Max(1, float64(health.PodCount)/2)
	case "node_count":
		return math.Max(1, float64(health.NodeCount)-1)
	default:
		return 0
	}
}

func anomalyScore(metric string, value float64) float64 {
	switch metric {
	case "cpu_usage_percent", "memory_usage_percent":
		if value <= 85 {
			return 0
		}
		return value - 85
	case "pod_count":
		if value <= 100 {
			return 0
		}
		return value - 100
	case "node_count":
		if value >= 1 {
			return 0
		}
		return 100
	default:
		return 0
	}
}

func nullableWorkerTime(ts pgtype.Timestamptz) any {
	if !ts.Valid {
		return nil
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func decodeWorkerJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func stringFromWorkerMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// notificationRecipients extracts destination addresses from a
// channel's JSONB configuration. Each channel type stores the
// destination under a slightly different conventional key — Slack
// puts the webhook URL under `webhook_url`, PagerDuty under
// `routing_key`, MS Teams under `webhook_url`, generic webhook under
// `url`, email under `recipients` (array). We probe the keys in
// preference order so an old row with `url` still works for any
// channel type, and a new row with the canonical key works too.
//
// Order intentionally favors the type-specific key first so a misuse
// (`url` field on a slack channel that should be webhook_url) doesn't
// silently mask a misconfig.
// NotificationRecipients exposes the channel delivery-target extraction for
// callers outside this package (e.g. the handler's Test Channel endpoint).
func NotificationRecipients(channel sqlc.NotificationChannel) []string {
	return notificationRecipients(channel)
}

func notificationRecipients(channel sqlc.NotificationChannel) []string {
	var cfg map[string]any
	if err := json.Unmarshal(channel.Configuration, &cfg); err != nil {
		return nil
	}
	// Type-aware probe order. Channel type lookup keys:
	//   slack       -> webhook_url, url
	//   pagerduty   -> routing_key, integration_key, key
	//   msteams     -> webhook_url, url, workflow_url
	//   webhook     -> url, webhook_url
	//   email       -> recipients (array) or email/address (single)
	keys := []string{"webhook_url", "routing_key", "integration_key", "key", "workflow_url", "url", "address", "email"}
	for _, key := range keys {
		if v, ok := cfg[key].(string); ok && v != "" {
			return []string{v}
		}
	}
	if recipients, ok := cfg["recipients"].([]any); ok {
		out := make([]string, 0, len(recipients))
		for _, v := range recipients {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func evaluatePromQLRule(ctx context.Context, rule sqlc.AlertRule, config map[string]any, cluster sqlc.Cluster, details map[string]any) (bool, string, []byte, pgtype.UUID, bool, error) {
	queryTemplate := strings.TrimSpace(stringFromWorkerMap(config, "query"))
	if queryTemplate == "" {
		return false, "", nil, pgtype.UUID{}, false, nil
	}
	if !rule.ClusterID.Valid && !queryUsesClusterTemplate(queryTemplate) {
		return false, "", nil, pgtype.UUID{}, false, nil
	}

	client, selector, ok, err := monitoringClientForCluster(ctx, cluster.ID)
	if err != nil {
		return false, "", nil, pgtype.UUID{}, true, err
	}
	if !ok {
		return false, "", nil, pgtype.UUID{}, false, nil
	}

	query := renderAlertQuery(queryTemplate, cluster, selector)
	value, err := client.QueryScalar(ctx, query)
	if err != nil {
		return false, "", nil, pgtype.UUID{}, true, err
	}

	comparison := stringFromWorkerMap(config, "comparison")
	if comparison == "" {
		comparison = defaultComparisonForRule(rule.RuleType, config)
	}
	// Distinguish an unset threshold from an explicit 0. A rule meaning
	// "fire when the query value > 0" sets threshold=0; substituting the
	// hardcoded default (1) here silently broke that rule. Only fall back to
	// the default when the operator never supplied a threshold at all.
	threshold := floatFromAny(config["threshold"])
	if _, ok := config["threshold"]; !ok {
		threshold = defaultThresholdForPromRule(rule.RuleType)
	}

	clusterID := pgtype.UUID{Bytes: cluster.ID, Valid: true}
	details["query"] = query
	details["query_value"] = value
	details["comparison"] = comparison
	details["threshold"] = threshold
	details["evaluation_source"] = "promql"

	triggered := compareMetric(value, threshold, comparison)
	switch rule.RuleType {
	case "absence", "deadman":
		if threshold == 0 {
			threshold = 0
		}
		triggered = compareMetric(value, threshold, "lte")
		details["comparison"] = "lte"
		details["threshold"] = threshold
	case "anomaly":
		// Query is expected to compute the anomaly score/value directly.
		triggered = compareMetric(value, threshold, comparison)
	case "change":
		// Query is expected to compute the delta directly.
		triggered = compareMetric(math.Abs(value), threshold, comparison)
		details["query_value"] = math.Abs(value)
	}

	blob, _ := json.Marshal(details)
	if !triggered {
		return false, "", blob, clusterID, true, nil
	}
	return true, fmt.Sprintf("Cluster %s query matched %s %.2f (value %.2f)", strutil.FirstNonBlank(cluster.DisplayName, cluster.Name), comparison, threshold, value), blob, clusterID, true, nil
}

func monitoringClientForCluster(ctx context.Context, clusterID uuid.UUID) (*imonitoring.Client, monitoringSelector, bool, error) {
	if runtimeDeps.Queries == nil {
		return nil, monitoringSelector{}, false, nil
	}
	if joined, err := runtimeDeps.Queries.GetClusterMonitoringContext(ctx, clusterID); err == nil {
		client, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:           joined.QueryUrl,
			TenantID:           joined.TenantID,
			AuthType:           joined.AuthType,
			AuthConfig:         joined.AuthConfig,
			DefaultStepSeconds: joined.DefaultStepSeconds,
			TimeoutSeconds:     joined.TimeoutSeconds,
		})
		if err != nil {
			return nil, monitoringSelector{}, false, err
		}
		return client, monitoringSelector{
			Label: joined.ClusterLabel,
			Value: joined.ClusterLabelValue,
		}, true, nil
	} else if err != pgx.ErrNoRows {
		return nil, monitoringSelector{}, false, err
	}
	backend, err := runtimeDeps.Queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, monitoringSelector{}, false, nil
		}
		return nil, monitoringSelector{}, false, err
	}
	client, err := imonitoring.NewClient(imonitoring.BackendConfig{
		QueryURL:           backend.QueryUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		AuthConfig:         backend.AuthConfig,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
	})
	if err != nil {
		return nil, monitoringSelector{}, false, err
	}
	return client, monitoringSelector{Label: "cluster_id", Value: clusterID.String()}, true, nil
}

type monitoringSelector struct {
	Label string
	Value string
}

func queryUsesClusterTemplate(query string) bool {
	return strings.Contains(query, "{{cluster_selector}}") || strings.Contains(query, "{{cluster_label}}") || strings.Contains(query, "{{cluster_value}}") || strings.Contains(query, "{{cluster_id}}") || strings.Contains(query, "{{cluster_name}}")
}

func renderAlertQuery(query string, cluster sqlc.Cluster, selector monitoringSelector) string {
	label := selector.Label
	if label == "" {
		label = "cluster_id"
	}
	value := selector.Value
	if value == "" {
		value = cluster.ID.String()
	}
	rendered := strings.ReplaceAll(query, "{{cluster_selector}}", fmt.Sprintf(`%s="%s"`, label, escapePromWorkerLabel(value)))
	rendered = strings.ReplaceAll(rendered, "{{cluster_label}}", label)
	rendered = strings.ReplaceAll(rendered, "{{cluster_value}}", escapePromWorkerLabel(value))
	rendered = strings.ReplaceAll(rendered, "{{cluster_id}}", cluster.ID.String())
	rendered = strings.ReplaceAll(rendered, "{{cluster_name}}", strutil.FirstNonBlank(cluster.DisplayName, cluster.Name))
	return rendered
}

func escapePromWorkerLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

func defaultThresholdForPromRule(ruleType string) float64 {
	switch ruleType {
	case "absence", "deadman":
		return 0
	default:
		return 1
	}
}
