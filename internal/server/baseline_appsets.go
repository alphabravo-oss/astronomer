package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/internal/baseline"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
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
	// baselineOwnershipDecisionLeaveLocal is the per-(cluster, component)
	// ownership decision that opts a cluster OUT of the server-pushed baseline:
	// the operator has chosen to keep that component locally managed, so the
	// push generator must not fan a baseline App onto it. adopt/replace (and the
	// absent-row default) keep the component under Argo push.
	baselineOwnershipDecisionLeaveLocal = "leave_local"
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

// fallbackBaselineApplicationSetComponents is the platform baseline that
// Astronomer auto-manages on every adopted cluster: ONLY the two metrics
// exporters that power the built-in dashboards and conflict with nothing.
// Everything else a cluster might want (trivy, fluent-bit, ingress-nginx,
// cert-manager, gatekeeper, …) is installed on demand from the per-cluster
// Tools view — that is the single, per-cluster ownership path for them, so we
// deliberately do NOT manage them as global baseline ApplicationSets here.
//
// It is derived from the single baseline.Registry seam (the dispatcher that
// routes each component to its lifecycle path) so the DefaultEnabled set no
// longer has to be hand-kept in sync with handler.platformBaselineComponentCatalog.
var fallbackBaselineApplicationSetComponents = baselineApplicationSetComponentsFromRegistry()

func baselineApplicationSetComponentsFromRegistry() []baselineApplicationSetComponent {
	registry := baseline.ApplicationSetComponents()
	out := make([]baselineApplicationSetComponent, 0, len(registry))
	for _, c := range registry {
		out = append(out, baselineApplicationSetComponent{
			ApplicationSetName: c.ApplicationSetName,
			ApplicationPrefix:  c.ApplicationPrefix,
			Slug:               c.Slug,
			DefaultEnabled:     c.DefaultEnabled,
			ChartName:          c.ChartName,
			RepoURL:            c.RepoURL,
			Namespace:          c.Namespace,
			ValuesYAML:         c.ValuesYAML,
			SyncPhase:          baselineSyncPhaseWorkloads,
		})
	}
	return out
}

type baselineToolQuerier interface {
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	// ListArgoCDBaselineOwnershipDecisionsByDecision lets the generator fetch all
	// "leave_local" rows across clusters so those clusters can be excluded from
	// each component's fan-out (H7 — make the ownership decision behavioral).
	ListArgoCDBaselineOwnershipDecisionsByDecision(ctx context.Context, decision string) ([]sqlc.ArgocdBaselineOwnershipDecision, error)
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
		out.ChartName = strutil.FirstNonBlankTrimmed(chart.ChartName, out.ChartName)
		out.RepoURL = strutil.FirstNonBlankTrimmed(chart.RepoURL, out.RepoURL)
		out.Namespace = strutil.FirstNonBlankTrimmed(chart.Namespace, out.Namespace)
	}
	if values := defaultPresetValues(tool.Presets); values != "" {
		out.ValuesYAML = values
	}
	if out.Namespace == "" {
		out.Namespace = strutil.FirstNonBlankTrimmed(tool.DefaultNamespace, fallback.Namespace)
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
	// Per-component map of cluster-id strings to EXCLUDE from the fan-out because
	// the operator recorded a "leave_local" ownership decision for that
	// (cluster, component) — the cluster keeps the component locally managed
	// (H7). Absent rows / adopt / replace are never excluded, preserving the
	// default apply behavior.
	excludeByComponentSlug := leaveLocalExclusionsByComponent(ctx, q)
	for _, component := range baselineApplicationSetComponents(ctx, q) {
		if !baselineComponentEnabled(ctx, settings, component) {
			// Disabled/opt-in: remove the appset so its generated Apps prune.
			if err := res.Delete(ctx, component.ApplicationSetName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete applicationset %s: %w", component.ApplicationSetName, err)
			}
			continue
		}
		obj := baselineApplicationSetObject(component, excludeByComponentSlug[component.Slug])
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

// leaveLocalExclusionsByComponent fetches every "leave_local" ownership row in
// one query and groups the excluded cluster-id strings by component slug. A
// query error (or nil querier) fails OPEN to an empty map — the generator then
// behaves exactly as before the ownership wiring (apply to all managed remote
// clusters), never worse than today's behavior.
func leaveLocalExclusionsByComponent(ctx context.Context, q baselineToolQuerier) map[string][]string {
	out := map[string][]string{}
	if q == nil {
		return out
	}
	decisions, err := q.ListArgoCDBaselineOwnershipDecisionsByDecision(ctx, baselineOwnershipDecisionLeaveLocal)
	if err != nil {
		return out
	}
	// Ownership decisions can carry an expires_at (a temporary "keep this
	// component locally managed for N days" cutover). The DB list query is
	// not time-filtered, so an expired row would otherwise exclude the
	// cluster from the baseline fan-out FOREVER — ArgoCD would never adopt
	// the component even though the operator intended the exclusion to
	// lapse. Drop rows whose expires_at is in the past so the exclusion
	// ends exactly when the operator set it to.
	now := time.Now()
	for _, d := range decisions {
		if d.ExpiresAt.Valid && !d.ExpiresAt.Time.After(now) {
			continue
		}
		out[d.ComponentSlug] = append(out[d.ComponentSlug], d.ClusterID.String())
	}
	return out
}

func baselineApplicationSetObject(component baselineApplicationSetComponent, excludeClusterIDs []string) *unstructured.Unstructured {
	values := strings.TrimSpace(component.ValuesYAML)
	helm := map[string]any{
		"releaseName": component.Slug,
	}
	if values != "" {
		helm["values"] = values + "\n"
	}
	syncPhase := baselineSyncPhaseOrDefault(component.SyncPhase)
	// Base selector: every managed, non-local cluster Secret. Only when the
	// operator recorded "leave_local" decisions for this component do we append a
	// cluster-id NotIn matchExpression that filters those clusters OUT of the
	// fan-out (H7). The cluster-id label is stamped on every cluster Secret by
	// argolabels.ManagedClusterLabels (ClusterIDLabelKey). With no exclusions the
	// selector is byte-for-byte identical to the pre-fix output (admin-push path
	// unbroken).
	// M9 profile pre-flight: the agent proxies ArgoCD's baseline apply with its
	// OWN SA token, so only the full-cluster profiles (operator/admin) can do the
	// cluster cache-sync + CreateNamespace + SSA. viewer / namespace-* profiles
	// 403 on every apply. Filter them OUT of the generator (In [operator,admin])
	// so they never get a baseline App that would just sit failing — instead of
	// opaque Argo 403s. The profile label is always stamped on the cluster Secret
	// by argolabels.ManagedClusterLabels.
	matchExpressions := []any{
		map[string]any{
			"key":      argoCDAgentProfileLabelKey,
			"operator": "In",
			"values":   []any{"operator", "admin"},
		},
	}
	// H7 leave_local: append a cluster-id NotIn only when the operator recorded
	// "leave_local" decisions for this component, filtering those clusters out.
	if len(excludeClusterIDs) > 0 {
		excluded := make([]any, 0, len(excludeClusterIDs))
		for _, id := range excludeClusterIDs {
			excluded = append(excluded, id)
		}
		matchExpressions = append(matchExpressions, map[string]any{
			"key":      argoCDClusterIDLabelKey,
			"operator": "NotIn",
			"values":   excluded,
		})
	}
	selector := map[string]any{
		"matchLabels": map[string]any{
			argoCDManagedByLabelKey: argoCDManagedByLabelValue,
			argoCDIsLocalLabelKey:   "false",
		},
		"matchExpressions": matchExpressions,
	}
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
							"selector": selector,
						},
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"name": component.ApplicationPrefix + "-{{nameNormalized}}",
						// L10: the resources-finalizer makes Application deletion CASCADE
						// to the actual workloads. Without it, disabling a baseline
						// component (which deletes this ApplicationSet -> deletes its
						// generated Apps) or excluding a cluster (leave_local / E1) would
						// ORPHAN the deployed resources instead of pruning them. With the
						// finalizer ArgoCD prunes the downstream resources before the App
						// is removed, so "disable" / "hand off to local" actually removes
						// the footprint.
						"finalizers": []any{"resources-finalizer.argocd.argoproj.io"},
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

// removeBaselineApplicationSets deletes every baseline component's
// ApplicationSet from the local ArgoCD namespace, ignoring NotFound. It is the
// stand-down teardown reconcileLocalArgoSelfManagement runs when
// PullReconcileEnabled is true: the agent's PULL loop owns the astronomer-*
// footprint, so any previously-pushed baseline appset must be pruned to avoid
// double-management (H6). On a green-field pull deploy no appset was ever
// created, so this is a no-op.
func removeBaselineApplicationSets(ctx context.Context, dyn dynamic.Interface) error {
	if dyn == nil {
		return fmt.Errorf("dynamic client not configured")
	}
	res := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace)
	for _, component := range fallbackBaselineApplicationSetComponents {
		if err := res.Delete(ctx, component.ApplicationSetName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete applicationset %s: %w", component.ApplicationSetName, err)
		}
	}
	return nil
}
