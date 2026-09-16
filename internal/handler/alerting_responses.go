package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *AlertingHandler) alertRuleResponse(ctx context.Context, rule sqlc.AlertRule) map[string]any {
	items := h.alertRuleResponses(ctx, []sqlc.AlertRule{rule})
	if len(items) == 0 {
		return map[string]any{}
	}
	return items[0]
}

// alertRuleResponses builds the response payloads for a page of rules with
// batched lookups: one query aggregates active-alert counts by rule_id, one
// bulk-loads rule<->channel links, and one bulk-loads the referenced
// clusters — replacing the ~3-queries-per-rule (incl. a 200-event fetch just
// to COUNT active alerts) the single-rule path used to run per rule.
func (h *AlertingHandler) alertRuleResponses(ctx context.Context, rules []sqlc.AlertRule) []map[string]any {
	ruleIDs := make([]uuid.UUID, 0, len(rules))
	clusterIDSet := map[uuid.UUID]struct{}{}
	for _, rule := range rules {
		ruleIDs = append(ruleIDs, rule.ID)
		if rule.ClusterID.Valid {
			clusterIDSet[uuid.UUID(rule.ClusterID.Bytes)] = struct{}{}
		}
	}

	activeByRule := map[uuid.UUID]int{}
	channelsByRule := map[uuid.UUID][]string{}
	if len(ruleIDs) > 0 {
		if counts, err := h.queries.CountActiveAlertsByRules(ctx, ruleIDs); err == nil {
			for _, c := range counts {
				activeByRule[c.RuleID] = int(c.ActiveCount)
			}
		}
		if links, err := h.queries.ListAlertRuleChannelsByRules(ctx, ruleIDs); err == nil {
			for _, link := range links {
				channelsByRule[link.AlertRuleID] = append(channelsByRule[link.AlertRuleID], link.NotificationChannelID.String())
			}
		}
	}

	clusterNames := map[uuid.UUID]any{}
	if len(clusterIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(clusterIDSet))
		for id := range clusterIDSet {
			ids = append(ids, id)
		}
		if clusters, err := h.queries.ListClustersByIDs(ctx, ids); err == nil {
			for _, cluster := range clusters {
				var name any = cluster.DisplayName
				if cluster.DisplayName == "" {
					name = cluster.Name
				}
				clusterNames[cluster.ID] = name
			}
		}
	}

	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		channelIDs := channelsByRule[rule.ID]
		if channelIDs == nil {
			channelIDs = []string{}
		}
		var clusterName any = nil
		if rule.ClusterID.Valid {
			if n, ok := clusterNames[uuid.UUID(rule.ClusterID.Bytes)]; ok {
				clusterName = n
			}
		}
		items = append(items, alertRuleResponseFields(rule, activeByRule[rule.ID], clusterName, channelIDs))
	}
	return items
}

func alertRuleResponseFields(rule sqlc.AlertRule, activeAlerts int, clusterName any, channelIDs []string) map[string]any {
	cfg := decodeJSONMap(rule.Configuration)
	return map[string]any{
		"id":                     rule.ID.String(),
		"name":                   rule.Name,
		"description":            stringFromMap(cfg, "description"),
		"type":                   defaultString(stringFromMap(cfg, "type"), rule.RuleType),
		"severity":               rule.Severity,
		"clusterId":              nullableUUID(rule.ClusterID),
		"clusterName":            clusterName,
		"namespace":              stringFromMap(cfg, "namespace"),
		"enabled":                rule.Enabled,
		"query":                  stringFromMap(cfg, "query"),
		"threshold":              numberOrNil(cfg["threshold"]),
		"duration":               defaultString(stringFromMap(cfg, "duration"), "5m"),
		"activeAlerts":           activeAlerts,
		"labels":                 mapStringMap(cfg["labels"]),
		"annotations":            mapStringMap(cfg["annotations"]),
		"notificationChannelIds": channelIDs,
		// Sprint 072 anomaly-rule surface. ruleKind defaults to
		// "threshold" so the frontend can branch its rule-edit
		// form without a follow-up GET.
		"ruleKind":             defaultString(stringFromMap(cfg, "rule_kind"), "threshold"),
		"metric":               stringFromMap(cfg, "metric"),
		"anomalyStddev":        numberOrNil(cfg["anomaly_stddev"]),
		"anomalyWindowSeconds": numberOrNil(cfg["anomaly_window_seconds"]),
		"anomalyMinSamples":    numberOrNil(cfg["anomaly_min_samples"]),
		"anomalyDirection":     defaultString(stringFromMap(cfg, "anomaly_direction"), ""),
		"createdAt":            rule.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":            rule.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func notificationChannelResponse(channel sqlc.NotificationChannel) map[string]any {
	return map[string]any{
		"id":        channel.ID.String(),
		"name":      channel.Name,
		"type":      channel.ChannelType,
		"enabled":   channel.Enabled,
		"config":    redactChannelConfig(decodeJSONMap(channel.Configuration)),
		"createdAt": channel.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt": channel.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// redactChannelConfig masks delivery secrets before a notification-channel
// config is returned on any read path. A channel config *is* the credential
// (Slack webhook URL, PagerDuty routing key, generic webhook token), so any
// value under a secret-shaped key is replaced with a marker while the key is
// preserved so the UI can still tell the channel is configured. Callers who
// need the real value must re-enter it on update (write-only secret pattern).
func redactChannelConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return cfg
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if channelSecretKey(k) {
			if v == nil || v == "" {
				out[k] = v
			} else {
				out[k] = "[redacted]"
			}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = redactChannelConfig(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func channelSecretKey(key string) bool {
	n := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	for _, s := range []string{"url", "token", "key", "secret", "password", "webhook", "credential"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func (h *AlertingHandler) alertEventResponse(ctx context.Context, event sqlc.AlertEvent) map[string]any {
	ruleName := ""
	severity := "warning"
	clusterName := any(nil)
	if rule, err := h.queries.GetAlertRuleByID(ctx, event.RuleID); err == nil {
		ruleName = rule.Name
		severity = rule.Severity
	}
	if event.ClusterID.Valid {
		if cluster, err := h.queries.GetClusterByID(ctx, uuid.UUID(event.ClusterID.Bytes)); err == nil {
			clusterName = cluster.DisplayName
			if clusterName == "" {
				clusterName = cluster.Name
			}
		}
	}
	return alertEventResponseFields(event, ruleName, severity, clusterName)
}

// alertEventRefLoader is the narrow batch surface alertEventResponsesBatched
// needs. AlertingQuerier satisfies it; a test can supply a counting fake.
type alertEventRefLoader interface {
	ListAlertRulesByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.AlertRule, error)
	ListClustersByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.Cluster, error)
}

// alertEventResponsesBatched builds the response payloads for a page of events
// with batched lookups: one ListAlertRulesByIDs + one ListClustersByIDs for the
// whole page, mirroring alertRuleResponses. Replaces the per-row
// GetAlertRuleByID + GetClusterByID N+1 (~2 queries per event) the event-list
// path used to run.
func alertEventResponsesBatched(ctx context.Context, q alertEventRefLoader, events []sqlc.AlertEvent) []map[string]any {
	ruleIDSet := map[uuid.UUID]struct{}{}
	clusterIDSet := map[uuid.UUID]struct{}{}
	for _, event := range events {
		ruleIDSet[event.RuleID] = struct{}{}
		if event.ClusterID.Valid {
			clusterIDSet[uuid.UUID(event.ClusterID.Bytes)] = struct{}{}
		}
	}

	type ruleInfo struct {
		name     string
		severity string
	}
	rulesByID := map[uuid.UUID]ruleInfo{}
	if len(ruleIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(ruleIDSet))
		for id := range ruleIDSet {
			ids = append(ids, id)
		}
		if rules, err := q.ListAlertRulesByIDs(ctx, ids); err == nil {
			for _, rule := range rules {
				rulesByID[rule.ID] = ruleInfo{name: rule.Name, severity: rule.Severity}
			}
		}
	}

	clusterNames := map[uuid.UUID]any{}
	if len(clusterIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(clusterIDSet))
		for id := range clusterIDSet {
			ids = append(ids, id)
		}
		if clusters, err := q.ListClustersByIDs(ctx, ids); err == nil {
			for _, cluster := range clusters {
				var name any = cluster.DisplayName
				if cluster.DisplayName == "" {
					name = cluster.Name
				}
				clusterNames[cluster.ID] = name
			}
		}
	}

	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		ruleName := ""
		severity := "warning"
		if info, ok := rulesByID[event.RuleID]; ok {
			ruleName = info.name
			severity = info.severity
		}
		var clusterName any = nil
		if event.ClusterID.Valid {
			if n, ok := clusterNames[uuid.UUID(event.ClusterID.Bytes)]; ok {
				clusterName = n
			}
		}
		items = append(items, alertEventResponseFields(event, ruleName, severity, clusterName))
	}
	return items
}

// alertEventResponseFields renders one event into its wire shape from already
// resolved rule/cluster metadata. Shared by the single-event path and the
// batched list path so both stay byte-identical.
func alertEventResponseFields(event sqlc.AlertEvent, ruleName, severity string, clusterName any) map[string]any {
	details := decodeJSONMap(event.Details)
	resp := map[string]any{
		"id":             event.ID.String(),
		"ruleId":         event.RuleID.String(),
		"ruleName":       ruleName,
		"severity":       severity,
		"status":         event.Status,
		"message":        event.Message,
		"clusterId":      nullableUUID(event.ClusterID),
		"clusterName":    clusterName,
		"namespace":      stringFromMap(details, "namespace"),
		"resource":       stringFromMap(details, "resource"),
		"labels":         mapStringMap(details["labels"]),
		"firedAt":        event.FiredAt.UTC().Format(time.RFC3339),
		"acknowledgedAt": nullableTime(event.AcknowledgedAt),
		"acknowledgedBy": nullableUUID(event.AcknowledgedByID),
		"resolvedAt":     nullableTime(event.ResolvedAt),
		"resolvedBy":     nil,
	}
	return resp
}

func alertSilenceResponse(silence sqlc.AlertSilence) map[string]any {
	return map[string]any{
		"id":        silence.ID.String(),
		"reason":    silence.Reason,
		"matchers":  map[string]string{"cluster_id": nullableUUIDString(silence.ClusterID), "rule_id": nullableUUIDString(silence.RuleID)},
		"startsAt":  silence.StartsAt.UTC().Format(time.RFC3339),
		"endsAt":    silence.EndsAt.UTC().Format(time.RFC3339),
		"duration":  silence.EndsAt.Sub(silence.StartsAt).String(),
		"createdBy": nullableUUID(silence.CreatedByID),
		"createdAt": silence.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func parseMatcherUUID(matchers map[string]string, keys ...string) *uuid.UUID {
	for _, key := range keys {
		if matchers == nil {
			return nil
		}
		value := strings.TrimSpace(matchers[key])
		if value == "" {
			continue
		}
		if id, err := uuid.Parse(value); err == nil {
			return &id
		}
	}
	return nil
}

func alertRuleConfiguration(req CreateAlertRuleRequest) json.RawMessage {
	cfg := map[string]any{
		"description": req.Description,
		"type":        defaultString(req.Type, req.RuleType),
		"query":       req.Query,
		"duration":    defaultString(req.Duration, "5m"),
		"labels":      req.Labels,
		"annotations": req.Annotations,
	}
	if req.Threshold != nil {
		cfg["threshold"] = *req.Threshold
	}
	applyAnomalyFieldsToConfig(cfg, req)
	data, _ := json.Marshal(cfg)
	return data
}

func alertRuleConfigurationWithFallback(req CreateAlertRuleRequest, current json.RawMessage) json.RawMessage {
	cfg := decodeJSONMap(current)
	if req.Description != "" {
		cfg["description"] = req.Description
	}
	if req.Type != "" || req.RuleType != "" {
		cfg["type"] = defaultString(req.Type, req.RuleType)
	}
	if req.Query != "" {
		cfg["query"] = req.Query
	}
	if req.Duration != "" {
		cfg["duration"] = req.Duration
	}
	if req.Threshold != nil {
		cfg["threshold"] = *req.Threshold
	}
	if req.Labels != nil {
		cfg["labels"] = req.Labels
	}
	if req.Annotations != nil {
		cfg["annotations"] = req.Annotations
	}
	applyAnomalyFieldsToConfig(cfg, req)
	data, _ := json.Marshal(cfg)
	return data
}

// applyAnomalyFieldsToConfig stamps the sprint 072 anomaly-rule
// fields into the rule's configuration JSONB.
//
// We store these in the configuration blob (rather than only in the
// dedicated alert_rules columns) so the alert evaluator can read them
// without an additional query — the existing AlertRule sqlc struct
// is unmodified and the evaluator already decodes the configuration
// on every tick.
//
// Defaults: anomaly_stddev=3, anomaly_direction=above,
// anomaly_min_samples=50, anomaly_window_seconds=86400 (24h). These
// match the migration column defaults so the two stay in sync.
func applyAnomalyFieldsToConfig(cfg map[string]any, req CreateAlertRuleRequest) {
	if req.RuleKind != "" {
		cfg["rule_kind"] = req.RuleKind
	}
	if req.Metric != "" {
		cfg["metric"] = req.Metric
	}
	if req.AnomalyStddev != nil {
		cfg["anomaly_stddev"] = *req.AnomalyStddev
	}
	if req.AnomalyWindowSeconds != nil {
		cfg["anomaly_window_seconds"] = *req.AnomalyWindowSeconds
	}
	if req.AnomalyMinSamples != nil {
		cfg["anomaly_min_samples"] = *req.AnomalyMinSamples
	}
	if req.AnomalyDirection != "" {
		cfg["anomaly_direction"] = req.AnomalyDirection
	}
	// On a rule-kind switch from anomaly→threshold via UpdateRule,
	// the operator clears the anomaly metadata explicitly via an
	// empty kind. We DON'T do that automatically — leaving the old
	// anomaly fields in place is harmless because the threshold
	// evaluator never reads them.
}

// validateAnomalyRuleRequest reports a validation error string if
// req declares an anomaly rule but is missing required anomaly
// fields. Returns "" when the request is valid (anomaly or not).
// Called by CreateRule before persistence; UpdateRule does not
// re-validate so a rule that loses its metric mid-update silently
// short-circuits to no-fire (preferred over a hard 400 — the rule
// stays editable).
func validateAnomalyRuleRequest(req CreateAlertRuleRequest) string {
	if req.RuleKind != "anomaly" {
		return ""
	}
	if req.Metric == "" {
		return "anomaly rule requires a metric name"
	}
	if req.AnomalyStddev != nil && *req.AnomalyStddev <= 0 {
		return "anomaly_stddev must be > 0"
	}
	if req.AnomalyWindowSeconds != nil && *req.AnomalyWindowSeconds <= 0 {
		return "anomaly_window_seconds must be > 0"
	}
	if req.AnomalyMinSamples != nil && *req.AnomalyMinSamples < 0 {
		return "anomaly_min_samples must be >= 0"
	}
	if req.AnomalyDirection != "" {
		switch req.AnomalyDirection {
		case "above", "below", "either":
		default:
			return "anomaly_direction must be one of above|below|either"
		}
	}
	return ""
}

type alertRuleChannelQuerier interface {
	ListChannelsForAlertRule(context.Context, uuid.UUID) ([]sqlc.NotificationChannel, error)
	AddAlertRuleChannel(context.Context, sqlc.AddAlertRuleChannelParams) error
	RemoveAlertRuleChannel(context.Context, sqlc.RemoveAlertRuleChannelParams) error
}

func syncRuleChannelsWith(ctx context.Context, q alertRuleChannelQuerier, ruleID uuid.UUID, channelIDs []string) error {
	existing, err := q.ListChannelsForAlertRule(ctx, ruleID)
	if err != nil {
		return err
	}
	existingSet := map[string]sqlc.NotificationChannel{}
	for _, channel := range existing {
		existingSet[channel.ID.String()] = channel
	}
	targetSet := map[string]struct{}{}
	for _, id := range channelIDs {
		targetSet[id] = struct{}{}
		if _, ok := existingSet[id]; ok {
			continue
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			return err
		}
		if err := q.AddAlertRuleChannel(ctx, sqlc.AddAlertRuleChannelParams{
			AlertRuleID:           ruleID,
			NotificationChannelID: parsed,
		}); err != nil {
			return err
		}
	}
	for id, channel := range existingSet {
		if _, ok := targetSet[id]; ok {
			continue
		}
		if err := q.RemoveAlertRuleChannel(ctx, sqlc.RemoveAlertRuleChannelParams{
			AlertRuleID:           ruleID,
			NotificationChannelID: channel.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func decodeJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mapStringMap(v any) map[string]string {
	out := map[string]string{}
	raw, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, value := range raw {
		if s, ok := value.(string); ok {
			out[k] = s
		}
	}
	return out
}

func stringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func numberOrNil(v any) any {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return n
	default:
		return nil
	}
}

func nullableUUID(id pgtype.UUID) any {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return nil
}

func nullableUUIDString(id pgtype.UUID) string {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return ""
}

func nullableTime(ts pgtype.Timestamptz) any {
	if ts.Valid {
		return ts.Time.UTC().Format(time.RFC3339)
	}
	return nil
}
