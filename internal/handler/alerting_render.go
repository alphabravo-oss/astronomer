package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"
)

func (h *AlertingHandler) syncSharedAlertingAssets(ctx context.Context) error {
	if h.requester == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	meta := sharedThanosMetadata(backend)
	clusterID := stringFromMap(meta, "managementClusterId")
	namespace := defaultString(stringFromMap(meta, "namespace"), "monitoring")
	if clusterID == "" {
		return nil
	}

	rules, err := h.queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	channels, err := h.queries.ListNotificationChannels(ctx, sqlc.ListNotificationChannelsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	silences, err := h.queries.ListAlertSilences(ctx, sqlc.ListAlertSilencesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}

	ruleContent, err := h.renderRulerRules(ctx, rules)
	if err != nil {
		return err
	}
	alertmanagerRouting, err := h.renderAlertmanagerConfig(ctx, channels, rules)
	if err != nil {
		return err
	}
	silenceContent, err := h.renderSilenceInventory(silences)
	if err != nil {
		return err
	}
	alertmanagerEndpoints, ok, err := h.renderThanosAlertmanagerEndpoints(ctx)
	if err != nil {
		return err
	}

	if err := ensureNamespaceWithRequester(ctx, h.requester, clusterID, namespace); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-ruler-rules", map[string]string{
		"rules.yaml": ruleContent,
	}); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-alertmanager-routing", map[string]string{
		"alertmanager.yaml": alertmanagerRouting,
	}); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-alert-silences", map[string]string{
		"silences.yaml": silenceContent,
	}); err != nil {
		return err
	}
	if ok {
		if err := applyAlertSecret(ctx, h.requester, clusterID, namespace, "astronomer-thanos-rule-alertmanagers", map[string]string{
			"config": alertmanagerEndpoints,
		}); err != nil {
			return err
		}
	}
	if err := h.persistSharedAlertingAssetHashes(ctx, backend, map[string]any{
		"rulerRules":              specHash(ruleContent),
		"alertmanagerRouting":     specHash(alertmanagerRouting),
		"silenceInventory":        specHash(silenceContent),
		"thanosAlertmanagerPeers": specHash(alertmanagerEndpoints),
	}); err != nil {
		return err
	}
	return nil
}

func (h *AlertingHandler) persistSharedAlertingAssetHashes(ctx context.Context, backend sqlc.MonitoringBackend, hashes map[string]any) error {
	// RMW site (migration 146). This is the least obvious of the four: it runs
	// as a side effect of an alert-rule or notification-channel edit, so a
	// version of it that re-marshalled the stored JSONB projection would let
	// "operator adds a Slack channel" delete the Thanos credential.
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	authCfg["sharedAlertingAssets"] = map[string]any{
		"hashes":    hashes,
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}

func (h *AlertingHandler) renderRulerRules(ctx context.Context, rules []sqlc.AlertRule) (string, error) {
	groupRules := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		cfg := decodeJSONMap(rule.Configuration)
		expr := strings.TrimSpace(stringFromMap(cfg, "query"))
		if expr == "" {
			expr = fallbackPromExpr(rule, cfg)
		}
		if expr == "" {
			continue
		}
		alertName := sanitizePromRuleName(rule.Name)
		labels := mapStringMap(cfg["labels"])
		if labels == nil {
			labels = map[string]string{}
		}
		labels["severity"] = defaultString(rule.Severity, "warning")
		labels["astronomer_rule_id"] = rule.ID.String()
		if rule.ClusterID.Valid {
			labels["astronomer_cluster_id"] = uuid.UUID(rule.ClusterID.Bytes).String()
		}
		annotations := mapStringMap(cfg["annotations"])
		if annotations == nil {
			annotations = map[string]string{}
		}
		if annotations["summary"] == "" {
			annotations["summary"] = strutil.FirstNonBlank(stringFromMap(cfg, "description"), rule.Name)
		}
		groupRules = append(groupRules, map[string]any{
			"alert":       alertName,
			"expr":        expr,
			"for":         defaultString(stringFromMap(cfg, "duration"), "5m"),
			"labels":      labels,
			"annotations": annotations,
		})
	}
	payload := map[string]any{
		"groups": []map[string]any{{
			"name":  "astronomer.rules",
			"rules": groupRules,
		}},
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderAlertmanagerConfig(ctx context.Context, channels []sqlc.NotificationChannel, rules []sqlc.AlertRule) (string, error) {
	// Load every rule<->channel link for the rule set in ONE query and
	// build a channel_id -> set(rule_id) map, instead of the old N+1 that
	// ran ListChannelsForAlertRule for every rule on every alerting
	// mutation (O(channels x rules) round-trips).
	channelRuleSet := map[uuid.UUID]map[uuid.UUID]bool{}
	if len(rules) > 0 {
		ruleIDs := make([]uuid.UUID, 0, len(rules))
		for _, rule := range rules {
			ruleIDs = append(ruleIDs, rule.ID)
		}
		links, err := h.queries.ListAlertRuleChannelsByRules(ctx, ruleIDs)
		if err != nil {
			return "", err
		}
		for _, link := range links {
			set := channelRuleSet[link.NotificationChannelID]
			if set == nil {
				set = map[uuid.UUID]bool{}
				channelRuleSet[link.NotificationChannelID] = set
			}
			set[link.AlertRuleID] = true
		}
	}

	receivers := []map[string]any{{"name": "null"}}
	routes := []map[string]any{}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		receiverName := "channel-" + channel.ID.String()
		receiver := map[string]any{"name": receiverName}
		cfg := decodeJSONMap(channel.Configuration)
		switch strings.ToLower(channel.ChannelType) {
		case "slack":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "webhook":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "email":
			if email, ok := firstConfigString(cfg, "email", "address"); ok {
				receiver["email_configs"] = []map[string]any{{"to": email, "send_resolved": true}}
			}
		default:
			continue
		}
		receivers = append(receivers, receiver)
		for _, rule := range rulesForChannel(rules, channelRuleSet[channel.ID]) {
			routes = append(routes, map[string]any{
				"receiver": receiverName,
				"matchers": []string{fmt.Sprintf(`astronomer_rule_id="%s"`, rule.ID.String())},
				"continue": true,
			})
		}
	}
	groupWait, groupInterval, repeatInterval := h.alertmanagerTiming(ctx)
	payload := map[string]any{
		"global": map[string]any{
			"resolve_timeout": "5m",
		},
		"route": map[string]any{
			"receiver":        "null",
			"group_by":        []string{"alertname", "astronomer_rule_id", "cluster"},
			"group_wait":      groupWait,
			"group_interval":  groupInterval,
			"repeat_interval": repeatInterval,
			"routes":          routes,
		},
		"receivers": receivers,
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderSilenceInventory(silences []sqlc.AlertSilence) (string, error) {
	items := make([]map[string]any, 0, len(silences))
	for _, silence := range silences {
		items = append(items, map[string]any{
			"id":        silence.ID.String(),
			"clusterId": nullableUUID(silence.ClusterID),
			"ruleId":    nullableUUID(silence.RuleID),
			"reason":    silence.Reason,
			"startsAt":  silence.StartsAt.UTC().Format(time.RFC3339),
			"endsAt":    silence.EndsAt.UTC().Format(time.RFC3339),
		})
	}
	raw, err := yaml.Marshal(map[string]any{"silences": items})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderThanosAlertmanagerEndpoints(ctx context.Context) (string, bool, error) {
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if strings.TrimSpace(backend.AlertmanagerUrl) == "" {
		return "", false, nil
	}
	payload := map[string]any{
		"alertmanagers": []map[string]any{{
			"static_configs": []string{backend.AlertmanagerUrl},
			"scheme":         alertmanagerScheme(backend.AlertmanagerUrl),
			"timeout":        "10s",
			"api_version":    "v2",
		}},
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// rulesForChannel returns the rules whose IDs are in ruleSet, preserving
// the order of allRules. ruleSet is the precomputed set of rule IDs linked
// to a given channel (see renderAlertmanagerConfig's bulk link load).
func rulesForChannel(allRules []sqlc.AlertRule, ruleSet map[uuid.UUID]bool) []sqlc.AlertRule {
	if len(ruleSet) == 0 {
		return nil
	}
	matched := make([]sqlc.AlertRule, 0, len(ruleSet))
	for _, rule := range allRules {
		if ruleSet[rule.ID] {
			matched = append(matched, rule)
		}
	}
	return matched
}

func fallbackPromExpr(rule sqlc.AlertRule, cfg map[string]any) string {
	clusterMatcher := ""
	if rule.ClusterID.Valid {
		clusterMatcher = fmt.Sprintf(`{cluster_id="%s"}`, uuid.UUID(rule.ClusterID.Bytes).String())
	}
	switch strings.ToLower(rule.RuleType) {
	case "absence", "deadman":
		return fmt.Sprintf(`absent(up%s)`, clusterMatcher)
	default:
		threshold := float64(0)
		if v := numberOrNil(cfg["threshold"]); v != nil {
			threshold, _ = v.(float64)
		}
		query := strings.ToLower(stringFromMap(cfg, "query"))
		switch {
		case strings.Contains(query, "cpu"):
			return fmt.Sprintf(`sum(rate(node_cpu_seconds_total{mode!="idle"%s}[5m])) > %.2f`, promMatcherSuffix(clusterMatcher), threshold)
		case strings.Contains(query, "memory"):
			return fmt.Sprintf(`sum(node_memory_MemTotal_bytes%s - node_memory_MemAvailable_bytes%s) > %.2f`, clusterMatcher, clusterMatcher, threshold)
		default:
			return ""
		}
	}
}

func promMatcherSuffix(matcher string) string {
	if matcher == "" {
		return ""
	}
	return "," + strings.TrimPrefix(strings.TrimSuffix(matcher, "}"), "{")
}

func sanitizePromRuleName(name string) string {
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_")
	return replacer.Replace(name)
}

func alertmanagerScheme(rawURL string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "https://") {
		return "https"
	}
	return "http"
}

func firstConfigString(cfg map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func firstNonEmptyString(values ...string) string {
	return strutil.FirstNonBlank(values...)
}

func ensureNamespaceWithRequester(ctx context.Context, requester K8sRequester, clusterID, namespace string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s", namespace)
	resp, err := requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
		},
	})
	if err != nil {
		return err
	}
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func applyConfigMap(ctx context.Context, requester K8sRequester, clusterID, namespace, name string, data map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"data": data,
	})
	if err != nil {
		return err
	}
	return applyNamedResource(ctx, requester, clusterID, namespace, "configmaps", name, body)
}

func applyAlertSecret(ctx context.Context, requester K8sRequester, clusterID, namespace, name string, stringData map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"type":       "Opaque",
		"stringData": stringData,
	})
	if err != nil {
		return err
	}
	return applyNamedResource(ctx, requester, clusterID, namespace, "secrets", name, body)
}

func applyNamedResource(ctx context.Context, requester K8sRequester, clusterID, namespace, plural, name string, body []byte) error {
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/%s/%s", namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/%s", namespace, plural)
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}
