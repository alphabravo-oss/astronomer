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
	for _, rule := range rules {
		triggered, message, details, targetClusterID, err := evaluateRule(ctx, rule)
		if err != nil {
			return err
		}
		silence, err := activeSilenceForRule(ctx, rule, targetClusterID)
		if err != nil {
			return err
		}
		existingEvents, err := runtimeDeps.Queries.ListAlertEventsByRule(ctx, sqlc.ListAlertEventsByRuleParams{
			RuleID: rule.ID,
			Limit:  200,
			Offset: 0,
		})
		if err != nil {
			return err
		}
		activeEvents := filterActiveEventsForCluster(existingEvents, targetClusterID)
		if !triggered {
			for _, event := range activeEvents {
				if err := runtimeDeps.Queries.UpdateAlertEventStatus(ctx, sqlc.UpdateAlertEventStatusParams{
					ID:         event.ID,
					Status:     "resolved",
					ResolvedAt: pgTime(time.Now()),
				}); err != nil {
					return err
				}
			}
			continue
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
			continue
		}
		if len(activeEvents) > 0 {
			runtimeLogger().InfoContext(ctx, "alert already active, skipping duplicate event", "rule_id", rule.ID.String())
			continue
		}
		if !cooldownElapsed(rule, existingEvents, targetClusterID) {
			runtimeLogger().InfoContext(ctx, "alert cooldown active, skipping event", "rule_id", rule.ID.String())
			continue
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
			continue
		}
		channels, err := runtimeDeps.Queries.ListChannelsForAlertRule(ctx, rule.ID)
		if err != nil {
			return err
		}
		for _, channel := range channels {
			if !channel.Enabled {
				continue
			}
			task, err := NewNotificationSendTask(NotificationSendPayload{
				Channel:    channel.ChannelType,
				Subject:    "Astronomer alert: " + rule.Name,
				Body:       message,
				Recipients: notificationRecipients(channel),
			})
			if err == nil && task != nil {
				runtimeLogger().InfoContext(ctx, "prepared alert notification",
					"event_id", event.ID.String(),
					"channel_id", channel.ID.String(),
					"recipient_count", len(notificationRecipients(channel)))
			}
		}
	}

	slog.InfoContext(ctx, "alert evaluation complete")
	return nil
}

func alertRulesForEvaluation(ctx context.Context, ruleID string) ([]sqlc.AlertRule, error) {
	if ruleID != "" {
		id, err := uuid.Parse(ruleID)
		if err != nil {
			return nil, fmt.Errorf("invalid rule_id: %w", err)
		}
		rules, err := runtimeDeps.Queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 500, Offset: 0})
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
	return runtimeDeps.Queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 500, Offset: 0})
}

func evaluateRule(ctx context.Context, rule sqlc.AlertRule) (bool, string, []byte, pgtype.UUID, error) {
	if !rule.Enabled {
		return false, "", nil, pgtype.UUID{}, nil
	}
	config := decodeWorkerJSONMap(rule.Configuration)
	if rule.ClusterID.Valid {
		details := baseRuleDetails(rule, config)
		cluster, err := runtimeDeps.Queries.GetClusterByID(ctx, uuid.UUID(rule.ClusterID.Bytes))
		if err != nil {
			return false, "", nil, pgtype.UUID{}, err
		}
		health, err := runtimeDeps.Queries.GetClusterHealthStatus(ctx, cluster.ID)
		if err != nil {
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
			return false, "", nil, pgtype.UUID{}, err
		} else if ok {
			return triggered, message, payload, clusterID, nil
		}
		return evaluateClusterRule(rule, config, cluster, health, details)
	}
	clusters, err := runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 500, Offset: 0})
	if err != nil {
		return false, "", nil, pgtype.UUID{}, err
	}
	for _, cluster := range clusters {
		details := baseRuleDetails(rule, config)
		details["scope"] = "global"
		health, err := runtimeDeps.Queries.GetClusterHealthStatus(ctx, cluster.ID)
		if err != nil {
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
		if triggered, message, payload, clusterID, ok, evalErr := evaluatePromQLRule(ctx, rule, config, cluster, details); evalErr != nil {
			return false, "", nil, pgtype.UUID{}, evalErr
		} else if ok {
			if triggered {
				return true, message, payload, clusterID, nil
			}
			continue
		}
		triggered, message, payload, clusterID, evalErr := evaluateClusterRule(rule, config, cluster, health, details)
		if evalErr != nil {
			return false, "", nil, pgtype.UUID{}, evalErr
		}
		if triggered {
			return true, message, payload, clusterID, nil
		}
	}
	blob, _ := json.Marshal(map[string]any{
		"severity":  rule.Severity,
		"rule_type": rule.RuleType,
		"scope":     "global",
		"query":     stringFromWorkerMap(config, "query"),
	})
	return false, "", blob, pgtype.UUID{}, nil
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

func activeSilenceForRule(ctx context.Context, rule sqlc.AlertRule, clusterID pgtype.UUID) (*sqlc.AlertSilence, error) {
	silences, err := runtimeDeps.Queries.ListAlertSilences(ctx, sqlc.ListAlertSilencesParams{
		Limit:  500,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, silence := range silences {
		if now.Before(silence.StartsAt.UTC()) || now.After(silence.EndsAt.UTC()) {
			continue
		}
		if !silence.RuleID.Valid && !silence.ClusterID.Valid {
			return &silence, nil
		}
		if silence.RuleID.Valid && uuid.UUID(silence.RuleID.Bytes) == rule.ID {
			if silence.ClusterID.Valid && (!clusterID.Valid || uuid.UUID(silence.ClusterID.Bytes) != uuid.UUID(clusterID.Bytes)) {
				continue
			}
			return &silence, nil
		}
		if clusterID.Valid && silence.ClusterID.Valid && uuid.UUID(silence.ClusterID.Bytes) == uuid.UUID(clusterID.Bytes) {
			return &silence, nil
		}
	}
	return nil, nil
}

func evaluateClusterRule(rule sqlc.AlertRule, config map[string]any, cluster sqlc.Cluster, health sqlc.ClusterHealthStatus, details map[string]any) (bool, string, []byte, pgtype.UUID, error) {
	displayName := firstNonEmptyString(cluster.DisplayName, cluster.Name)
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
		if health.NodeCount == 0 {
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
		if event.Status == "firing" || event.Status == "acknowledged" || event.Status == "silenced" {
			return false
		}
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func notificationRecipients(channel sqlc.NotificationChannel) []string {
	var cfg map[string]any
	if err := json.Unmarshal(channel.Configuration, &cfg); err != nil {
		return nil
	}
	for _, key := range []string{"url", "webhook_url", "address", "email"} {
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
	threshold := floatFromAny(config["threshold"])
	if threshold == 0 {
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
	return true, fmt.Sprintf("Cluster %s query matched %s %.2f (value %.2f)", firstNonEmptyString(cluster.DisplayName, cluster.Name), comparison, threshold, value), blob, clusterID, true, nil
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
	rendered = strings.ReplaceAll(rendered, "{{cluster_name}}", firstNonEmptyString(cluster.DisplayName, cluster.Name))
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
