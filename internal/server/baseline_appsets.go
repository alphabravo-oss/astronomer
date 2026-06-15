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
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

const (
	platformSettingArgoCDManageBaseline  = "argocd.manage_platform_baseline"
	argoCDManagedByLabelKey              = "astronomer.io/managed-by"
	argoCDManagedByLabelValue            = "astronomer"
	argoCDClusterIDLabelKey              = "astronomer.io/cluster-id"
	argoCDClusterNameLabelKey            = "astronomer.io/cluster-name"
	argoCDEnvironmentLabelKey            = "astronomer.io/environment"
	argoCDIsLocalLabelKey                = "astronomer.io/is-local"
	argoCDRegionLabelKey                 = "astronomer.io/region"
	argoCDProviderLabelKey               = "astronomer.io/provider"
	argoCDDistributionLabelKey           = "astronomer.io/distribution"
	argoCDAgentProfileLabelKey           = "astronomer.io/agent-privilege-profile"
	baselineApplicationSetTargetLabel    = "astronomer.io/baseline-target"
	baselineApplicationSetSyncPhaseLabel = "astronomer.io/sync-phase"
	baselineTargetAdoptedClusters        = "adopted-clusters"
)

var argocdApplicationSetGVR = kubeutil.ArgoApplicationSetGVR

type baselineApplicationSetComponent struct {
	ApplicationSetName string
	ApplicationPrefix  string
	Slug               string
	ChartName          string
	RepoURL            string
	Namespace          string
	ValuesYAML         string
	SyncPhase          baselineSyncPhase
	// DefaultEnabled installs this component on every adopted cluster unless an
	// operator explicitly disables it. Only the two metrics exporters
	// (kube-state-metrics, prometheus-node-exporter) ship on by default — they
	// power the platform's metrics dashboards and conflict with nothing.
	// Everything else (trivy, fluent-bit, ingress-nginx, cert-manager,
	// gatekeeper) is OPT-IN via the per-component setting argocd.baseline.<slug>:
	// the kube-API agent already serves resources/logs/exec tool-free, so these
	// are value-adds the operator turns on from the cluster Tools view.
	DefaultEnabled bool
}

type baselineSyncPhase string

const (
	baselineSyncPhaseNamespaces  baselineSyncPhase = "namespaces"
	baselineSyncPhaseCRDs        baselineSyncPhase = "crds"
	baselineSyncPhaseOperators   baselineSyncPhase = "operators"
	baselineSyncPhasePolicies    baselineSyncPhase = "policies"
	baselineSyncPhaseWorkloads   baselineSyncPhase = "workloads"
	baselineSyncPhaseHealthCheck baselineSyncPhase = "health-checks"
)

const (
	baselineSyncWaveNamespaces  = -40
	baselineSyncWaveCRDs        = -30
	baselineSyncWaveOperators   = -20
	baselineSyncWavePolicies    = -10
	baselineSyncWaveWorkloads   = 10
	baselineSyncWaveHealthCheck = 30
)

var baselineSyncWaveByPhase = map[baselineSyncPhase]int{
	baselineSyncPhaseNamespaces:  baselineSyncWaveNamespaces,
	baselineSyncPhaseCRDs:        baselineSyncWaveCRDs,
	baselineSyncPhaseOperators:   baselineSyncWaveOperators,
	baselineSyncPhasePolicies:    baselineSyncWavePolicies,
	baselineSyncPhaseWorkloads:   baselineSyncWaveWorkloads,
	baselineSyncPhaseHealthCheck: baselineSyncWaveHealthCheck,
}

func baselineSyncPhaseOrDefault(phase baselineSyncPhase) baselineSyncPhase {
	if phase == "" {
		return baselineSyncPhaseWorkloads
	}
	if _, ok := baselineSyncWaveByPhase[phase]; ok {
		return phase
	}
	return baselineSyncPhaseWorkloads
}

func baselineSyncWaveForPhase(phase baselineSyncPhase) int {
	wave, ok := baselineSyncWaveByPhase[phase]
	if !ok {
		return baselineSyncWaveWorkloads
	}
	return wave
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
		SyncPhase:          baselineSyncPhaseHealthCheck,
	},
	{
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
		ApplicationPrefix:  "astronomer-ksm",
		Slug:               "kube-state-metrics",
		DefaultEnabled:     true,
		ChartName:          "kube-state-metrics",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		Namespace:          "monitoring",
		ValuesYAML:         "metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n",
		SyncPhase:          baselineSyncPhaseWorkloads,
	},
	{
		ApplicationSetName: "astronomer-baseline-node-exporter",
		ApplicationPrefix:  "astronomer-node-exporter",
		Slug:               "prometheus-node-exporter",
		DefaultEnabled:     true,
		ChartName:          "prometheus-node-exporter",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		Namespace:          "monitoring",
		ValuesYAML:         "hostRootFsMount:\n  enabled: true\n",
		SyncPhase:          baselineSyncPhaseWorkloads,
	},
	{
		ApplicationSetName: "astronomer-baseline-fluent-bit",
		ApplicationPrefix:  "astronomer-fluent-bit",
		Slug:               "fluent-bit",
		ChartName:          "fluent-bit",
		RepoURL:            "https://fluent.github.io/helm-charts",
		Namespace:          "logging",
		ValuesYAML:         "config:\n  service: |\n    [SERVICE]\n        Daemon Off\n        Flush 1\n",
		SyncPhase:          baselineSyncPhaseWorkloads,
	},
	{
		ApplicationSetName: "astronomer-baseline-ingress-nginx",
		ApplicationPrefix:  "astronomer-ingress-nginx",
		Slug:               "ingress-nginx",
		ChartName:          "ingress-nginx",
		RepoURL:            "https://kubernetes.github.io/ingress-nginx",
		Namespace:          "ingress-nginx",
		ValuesYAML:         "controller:\n  metrics:\n    enabled: true\n",
		SyncPhase:          baselineSyncPhaseOperators,
	},
	{
		ApplicationSetName: "astronomer-baseline-cert-manager",
		ApplicationPrefix:  "astronomer-cert-manager",
		Slug:               "cert-manager",
		ChartName:          "cert-manager",
		RepoURL:            "https://charts.jetstack.io",
		Namespace:          "cert-manager",
		ValuesYAML:         "installCRDs: true\nstartupapicheck:\n  enabled: false\n",
		SyncPhase:          baselineSyncPhaseCRDs,
	},
	{
		ApplicationSetName: "astronomer-baseline-gatekeeper",
		ApplicationPrefix:  "astronomer-gatekeeper",
		Slug:               "gatekeeper",
		ChartName:          "gatekeeper",
		RepoURL:            "https://open-policy-agent.github.io/gatekeeper/charts",
		Namespace:          "gatekeeper-system",
		SyncPhase:          baselineSyncPhasePolicies,
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

// platformSettingBaselineComponentPrefix + slug gates a single baseline
// component. Value is a JSON bool; absent falls back to component.DefaultEnabled.
// This is how operators opt INTO the opinionated infra (ingress-nginx,
// cert-manager, gatekeeper) or opt OUT of a default-on agent.
const platformSettingBaselineComponentPrefix = "argocd.baseline."

func baselineComponentEnabled(ctx context.Context, q platformSettingReader, c baselineApplicationSetComponent) bool {
	if q == nil {
		return c.DefaultEnabled
	}
	row, err := q.GetPlatformSetting(ctx, platformSettingBaselineComponentPrefix+c.Slug)
	if err != nil {
		return c.DefaultEnabled
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		return c.DefaultEnabled
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
	// q is the real *sqlc.Queries (implements platformSettingReader); the
	// tool-only fakes/nil fall back to DefaultEnabled gating.
	settings, _ := q.(platformSettingReader)
	for _, component := range baselineApplicationSetComponents(ctx, q) {
		if !baselineComponentEnabled(ctx, settings, component) {
			// Disabled/opt-in: remove the appset so its generated Apps prune.
			if err := res.Delete(ctx, component.ApplicationSetName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete applicationset %s: %w", component.ApplicationSetName, err)
			}
			continue
		}
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
	syncPhase := baselineSyncPhaseOrDefault(component.SyncPhase)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "ApplicationSet",
			"metadata": map[string]any{
				"name":      component.ApplicationSetName,
				"namespace": localArgoNamespace,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by":       "astronomer",
					"astronomer.io/platform-owned":       "true",
					"astronomer.io/baseline":             "platform",
					"astronomer.io/tool-slug":            component.Slug,
					baselineApplicationSetTargetLabel:    baselineTargetAdoptedClusters,
					baselineApplicationSetSyncPhaseLabel: string(syncPhase),
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
						"annotations": map[string]any{
							"argocd.argoproj.io/sync-wave": fmt.Sprintf("%d", baselineSyncWaveForPhase(syncPhase)),
						},
						"labels": map[string]any{
							"app.kubernetes.io/managed-by":       "astronomer",
							"astronomer.io/platform-owned":       "true",
							"astronomer.io/baseline":             "platform",
							"astronomer.io/tool-slug":            component.Slug,
							baselineApplicationSetTargetLabel:    baselineTargetAdoptedClusters,
							baselineApplicationSetSyncPhaseLabel: string(syncPhase),
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
							// ServerSideApply has the apiserver perform the merge — the
							// recommended mode for proxied/aggregated clusters. (Note: it
							// does NOT avoid ArgoCD's anonymous openapi/discovery/apply
							// requests through the tunnel; that's handled by the
							// network-isolated internal proxy listener, not by a sync
							// option — see NewInternalArgoCDProxyRouter.)
							"syncOptions": []any{"CreateNamespace=true", "ServerSideApply=true"},
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
