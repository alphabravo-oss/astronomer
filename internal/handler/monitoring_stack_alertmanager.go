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
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

func (h *MonitoringHandler) updateSharedAlertmanagerMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedAlertmanagerRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	return h.updateSharedAlertmanagerMetadataWith(ctx, h.queries, backend, req, status)
}

func (h *MonitoringHandler) updateSharedAlertmanagerMetadataWith(ctx context.Context, q monitoringSharedMutationWriter, backend sqlc.MonitoringBackend, req SharedAlertmanagerRequest, status string) error {
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":   req.ManagementClusterID,
		"namespace":             defaultString(req.Namespace, "monitoring"),
		"releaseName":           defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"chartVersion":          req.ChartVersion,
		"replicas":              req.Replicas,
		"storageClass":          req.StorageClass,
		"storageSize":           req.StorageSize,
		"autoRollbackOnFailure": boolPtrValue(req.AutoRollbackOnFailure),
	})
	// RMW site (migration 146): same rule as updateSharedThanosMetadata — a
	// non-secret metadata stamp must not be able to delete the credential.
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	authCfg["sharedAlertmanager"] = map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           defaultString(req.Namespace, "monitoring"),
		"releaseName":         defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"status":              status,
		"chartVersion":        req.ChartVersion,
		"replicas":            req.Replicas,
		"storageClass":        req.StorageClass,
		"storageSize":         req.StorageSize,
		"lastAppliedSpecHash": appliedSpecHash,
		"updatedAt":           time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    defaultSharedAlertmanagerURL(backend.AlertmanagerUrl, req),
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = q.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}

func (h *MonitoringHandler) sharedAlertmanagerPayload(ctx context.Context, r *http.Request) (SharedAlertmanagerRequest, map[string]any, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedAlertmanagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "astronomer-alertmanager"
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "1.18.0"
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	if req.StorageSize == "" {
		req.StorageSize = "2Gi"
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	channels, err := h.queries.ListNotificationChannels(ctx, sqlc.ListNotificationChannelsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	rules, err := h.queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	routing, err := h.renderSharedAlertmanagerConfig(ctx, channels, rules)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(routing), &config); err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("failed to parse alertmanager config")
	}

	persistence := map[string]any{"enabled": true, "size": req.StorageSize}
	if req.StorageClass != "" {
		persistence["storageClass"] = req.StorageClass
	}
	values := map[string]any{
		"replicaCount": req.Replicas,
		"persistence":  persistence,
		"config":       config,
		"configmapReload": map[string]any{
			"enabled": true,
		},
	}
	return req, values, backend, nil
}

func (h *MonitoringHandler) renderSharedAlertmanagerConfig(ctx context.Context, channels []sqlc.NotificationChannel, rules []sqlc.AlertRule) (string, error) {
	// Load every rule<->channel link for the rule set in ONE query and build a
	// channel_id -> set(rule_id) map, instead of the old N+1 that ran
	// ListChannelsForAlertRule for every rule on every shared Alertmanager
	// Preview/Install/Upgrade/Replace (O(channels x rules) round-trips). Mirrors
	// AlertingHandler.renderAlertmanagerConfig.
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
		case "slack", "webhook":
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
	payload := map[string]any{
		"global": map[string]any{
			"resolve_timeout": "5m",
		},
		"route": map[string]any{
			"receiver": "null",
			"group_by": []string{"alertname", "astronomer_rule_id", "cluster"},
			// Defaults match platform_settings alertmanager.* (DIR-08); monitoring
			// stack render does not currently thread SettingsCache, so keep the
			// same registry defaults here for parity with AlertingHandler.
			"group_wait":      "30s",
			"group_interval":  "5m",
			"repeat_interval": "3h",
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

func defaultSharedThanosQueryURL(current string, req SharedThanosStackRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s-query-frontend.%s.svc.cluster.local:9090", defaultString(req.ReleaseName, "thanos"), defaultString(req.Namespace, "monitoring"))
}

func defaultSharedAlertmanagerURL(current string, req SharedAlertmanagerRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:9093", defaultString(req.ReleaseName, "astronomer-alertmanager"), defaultString(req.Namespace, "monitoring"))
}

func sharedAlertmanagerReplaceRequired(metadata map[string]any, req SharedAlertmanagerRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageClass"); current != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if current := stringFromMap(metadata, "storageSize"); current != "" && current != req.StorageSize {
		reasons = append(reasons, "storage size change")
	}
	return len(reasons) > 0, reasons
}

func sharedThanosReplaceRequired(metadata map[string]any, req SharedThanosStackRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageConfigId"); current != req.StorageConfigID {
		reasons = append(reasons, "object storage configuration change")
	}
	if current := stringFromMap(metadata, "objectStorageSecretName"); current != "" && current != req.ObjectStorageSecretName {
		reasons = append(reasons, "object storage secret change")
	}
	return len(reasons) > 0, reasons
}
