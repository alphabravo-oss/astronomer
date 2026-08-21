package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/deploy/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// grafanaFolderReconciler is the cluster-handler callback for folder-per-cluster
// Grafana provisioning. MonitoringHandler implements it. Folders are UX; the
// PromQL/LogQL rewrite is the tenant security boundary.
type grafanaFolderReconciler interface {
	TriggerGrafanaFolderReconcile()
}

func (h *MonitoringHandler) TriggerGrafanaFolderReconcile() {
	if h == nil || h.folderTriggerCh == nil {
		return
	}
	select {
	case h.folderTriggerCh <- struct{}{}:
	default:
	}
}

func (h *MonitoringHandler) runGrafanaFolderReconciler(ctx context.Context) {
	if h.folderTriggerCh == nil {
		h.folderTriggerCh = make(chan struct{}, 1)
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	_ = h.ReconcileGrafanaClusterFolders(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.ReconcileGrafanaClusterFolders(ctx); err != nil && h.log != nil {
				h.log.Warn("grafana cluster folder reconcile failed", "error", err)
			}
		case <-h.folderTriggerCh:
			if err := h.ReconcileGrafanaClusterFolders(ctx); err != nil && h.log != nil {
				h.log.Warn("grafana cluster folder reconcile failed", "error", err)
			}
		}
	}
}

// ReconcileGrafanaClusterFolders provisions one Grafana folder per adopted
// cluster (title = displayName, uid = cluster UUID) and copies cluster-scoped
// dashboards into it. It is not a security boundary.
func (h *MonitoringHandler) ReconcileGrafanaClusterFolders(ctx context.Context) error {
	if h == nil || h.queries == nil || h.requester == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return nil
	}
	meta := sharedStackMetadata(backend, "sharedGrafana")
	req := grafanaRequestFromMetadata(meta)
	mgmtID := defaultString(req.ManagementClusterID, stringFromMap(meta, "managementClusterId"))
	ns := defaultString(req.Namespace, "monitoring")
	if mgmtID == "" {
		return nil
	}
	if !grafanaStackPresent(stringFromMap(meta, "status")) {
		return h.deleteGrafanaClusterFolderConfigMaps(ctx, mgmtID, ns)
	}

	clusters, err := h.sizerListAllClusters(ctx)
	if err != nil {
		return fmt.Errorf("list clusters for grafana folders: %w", err)
	}
	wanted := make(map[string]sqlc.Cluster, len(clusters))
	for _, c := range clusters {
		id := c.ID.String()
		if id == "" {
			continue
		}
		wanted[id] = c
		cm, err := grafanaClusterDashboardConfigMap(c)
		if err != nil {
			return err
		}
		if err := h.ensureGrafanaConfigMap(ctx, mgmtID, ns, cm); err != nil {
			return fmt.Errorf("apply cluster folder dashboards for %s: %w", id, err)
		}
	}

	providers := grafanaClusterFolderProvidersConfigMap(ns, clusters)
	if err := h.ensureGrafanaConfigMap(ctx, mgmtID, ns, providers); err != nil {
		return fmt.Errorf("apply cluster folder providers: %w", err)
	}

	existing, err := h.listGrafanaClusterFolderConfigMaps(ctx, mgmtID, ns)
	if err != nil {
		return err
	}
	for _, name := range existing {
		id := grafanaClusterIDFromFolderConfigMapName(name)
		if id == "" {
			continue
		}
		if _, ok := wanted[id]; ok {
			continue
		}
		if err := deleteConfigMap(ctx, h.requester, mgmtID, ns, name); err != nil {
			return fmt.Errorf("delete stale grafana cluster folder %s: %w", name, err)
		}
	}
	return nil
}

func (h *MonitoringHandler) deleteGrafanaClusterFolderConfigMaps(ctx context.Context, clusterID, namespace string) error {
	existing, err := h.listGrafanaClusterFolderConfigMaps(ctx, clusterID, namespace)
	if err != nil {
		return err
	}
	for _, name := range existing {
		if err := deleteConfigMap(ctx, h.requester, clusterID, namespace, name); err != nil {
			return err
		}
	}
	return deleteConfigMap(ctx, h.requester, clusterID, namespace, grafanaClusterFolderProvidersCM)
}

func (h *MonitoringHandler) listGrafanaClusterFolderConfigMaps(ctx context.Context, clusterID, namespace string) ([]string, error) {
	if h.requester == nil {
		return nil, nil
	}
	selector := url.QueryEscape(grafanaClusterFolderLabelKey + "=" + grafanaClusterFolderLabelVal)
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps?labelSelector=%s", namespace, selector)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Name != "" {
			names = append(names, item.Metadata.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func grafanaClusterFolderTitle(c sqlc.Cluster) string {
	if title := strings.TrimSpace(c.DisplayName); title != "" {
		return title
	}
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	return c.ID.String()
}

func grafanaClusterDashboardPath(clusterID string) string {
	return grafanaClusterDashboardRoot + "/" + strings.ToLower(clusterID)
}

func grafanaClusterFolderConfigMapName(clusterID string) string {
	return "astronomer-gf-c-" + strings.ToLower(clusterID)
}

func grafanaClusterIDFromFolderConfigMapName(name string) string {
	const prefix = "astronomer-gf-c-"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	return strings.TrimPrefix(name, prefix)
}

func grafanaClusterScopedSlugs() []string {
	entries, err := fs.ReadDir(dashboards.FS, ".")
	if err != nil {
		return nil
	}
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".json")
		if grafanaDashboardFolder(slug) != grafanaFolderFleet {
			continue
		}
		raw, err := dashboards.FS.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		if !grafanaDashboardIsClusterScoped(raw) {
			continue
		}
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

func grafanaDashboardIsClusterScoped(raw []byte) bool {
	if strings.Contains(string(raw), "$cluster") {
		return true
	}
	var dash map[string]any
	if err := json.Unmarshal(raw, &dash); err != nil {
		return false
	}
	templating, _ := dash["templating"].(map[string]any)
	list, _ := templating["list"].([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m["name"] == "cluster" {
			return true
		}
	}
	return false
}

func grafanaClusterDashboardUID(slug, clusterID string) string {
	prefix := grafanaClusterDashboardUIDPrefix(slug)
	uid := prefix + "-" + clusterID
	if len(uid) <= 40 {
		return uid
	}
	compact := prefix + "-" + strings.ReplaceAll(clusterID, "-", "")
	if len(compact) > 40 {
		return compact[:40]
	}
	return compact
}

func grafanaClusterDashboardUIDPrefix(slug string) string {
	switch slug {
	case "cluster-overview":
		return "co"
	case "node-usage":
		return "nu"
	case "workload-health":
		return "wh"
	case "image-scan-summary":
		return "is"
	}
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, strings.ToLower(slug))
	if len(cleaned) >= 2 {
		return cleaned[:2]
	}
	if cleaned != "" {
		return cleaned
	}
	return "cl"
}

func grafanaClusterDashboardConfigMap(c sqlc.Cluster) (map[string]any, error) {
	id := c.ID.String()
	title := grafanaClusterFolderTitle(c)
	data := map[string]any{}
	for _, slug := range grafanaClusterScopedSlugs() {
		raw, err := dashboards.FS.ReadFile(slug + ".json")
		if err != nil {
			continue
		}
		pinned, err := pinClusterDashboard(raw, id, title, slug)
		if err != nil {
			return nil, fmt.Errorf("pin %s for cluster %s: %w", slug, id, err)
		}
		data[slug+".json"] = string(pinned)
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name": grafanaClusterFolderConfigMapName(id),
			"labels": map[string]any{
				"grafana_dashboard":          "1",
				grafanaClusterFolderLabelKey: grafanaClusterFolderLabelVal,
				grafanaClusterIDLabelKey:     id,
			},
			"annotations": map[string]any{
				grafanaDashboardFolderAnnotationKey: grafanaClusterDashboardPath(id),
			},
		},
		"data": data,
	}, nil
}

func pinClusterDashboard(raw []byte, clusterID, displayName, slug string) ([]byte, error) {
	var dash map[string]any
	if err := json.Unmarshal(raw, &dash); err != nil {
		return nil, err
	}
	dash["uid"] = grafanaClusterDashboardUID(slug, clusterID)
	if title, _ := dash["title"].(string); strings.TrimSpace(title) != "" {
		dash["title"] = strings.TrimSpace(title) + " — " + displayName
	}
	templating, _ := dash["templating"].(map[string]any)
	if templating == nil {
		templating = map[string]any{}
	}
	list, _ := templating["list"].([]any)
	found := false
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok || m["name"] != "cluster" {
			continue
		}
		m["current"] = map[string]any{"text": clusterID, "value": clusterID, "selected": true}
		m["hide"] = 2
		list[i] = m
		found = true
	}
	if !found {
		list = append(list, map[string]any{
			"name":    "cluster",
			"type":    "constant",
			"query":   clusterID,
			"current": map[string]any{"text": clusterID, "value": clusterID, "selected": true},
			"hide":    2,
		})
	}
	templating["list"] = list
	dash["templating"] = templating
	return json.Marshal(dash)
}

func grafanaClusterFolderProvidersConfigMap(namespace string, clusters []sqlc.Cluster) map[string]any {
	sorted := append([]sqlc.Cluster(nil), clusters...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID.String() < sorted[j].ID.String()
	})
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      grafanaClusterFolderProvidersCM,
			"namespace": namespace,
			"labels": map[string]any{
				grafanaClusterFolderLabelKey: "providers",
			},
		},
		"data": map[string]any{
			grafanaClusterFolderProvidersFile: grafanaClusterFolderProvidersYAML(sorted),
		},
	}
}

func grafanaClusterFolderProvidersYAML(clusters []sqlc.Cluster) string {
	var b strings.Builder
	b.WriteString("apiVersion: 1\nproviders:\n")
	if len(clusters) == 0 {
		b.WriteString("  []\n")
		return b.String()
	}
	for _, c := range clusters {
		id := c.ID.String()
		b.WriteString("  - name: cluster-")
		b.WriteString(id)
		b.WriteString("\n    orgId: 1\n    folder: ")
		b.WriteString(yamlQuotedString(grafanaClusterFolderTitle(c)))
		b.WriteString("\n    folderUid: ")
		b.WriteString(yamlQuotedString(id))
		b.WriteString("\n    type: file\n    disableDeletion: false\n    updateIntervalSeconds: 30\n    options:\n      path: ")
		b.WriteString(yamlQuotedString(grafanaClusterDashboardPath(id)))
		b.WriteString("\n      foldersFromFilesStructure: false\n")
	}
	return b.String()
}
