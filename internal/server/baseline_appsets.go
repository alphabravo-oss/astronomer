package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	platformSettingArgoCDManageBaseline = "argocd.manage_platform_baseline"
	argoCDManagedByLabelKey             = "astronomer.io/managed-by"
	argoCDManagedByLabelValue           = "astronomer"
	argoCDIsLocalLabelKey               = "astronomer.io/is-local"
)

var argocdApplicationSetGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applicationsets",
}

type baselineApplicationSetComponent struct {
	ApplicationSetName string
	ApplicationPrefix  string
	Slug               string
	ChartName          string
	RepoURL            string
	Namespace          string
	ValuesYAML         string
}

type baselineChartCoordinates struct {
	ChartName string `json:"chart_name"`
	RepoURL   string `json:"repo_url"`
	Namespace string `json:"namespace"`
	Order     int    `json:"order"`
}

var fallbackBaselineApplicationSetComponents = []baselineApplicationSetComponent{
	{
		ApplicationSetName: "astronomer-baseline-trivy",
		ApplicationPrefix:  "astronomer-trivy",
		Slug:               "trivy-operator",
		ChartName:          "trivy-operator",
		RepoURL:            "https://aquasecurity.github.io/helm-charts",
		Namespace:          "trivy-system",
		ValuesYAML:         "trivy:\n  ignoreUnfixed: true\noperator:\n  scanJobTimeout: 5m\n",
	},
	{
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
		ApplicationPrefix:  "astronomer-ksm",
		Slug:               "kube-state-metrics",
		ChartName:          "kube-state-metrics",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		Namespace:          "monitoring",
		ValuesYAML:         "metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n",
	},
	{
		ApplicationSetName: "astronomer-baseline-node-exporter",
		ApplicationPrefix:  "astronomer-node-exporter",
		Slug:               "prometheus-node-exporter",
		ChartName:          "prometheus-node-exporter",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		Namespace:          "monitoring",
		ValuesYAML:         "hostRootFsMount:\n  enabled: true\n",
	},
	{
		ApplicationSetName: "astronomer-baseline-fluent-bit",
		ApplicationPrefix:  "astronomer-fluent-bit",
		Slug:               "fluent-bit",
		ChartName:          "fluent-bit",
		RepoURL:            "https://fluent.github.io/helm-charts",
		Namespace:          "logging",
		ValuesYAML:         "config:\n  service: |\n    [SERVICE]\n        Daemon Off\n        Flush 1\n",
	},
	{
		ApplicationSetName: "astronomer-baseline-cert-manager",
		ApplicationPrefix:  "astronomer-cert-manager",
		Slug:               "cert-manager",
		ChartName:          "cert-manager",
		RepoURL:            "https://charts.jetstack.io",
		Namespace:          "cert-manager",
		ValuesYAML:         "installCRDs: true\nstartupapicheck:\n  enabled: false\n",
	},
}

type baselineToolQuerier interface {
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
}

type platformSettingReader interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

func argoCDManagePlatformBaselineEnabled(ctx context.Context, q platformSettingReader) bool {
	if q == nil {
		return true
	}
	row, err := q.GetPlatformSetting(ctx, platformSettingArgoCDManageBaseline)
	if err != nil {
		return true
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		return true
	}
	return enabled
}

func baselineApplicationSetComponents(ctx context.Context, q baselineToolQuerier) []baselineApplicationSetComponent {
	components := make([]baselineApplicationSetComponent, 0, len(fallbackBaselineApplicationSetComponents))
	for _, fallback := range fallbackBaselineApplicationSetComponents {
		components = append(components, baselineComponentFromTool(ctx, q, fallback))
	}
	return components
}

func baselineComponentFromTool(ctx context.Context, q baselineToolQuerier, fallback baselineApplicationSetComponent) baselineApplicationSetComponent {
	out := fallback
	if q == nil {
		return out
	}
	tool, err := q.GetToolBySlug(ctx, fallback.Slug)
	if err != nil {
		if err == pgx.ErrNoRows {
			return out
		}
		return out
	}
	if chart, ok := firstToolChart(tool.Charts); ok {
		out.ChartName = firstNonEmptyServerString(chart.ChartName, out.ChartName)
		out.RepoURL = firstNonEmptyServerString(chart.RepoURL, out.RepoURL)
		out.Namespace = firstNonEmptyServerString(chart.Namespace, out.Namespace)
	}
	if values := defaultPresetValues(tool.Presets); values != "" {
		out.ValuesYAML = values
	}
	if out.Namespace == "" {
		out.Namespace = firstNonEmptyServerString(tool.DefaultNamespace, fallback.Namespace)
	}
	return out
}

func firstToolChart(raw json.RawMessage) (baselineChartCoordinates, bool) {
	var charts []baselineChartCoordinates
	if err := json.Unmarshal(raw, &charts); err != nil || len(charts) == 0 {
		return baselineChartCoordinates{}, false
	}
	best := charts[0]
	for _, chart := range charts[1:] {
		if chart.Order < best.Order {
			best = chart
		}
	}
	return best, true
}

func defaultPresetValues(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var presets map[string]string
	if err := json.Unmarshal(raw, &presets); err == nil {
		return strings.TrimSpace(presets["default"])
	}
	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		return ""
	}
	if v, ok := loose["default"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func ensureBaselineApplicationSets(ctx context.Context, dyn dynamic.Interface, q baselineToolQuerier) error {
	if dyn == nil {
		return fmt.Errorf("dynamic client not configured")
	}
	res := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace)
	for _, component := range baselineApplicationSetComponents(ctx, q) {
		obj := baselineApplicationSetObject(component)
		current, err := res.Get(ctx, component.ApplicationSetName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err := res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("create applicationset %s: %w", component.ApplicationSetName, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("get applicationset %s: %w", component.ApplicationSetName, err)
		}
		obj.SetResourceVersion(current.GetResourceVersion())
		if _, err := res.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update applicationset %s: %w", component.ApplicationSetName, err)
		}
	}
	return nil
}

func baselineApplicationSetObject(component baselineApplicationSetComponent) *unstructured.Unstructured {
	values := strings.TrimSpace(component.ValuesYAML)
	helm := map[string]any{
		"releaseName": component.Slug,
	}
	if values != "" {
		helm["values"] = values + "\n"
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "ApplicationSet",
			"metadata": map[string]any{
				"name":      component.ApplicationSetName,
				"namespace": localArgoNamespace,
				"labels": map[string]any{
					"astronomer.io/platform-owned": "true",
					"astronomer.io/baseline":       "platform",
				},
			},
			"spec": map[string]any{
				"generators": []any{
					map[string]any{
						"clusters": map[string]any{
							"selector": map[string]any{
								"matchLabels": map[string]any{
									argoCDManagedByLabelKey: argoCDManagedByLabelValue,
									argoCDIsLocalLabelKey:   "false",
								},
							},
						},
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"name": component.ApplicationPrefix + "-{{nameNormalized}}",
						"labels": map[string]any{
							"astronomer.io/platform-owned": "true",
							"astronomer.io/baseline":       "platform",
							"astronomer.io/tool-slug":      component.Slug,
						},
					},
					"spec": map[string]any{
						"project": "default",
						"source": map[string]any{
							"repoURL":        component.RepoURL,
							"chart":          component.ChartName,
							"targetRevision": "*",
							"helm":           helm,
						},
						"destination": map[string]any{
							"server":    "{{server}}",
							"namespace": component.Namespace,
						},
						"syncPolicy": map[string]any{
							"automated": map[string]any{
								"prune":    true,
								"selfHeal": true,
							},
							"syncOptions": []any{"CreateNamespace=true"},
						},
					},
				},
			},
		},
	}
}

func firstNonEmptyServerString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
