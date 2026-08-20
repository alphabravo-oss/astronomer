package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	fluentBitToolSlug         = "fluent-bit"
	fluentBitChartName        = "fluent-bit"
	fluentBitChartRepo        = "https://fluent.github.io/helm-charts"
	fluentBitIngestSecretName = "astronomer-loki-ingest-token"
	fluentBitIngestSecretKey  = "token"
	fluentBitIngestVolumeName = "astronomer-loki-ingest"
	fluentBitIngestMountPath  = "/var/run/astronomer/loki-ingest"
	fluentBitIngestTokenFile  = fluentBitIngestMountPath + "/" + fluentBitIngestSecretKey
)

// fluentBitReleaseStore looks up the baseline fluent-bit install so ingest
// token mounts can be merged into stored Helm values without wiping operator
// / distribution overrides.
type fluentBitReleaseStore interface {
	GetInstalledChartByClusterAndTool(ctx context.Context, arg sqlc.GetInstalledChartByClusterAndToolParams) (sqlc.InstalledChart, error)
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	UpdateInstalledChartValues(ctx context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
}

func (h *LoggingHandler) syncMemberLokiIngestSecret(ctx context.Context, env loggingOperationEnvelope) error {
	if !env.IsSystem || !strings.EqualFold(env.OutputType, "loki") {
		return nil
	}
	if env.Enabled && env.BearerToken != "" {
		if err := applyAlertSecret(ctx, h.requester, env.ClusterID, LoggingNamespace, fluentBitIngestSecretName, map[string]string{
			fluentBitIngestSecretKey: env.BearerToken,
		}); err != nil {
			return fmt.Errorf("apply ingest token secret: %w", err)
		}
		return h.reconcileFluentBitIngestMounts(ctx, env.ClusterID)
	}
	return deleteSecret(ctx, h.requester, env.ClusterID, LoggingNamespace, fluentBitIngestSecretName)
}

func (h *LoggingHandler) reconcileFluentBitIngestMounts(ctx context.Context, clusterID string) error {
	if h == nil || h.helm == nil {
		return nil
	}
	cu, err := uuid.Parse(clusterID)
	if err != nil {
		return fmt.Errorf("parse cluster id: %w", err)
	}
	store, ok := h.queries.(fluentBitReleaseStore)
	if !ok {
		return nil
	}
	item, err := store.GetInstalledChartByClusterAndTool(ctx, sqlc.GetInstalledChartByClusterAndToolParams{
		ClusterID: cu,
		ToolSlug:  fluentBitToolSlug,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if h.log != nil {
				h.log.Warn("logging: fluent-bit baseline release not found; ingest Secret applied without extraVolumeMounts", "cluster_id", clusterID)
			}
			return nil
		}
		return fmt.Errorf("lookup fluent-bit release: %w", err)
	}

	values := map[string]any{}
	if strings.TrimSpace(item.ValuesOverride) != "" {
		if err := yaml.Unmarshal([]byte(item.ValuesOverride), &values); err != nil {
			return fmt.Errorf("parse fluent-bit values: %w", err)
		}
	}
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["existingConfigMap"]; !ok {
		values["existingConfigMap"] = FluentBitConfigMapName
	}
	changed := ensureFluentBitIngestMountValues(values)
	if !changed {
		return nil
	}

	chartName, repoURL, version := fluentBitChartSpec(ctx, store)
	ns := item.Namespace
	if ns == "" {
		ns = LoggingNamespace
	}
	release := item.ReleaseName
	if release == "" {
		release = fluentBitToolSlug
	}
	result, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUpgrade, protocol.HelmRequestPayload{
		ReleaseName: release,
		Namespace:   ns,
		ChartName:   chartName,
		RepoURL:     repoURL,
		Version:     version,
		Values:      values,
	})
	if err != nil {
		return fmt.Errorf("upgrade fluent-bit extraVolumeMounts: %w", err)
	}

	raw, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal fluent-bit values: %w", err)
	}
	status := item.Status
	revision := item.Revision
	if result != nil {
		if s := normalizeToolStatus(result.Status); s != "" {
			status = s
		}
		if result.Revision > 0 {
			revision = int32(result.Revision)
		}
	}
	if _, err := store.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
		ID:             item.ID,
		ValuesOverride: string(raw),
		Status:         status,
		Revision:       revision,
	}); err != nil {
		return fmt.Errorf("persist fluent-bit values: %w", err)
	}
	return nil
}

func fluentBitChartSpec(ctx context.Context, store fluentBitReleaseStore) (chartName, repoURL, version string) {
	chartName, repoURL, version = fluentBitChartName, fluentBitChartRepo, ""
	if store == nil {
		return chartName, repoURL, version
	}
	tool, err := store.GetToolBySlug(ctx, fluentBitToolSlug)
	if err != nil {
		return chartName, repoURL, version
	}
	version = tool.VersionConstraint
	charts, err := parseToolCharts(tool.Charts)
	if err != nil || len(charts) == 0 {
		return chartName, repoURL, version
	}
	chart := firstChart(charts)
	if chart.ChartName != "" {
		chartName = chart.ChartName
	}
	if chart.RepoURL != "" {
		repoURL = chart.RepoURL
	}
	return chartName, repoURL, version
}

func ensureFluentBitIngestMountValues(values map[string]any) bool {
	if values == nil {
		return false
	}
	changed := false
	volumes := asAnySlice(values["extraVolumes"])
	if volumes, ok := upsertMapByName(volumes, fluentBitIngestVolume()); ok {
		values["extraVolumes"] = volumes
		changed = true
	} else {
		values["extraVolumes"] = volumes
	}
	mounts := asAnySlice(values["extraVolumeMounts"])
	if mounts, ok := upsertMapByName(mounts, fluentBitIngestVolumeMount()); ok {
		values["extraVolumeMounts"] = mounts
		changed = true
	} else {
		values["extraVolumeMounts"] = mounts
	}
	hot, _ := values["hotReload"].(map[string]any)
	if hot == nil {
		hot = map[string]any{"enabled": true}
		values["hotReload"] = hot
		changed = true
	}
	watch := asAnySlice(hot["extraWatchVolumes"])
	if watch, ok := upsertString(watch, fluentBitIngestVolumeName); ok {
		hot["extraWatchVolumes"] = watch
		values["hotReload"] = hot
		changed = true
	} else {
		hot["extraWatchVolumes"] = watch
		values["hotReload"] = hot
	}
	return changed
}

func asAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	default:
		return nil
	}
}

func fluentBitIngestVolume() map[string]any {
	return map[string]any{
		"name": fluentBitIngestVolumeName,
		"secret": map[string]any{
			"secretName": fluentBitIngestSecretName,
			"optional":   true,
		},
	}
}

func fluentBitIngestVolumeMount() map[string]any {
	return map[string]any{
		"name":      fluentBitIngestVolumeName,
		"mountPath": fluentBitIngestMountPath,
		"readOnly":  true,
	}
}

func upsertMapByName(list []any, want map[string]any) ([]any, bool) {
	name, _ := want["name"].(string)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprint(m["name"]) == name {
			return list, false
		}
	}
	return append(list, want), true
}

func upsertString(list []any, want string) ([]any, bool) {
	for _, item := range list {
		if fmt.Sprint(item) == want {
			return list, false
		}
	}
	return append(list, want), true
}
