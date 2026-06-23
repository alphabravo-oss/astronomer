// Package crd implements the CRD-mirror management API for astronomer-go.
//
// Management CustomResourceDefinitions —
//   - Cluster.management.astronomer.io/v1alpha1
//   - Project.management.astronomer.io/v1alpha1
//   - ClusterBaseline.management.astronomer.io/v1alpha1
//   - ComponentBundle.management.astronomer.io/v1alpha1
//   - AgentProfile.management.astronomer.io/v1alpha1
//   - GitOpsTarget.management.astronomer.io/v1alpha1
//
// — expose Kubernetes-native desired state for adopted-cluster management.
// Cluster and Project are reconciled against the existing DB tables today.
// The remaining CRDs run validation/status reconcilers today and are shaped as
// durable API surfaces for baseline, bundle, agent-profile, and GitOps-target
// intent.
//
// The package intentionally does not import internal/handler — instead the
// controller depends on narrow interfaces (ClusterSync / ProjectSync) that the
// server-side wiring satisfies with a thin adapter. This keeps the CRD package
// trivially mockable and avoids dragging the heavy handler import graph (chi,
// otelpgx, etc.) into anything that just wants to wire the manager.
package crd

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion identifies the API group + version for the management CRDs.
// This stays the storage version: it is what the controller stamps onto the
// apiVersion of objects it creates / annotates and what the DB-mirror logic
// treats as canonical.
var GroupVersion = schema.GroupVersion{Group: "management.astronomer.io", Version: "v1alpha1"}

// GroupVersionV1Beta1 is the promoted, additionally-served version of the
// management CRD group. The Go types are shared verbatim across v1alpha1 and
// v1beta1, so conversion between the two is the identity transform — no field
// renames or shape changes. Serving both versions lets operators start writing
// manifests at the more stable apiVersion while existing v1alpha1 manifests
// keep working unchanged. See docs/crd-versioning.md.
var GroupVersionV1Beta1 = schema.GroupVersion{Group: "management.astronomer.io", Version: "v1beta1"}

// TrivyGroupVersion identifies the upstream Trivy-operator CRD group.
// Sprint 062: the CRD-mirror watcher subscribes to VulnerabilityReports
// on this group/version and routes them into internal/scanner.Ingest.
//
// Kept as a top-level var (not a const) so callers can inspect the
// Group / Version fields directly, the same way they read GroupVersion
// for the management CRDs above.
var TrivyGroupVersion = schema.GroupVersion{Group: "aquasecurity.github.io", Version: "v1alpha1"}

// TrivyVulnerabilityReportKind is the upstream Kind name. Used by the
// CRD-mirror watcher when subscribing to `kind: VulnerabilityReport`
// events on TrivyGroupVersion.
const TrivyVulnerabilityReportKind = "VulnerabilityReport"

// SchemeBuilder collects the runtime registrations for both CRDs into a single
// runtime.SchemeBuilder so callers (controller manager, fake client) can add
// the types to a scheme in one call.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme registers all CRD kinds with the provided scheme. It is the
// standard kubebuilder-style entrypoint.
func AddToScheme(s *runtime.Scheme) error { return SchemeBuilder.AddToScheme(s) }

func addKnownTypes(s *runtime.Scheme) error {
	// v1alpha1 and v1beta1 are both SERVED by the apiserver (see the CRD chart
	// templates' versions list) with an identical schema, so conversion is the
	// identity transform and no conversion webhook is required. The Go scheme,
	// however, registers the shared Go types under the storage version only:
	// controller-runtime's typed client resolves a Go type to exactly one GVK
	// (apiutil.GVKForObject refuses to guess among several), so registering the
	// same struct under both versions would break every typed Get/List/Reconcile.
	// The v1beta1 served version lives purely in the CRD manifest; the Go client
	// always works against the storage version. See docs/crd-versioning.md.
	s.AddKnownTypes(GroupVersion,
		&Cluster{}, &ClusterList{},
		&Project{}, &ProjectList{},
		&ClusterBaseline{}, &ClusterBaselineList{},
		&ComponentBundle{}, &ComponentBundleList{},
		&AgentProfile{}, &AgentProfileList{},
		&GitOpsTarget{}, &GitOpsTargetList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

// FinalizerCluster is the finalizer string the controller installs on every
// Cluster CR. The decommission flow drops it once the DB-side cleanup is done
// (see ClusterSync.DeleteByName).
const FinalizerCluster = "management.astronomer.io/decommission"

// FinalizerProject is the equivalent for Project CRs — kept symmetric with the
// cluster finalizer even though the project delete path is synchronous, so the
// controller can swap in async cleanup later without re-shaping the CR.
const FinalizerProject = "management.astronomer.io/cleanup"

const (
	FinalizerClusterBaseline = "management.astronomer.io/clusterbaseline-cleanup"
	FinalizerComponentBundle = "management.astronomer.io/componentbundle-cleanup"
	FinalizerAgentProfile    = "management.astronomer.io/agentprofile-cleanup"
	FinalizerGitOpsTarget    = "management.astronomer.io/gitopstarget-cleanup"
)

// -----------------------------------------------------------------------------
// Cluster CRD
// -----------------------------------------------------------------------------

// ClusterSpec mirrors the operator-tunable subset of the `clusters` table.
//
// Operationally-derived fields (last_heartbeat, agent_version, kubernetes
// discovery values) are not part of the spec — they live in status only. The
// CRD spec is the desired state; the DB row stays the system of record and
// the controller is the bridge between them.
type ClusterSpec struct {
	// Name is the RFC-1123 cluster name written into clusters.name. Kept
	// distinct from metadata.name so CR rename (rare) can happen without a
	// DB cascade — though in practice operators set them to the same value
	// and the OpenAPI validation in the chart enforces RFC-1123 on both.
	Name string `json:"name"`

	// DisplayName is the human-friendly label rendered in the dashboard.
	// Optional; defaults to Name when empty.
	DisplayName string `json:"displayName,omitempty"`

	// Description is a free-form note.
	Description string `json:"description,omitempty"`

	// Environment is one of production / staging / development. The chart
	// schema enforces the enum.
	Environment string `json:"environment,omitempty"`

	// Region is the operator's free-form region tag (e.g. us-east-1).
	Region string `json:"region,omitempty"`

	// Provider is the cluster provider (eks / gke / aks / k3d / kind / …).
	Provider string `json:"provider,omitempty"`

	// Distribution captures the k8s distribution (vanilla / k3s / openshift …).
	Distribution string `json:"distribution,omitempty"`

	// Labels become clusters.labels (JSONB column).
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations become clusters.annotations.
	Annotations map[string]string `json:"annotations,omitempty"`

	// ProjectRefs is an operator-supplied list of project names that should
	// reference this cluster. The controller currently records the list onto
	// .status.observedProjectRefs for visibility; the projects → cluster
	// linkage itself lives on the Project CR (ClusterID field) as that is
	// the schema's source of truth.
	ProjectRefs []string `json:"projectRefs,omitempty"`

	// ArgoCD controls automatic registration into the built-in ArgoCD fleet.
	ArgoCD ClusterArgoCDSpec `json:"argocd,omitempty"`

	// Baseline selects the platform baseline profile ArgoCD should reconcile.
	Baseline ClusterBaselineProfileSpec `json:"baseline,omitempty"`

	// Agent controls adopted-cluster agent behavior such as privilege profile.
	Agent ClusterAgentSpec `json:"agent,omitempty"`

	// AdoptionPolicy captures fleet-management policy that should travel with
	// the cluster's declarative intent without introducing a separate CRD yet.
	AdoptionPolicy ClusterAdoptionPolicySpec `json:"adoptionPolicy,omitempty"`
}

type ClusterArgoCDSpec struct {
	// AutoAdopt controls whether the cluster should be registered into ArgoCD.
	// Nil means use the platform default.
	AutoAdopt *bool `json:"autoAdopt,omitempty"`

	// InstanceRef optionally targets a named ArgoCD instance.
	InstanceRef string `json:"instanceRef,omitempty"`
}

type ClusterBaselineProfileSpec struct {
	// Profile names the platform baseline profile. Empty means platform default.
	Profile string `json:"profile,omitempty"`
}

type ClusterAgentSpec struct {
	// PrivilegeProfile is viewer | operator | namespace-viewer |
	// namespace-operator | custom | admin. Empty means platform default.
	PrivilegeProfile string `json:"privilegeProfile,omitempty"`

	// ProfileRef points to a same-namespace AgentProfile. When set, the Cluster
	// reconciler resolves that profile and projects its privilegeProfile into the
	// cluster annotations consumed by registration manifest rendering.
	ProfileRef string `json:"profileRef,omitempty"`
}

type ClusterAdoptionPolicySpec struct {
	// Mode is manual | auto. Empty means use the platform default.
	Mode string `json:"mode,omitempty"`

	// AllowedManagementModes constrains which deployment engines may own
	// cluster components. Valid values are argocd, helm, and manual.
	AllowedManagementModes []string `json:"allowedManagementModes,omitempty"`
}

// ClusterStatus is the controller-managed view of the DB-side cluster state.
type ClusterStatus struct {
	// ClusterID is the canonical DB UUID. Set on first successful reconcile,
	// stable thereafter — operators can correlate kubectl get cluster with
	// the audit log / REST API.
	ClusterID string `json:"clusterId,omitempty"`

	// Phase is a coarse lifecycle state: pending | registered | decommissioned.
	Phase string `json:"phase,omitempty"`

	// LastReconciled is the wallclock of the last successful Reconcile pass.
	LastReconciled metav1.Time `json:"lastReconciled,omitempty"`

	// AgentVersion is the most recent agent build the cluster reported, mirrored
	// from clusters.agent_version for at-a-glance kubectl visibility.
	AgentVersion string `json:"agentVersion,omitempty"`

	// ObservedProjectRefs records the spec.projectRefs value we last saw.
	// Surfaced in status so operators have a single read for "what did the
	// controller actually pick up", helpful when the spec was hand-edited.
	ObservedProjectRefs []string `json:"observedProjectRefs,omitempty"`

	// ArgoCD reports the adoption state for this cluster.
	ArgoCD ClusterArgoCDStatus `json:"argocd,omitempty"`

	// Conditions are standard-shape K8s conditions. Stored as raw entries so
	// the controller can append without dragging in apimachinery validation.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ClusterArgoCDStatus struct {
	// Phase is pending | registering | registered | failed | disabled.
	Phase string `json:"phase,omitempty"`

	// ClusterSecretName is the ArgoCD cluster Secret name when registered.
	ClusterSecretName string `json:"clusterSecretName,omitempty"`

	// Conditions explain adoption and baseline matching failures.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// Cluster is the Kubernetes-side representation of a registered cluster row.
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterList is the controller-runtime List wrapper around Cluster.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

// DeepCopyInto copies receiver into the destination Cluster. We hand-roll
// DeepCopy* instead of code-generating with deepcopy-gen — the schema is
// small enough that a manual implementation is cheaper than dragging the
// generator into the build pipeline.
func (in *Cluster) DeepCopyInto(out *Cluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is the kubebuilder-conventional name for the typed copy.
func (in *Cluster) DeepCopy() *Cluster {
	if in == nil {
		return nil
	}
	out := new(Cluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *Cluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto on the list copies all items into the destination.
func (in *ClusterList) DeepCopyInto(out *ClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Cluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a typed deep copy of the list.
func (in *ClusterList) DeepCopy() *ClusterList {
	if in == nil {
		return nil
	}
	out := new(ClusterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *ClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto on ClusterSpec copies maps/slices defensively so callers can
// mutate the destination without aliasing the source.
func (in *ClusterSpec) DeepCopyInto(out *ClusterSpec) {
	*out = *in
	if in.ArgoCD.AutoAdopt != nil {
		v := *in.ArgoCD.AutoAdopt
		out.ArgoCD.AutoAdopt = &v
	}
	if in.Labels != nil {
		out.Labels = make(map[string]string, len(in.Labels))
		for k, v := range in.Labels {
			out.Labels[k] = v
		}
	}
	if in.Annotations != nil {
		out.Annotations = make(map[string]string, len(in.Annotations))
		for k, v := range in.Annotations {
			out.Annotations[k] = v
		}
	}
	if in.ProjectRefs != nil {
		out.ProjectRefs = append([]string(nil), in.ProjectRefs...)
	}
	if in.AdoptionPolicy.AllowedManagementModes != nil {
		out.AdoptionPolicy.AllowedManagementModes = append([]string(nil), in.AdoptionPolicy.AllowedManagementModes...)
	}
}

// DeepCopyInto on ClusterStatus copies conditions + project refs defensively.
func (in *ClusterStatus) DeepCopyInto(out *ClusterStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	if in.ObservedProjectRefs != nil {
		out.ObservedProjectRefs = append([]string(nil), in.ObservedProjectRefs...)
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.ArgoCD.Conditions != nil {
		out.ArgoCD.Conditions = make([]metav1.Condition, len(in.ArgoCD.Conditions))
		for i := range in.ArgoCD.Conditions {
			in.ArgoCD.Conditions[i].DeepCopyInto(&out.ArgoCD.Conditions[i])
		}
	}
}

// -----------------------------------------------------------------------------
// Project CRD
// -----------------------------------------------------------------------------

// ProjectResourceQuota mirrors the per-project quota knobs stored on the
// projects row. Strings are pass-through to the DB columns (which are
// freeform-text resource.Quantity values like "16" or "32Gi"); PodCount is the
// integer column.
type ProjectResourceQuota struct {
	CPULimit    string `json:"cpuLimit,omitempty"`
	MemoryLimit string `json:"memoryLimit,omitempty"`
	PodCount    int32  `json:"podCount,omitempty"`
}

// ProjectSpec mirrors the operator-tunable subset of `projects`.
type ProjectSpec struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`

	// PodSecurityProfile is the per-project PSS label: privileged | baseline
	// | restricted. The chart-side OpenAPI validation enforces the enum.
	PodSecurityProfile string `json:"podSecurityProfile,omitempty"`

	// ResourceQuota groups the three quota columns so kubectl edit reads as
	// a single block rather than three flat fields.
	ResourceQuota ProjectResourceQuota `json:"resourceQuota,omitempty"`

	// NetworkPolicyMode is the per-project network-policy enforcement level:
	// none | open | isolated.
	NetworkPolicyMode string `json:"networkPolicyMode,omitempty"`

	// Clusters is the list of cluster names the project is bound to.
	// projects.cluster_id is single-valued today; the controller uses the
	// FIRST entry to resolve clusters.id. Extra entries are recorded on
	// status.observedClusters for visibility.
	Clusters []string `json:"clusters,omitempty"`
}

// ProjectStatus is the controller-managed view of the DB-side project state.
type ProjectStatus struct {
	// ProjectID is the canonical DB UUID — same role as ClusterStatus.ClusterID.
	ProjectID string `json:"projectId,omitempty"`

	// Phase is pending | active | tombstoned. Today the project lifecycle is
	// binary (created / deleted) so this is mostly forward-looking, but it
	// keeps the field-shape symmetric with Cluster.status.
	Phase string `json:"phase,omitempty"`

	// LastReconciled is the wallclock of the last successful Reconcile pass.
	LastReconciled metav1.Time `json:"lastReconciled,omitempty"`

	// ResolvedClusterID is the cluster UUID we mapped spec.clusters[0] to,
	// surfaced so operators can correlate the project with a cluster row
	// without an extra read.
	ResolvedClusterID string `json:"resolvedClusterId,omitempty"`

	// ObservedClusters echoes spec.clusters so the operator can see what
	// the controller saw (and which entries beyond the first were ignored).
	ObservedClusters []string `json:"observedClusters,omitempty"`

	// Conditions matches the standard k8s shape.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// Project is the Kubernetes-side representation of a project row.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProjectSpec   `json:"spec,omitempty"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList is the controller-runtime List wrapper.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

// DeepCopyInto on Project.
func (in *Project) DeepCopyInto(out *Project) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is the kubebuilder-conventional name.
func (in *Project) DeepCopy() *Project {
	if in == nil {
		return nil
	}
	out := new(Project)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *Project) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto on ProjectList copies items defensively.
func (in *ProjectList) DeepCopyInto(out *ProjectList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Project, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy on the list.
func (in *ProjectList) DeepCopy() *ProjectList {
	if in == nil {
		return nil
	}
	out := new(ProjectList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *ProjectList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto on ProjectSpec.
func (in *ProjectSpec) DeepCopyInto(out *ProjectSpec) {
	*out = *in
	out.ResourceQuota = in.ResourceQuota
	if in.Clusters != nil {
		out.Clusters = append([]string(nil), in.Clusters...)
	}
}

// DeepCopyInto on ProjectStatus.
func (in *ProjectStatus) DeepCopyInto(out *ProjectStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	if in.ObservedClusters != nil {
		out.ObservedClusters = append([]string(nil), in.ObservedClusters...)
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// -----------------------------------------------------------------------------
// ClusterBaseline CRD
// -----------------------------------------------------------------------------

type LabelSelectorSpec struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	ClusterRefs []string          `json:"clusterRefs,omitempty"`
}

type ClusterBaselineBundleRef struct {
	Name       string                        `json:"name"`
	Version    string                        `json:"version,omitempty"`
	Enabled    *bool                         `json:"enabled,omitempty"`
	Values     map[string]string             `json:"values,omitempty"`
	ValuesFrom []ClusterBaselineValuesSource `json:"valuesFrom,omitempty"`
}

type ClusterBaselineValuesSource struct {
	// Type is git, secret, or configMap. Git sources become Argo Helm
	// valueFiles. Secret/configMap sources are modeled for governance and
	// validation but are not inlined into generated Argo specs.
	Type string `json:"type"`
	// Path is required for git values and must be a relative value file path.
	Path string `json:"path,omitempty"`
	// Name is required for secret/configMap values and must be same-namespace.
	Name string `json:"name,omitempty"`
	// Key is optional for secret/configMap values and defaults to values.yaml.
	Key string `json:"key,omitempty"`
	// Optional lets operators model environment-specific files or refs without
	// blocking reconciliation when the source is absent.
	Optional bool `json:"optional,omitempty"`
}

type ClusterBaselineSyncPolicy struct {
	Automated bool `json:"automated,omitempty"`
	Prune     bool `json:"prune,omitempty"`
	SelfHeal  bool `json:"selfHeal,omitempty"`
}

type ClusterBaselineSpec struct {
	ClusterSelector LabelSelectorSpec          `json:"clusterSelector,omitempty"`
	ProfileName     string                     `json:"profileName,omitempty"`
	Bundles         []ClusterBaselineBundleRef `json:"bundles,omitempty"`
	SyncPolicy      ClusterBaselineSyncPolicy  `json:"syncPolicy,omitempty"`
	Suspended       bool                       `json:"suspended,omitempty"`
}

type ClusterBaselineApplicationStatus struct {
	Name              string                  `json:"name,omitempty"`
	Namespace         string                  `json:"namespace,omitempty"`
	SyncStatus        string                  `json:"syncStatus,omitempty"`
	Health            string                  `json:"health,omitempty"`
	ApplicationCount  int32                   `json:"applicationCount,omitempty"`
	ChildApplications []ArgoApplicationStatus `json:"childApplications,omitempty"`
}

type ArgoApplicationResourceStatus struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Health    string `json:"health,omitempty"`
}

type ArgoApplicationStatus struct {
	Name           string                          `json:"name,omitempty"`
	Namespace      string                          `json:"namespace,omitempty"`
	SyncStatus     string                          `json:"syncStatus,omitempty"`
	Health         string                          `json:"health,omitempty"`
	Revision       string                          `json:"revision,omitempty"`
	OperationPhase string                          `json:"operationPhase,omitempty"`
	Message        string                          `json:"message,omitempty"`
	Resources      []ArgoApplicationResourceStatus `json:"resources,omitempty"`
}

type ClusterBaselineStatus struct {
	ObservedGeneration int64                              `json:"observedGeneration,omitempty"`
	Phase              string                             `json:"phase,omitempty"`
	LastReconciled     metav1.Time                        `json:"lastReconciled,omitempty"`
	TargetedClusters   []string                           `json:"targetedClusters,omitempty"`
	Applications       []ClusterBaselineApplicationStatus `json:"applications,omitempty"`
	Conditions         []metav1.Condition                 `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterBaseline struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterBaselineSpec   `json:"spec,omitempty"`
	Status ClusterBaselineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterBaselineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterBaseline `json:"items"`
}

func (in *ClusterBaseline) DeepCopyInto(out *ClusterBaseline) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *ClusterBaseline) DeepCopy() *ClusterBaseline {
	if in == nil {
		return nil
	}
	out := new(ClusterBaseline)
	in.DeepCopyInto(out)
	return out
}

func (in *ClusterBaseline) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ClusterBaselineList) DeepCopyInto(out *ClusterBaselineList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ClusterBaseline, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ClusterBaselineList) DeepCopy() *ClusterBaselineList {
	if in == nil {
		return nil
	}
	out := new(ClusterBaselineList)
	in.DeepCopyInto(out)
	return out
}

func (in *ClusterBaselineList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ClusterBaselineSpec) DeepCopyInto(out *ClusterBaselineSpec) {
	*out = *in
	in.ClusterSelector.DeepCopyInto(&out.ClusterSelector)
	out.Bundles = copyClusterBaselineBundleRefs(in.Bundles)
}

func (in *LabelSelectorSpec) DeepCopyInto(out *LabelSelectorSpec) {
	*out = *in
	out.MatchLabels = copyStringMap(in.MatchLabels)
	out.ClusterRefs = copyStringSlice(in.ClusterRefs)
}

func (in *ClusterBaselineStatus) DeepCopyInto(out *ClusterBaselineStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	out.TargetedClusters = copyStringSlice(in.TargetedClusters)
	if in.Applications != nil {
		out.Applications = copyClusterBaselineApplicationStatuses(in.Applications)
	}
	out.Conditions = copyConditions(in.Conditions)
}

// -----------------------------------------------------------------------------
// ComponentBundle CRD
// -----------------------------------------------------------------------------

type ComponentBundleSourceSpec struct {
	Type            string                     `json:"type"`
	RepoURL         string                     `json:"repoURL,omitempty"`
	Path            string                     `json:"path,omitempty"`
	Chart           string                     `json:"chart,omitempty"`
	TargetRevision  string                     `json:"targetRevision,omitempty"`
	ValuesSchemaRef string                     `json:"valuesSchemaRef,omitempty"`
	SecretRefs      []ComponentBundleSecretRef `json:"secretRefs,omitempty"`
}

type ComponentBundleHealthCheck struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type ComponentBundleSecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key,omitempty"`
}

type ComponentBundleRequirement struct {
	Feature string `json:"feature"`
	Reason  string `json:"reason,omitempty"`
}

type ComponentBundleUpgradePolicy struct {
	Strategy       string `json:"strategy,omitempty"`
	MaxUnavailable int32  `json:"maxUnavailable,omitempty"`
}

type ComponentBundleVersionSpec struct {
	Version                string                       `json:"version"`
	Description            string                       `json:"description,omitempty"`
	DefaultNamespace       string                       `json:"defaultNamespace,omitempty"`
	Source                 ComponentBundleSourceSpec    `json:"source"`
	CapabilityRequirements []ComponentBundleRequirement `json:"capabilityRequirements,omitempty"`
	HealthChecks           []ComponentBundleHealthCheck `json:"healthChecks,omitempty"`
	UpgradePolicy          ComponentBundleUpgradePolicy `json:"upgradePolicy,omitempty"`
}

type ComponentBundleSpec struct {
	Version                string                       `json:"version"`
	Description            string                       `json:"description,omitempty"`
	DefaultNamespace       string                       `json:"defaultNamespace,omitempty"`
	Source                 ComponentBundleSourceSpec    `json:"source"`
	CapabilityRequirements []ComponentBundleRequirement `json:"capabilityRequirements,omitempty"`
	HealthChecks           []ComponentBundleHealthCheck `json:"healthChecks,omitempty"`
	UpgradePolicy          ComponentBundleUpgradePolicy `json:"upgradePolicy,omitempty"`
	Versions               []ComponentBundleVersionSpec `json:"versions,omitempty"`
}

type ComponentBundleStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	LastReconciled     metav1.Time        `json:"lastReconciled,omitempty"`
	ResolvedRevision   string             `json:"resolvedRevision,omitempty"`
	AvailableVersions  []string           `json:"availableVersions,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type ComponentBundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentBundleSpec   `json:"spec,omitempty"`
	Status ComponentBundleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ComponentBundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComponentBundle `json:"items"`
}

func (in *ComponentBundle) DeepCopyInto(out *ComponentBundle) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *ComponentBundle) DeepCopy() *ComponentBundle {
	if in == nil {
		return nil
	}
	out := new(ComponentBundle)
	in.DeepCopyInto(out)
	return out
}

func (in *ComponentBundle) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ComponentBundleList) DeepCopyInto(out *ComponentBundleList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ComponentBundle, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ComponentBundleList) DeepCopy() *ComponentBundleList {
	if in == nil {
		return nil
	}
	out := new(ComponentBundleList)
	in.DeepCopyInto(out)
	return out
}

func (in *ComponentBundleList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ComponentBundleSpec) DeepCopyInto(out *ComponentBundleSpec) {
	*out = *in
	in.Source.DeepCopyInto(&out.Source)
	if in.CapabilityRequirements != nil {
		out.CapabilityRequirements = append([]ComponentBundleRequirement(nil), in.CapabilityRequirements...)
	}
	if in.HealthChecks != nil {
		out.HealthChecks = append([]ComponentBundleHealthCheck(nil), in.HealthChecks...)
	}
	if in.Versions != nil {
		out.Versions = make([]ComponentBundleVersionSpec, len(in.Versions))
		for i := range in.Versions {
			in.Versions[i].DeepCopyInto(&out.Versions[i])
		}
	}
}

func (in *ComponentBundleVersionSpec) DeepCopyInto(out *ComponentBundleVersionSpec) {
	*out = *in
	in.Source.DeepCopyInto(&out.Source)
	if in.CapabilityRequirements != nil {
		out.CapabilityRequirements = append([]ComponentBundleRequirement(nil), in.CapabilityRequirements...)
	}
	if in.HealthChecks != nil {
		out.HealthChecks = append([]ComponentBundleHealthCheck(nil), in.HealthChecks...)
	}
}

func (in *ComponentBundleSourceSpec) DeepCopyInto(out *ComponentBundleSourceSpec) {
	*out = *in
	if in.SecretRefs != nil {
		out.SecretRefs = append([]ComponentBundleSecretRef(nil), in.SecretRefs...)
	}
}

func (in *ComponentBundleStatus) DeepCopyInto(out *ComponentBundleStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	if in.AvailableVersions != nil {
		out.AvailableVersions = append([]string(nil), in.AvailableVersions...)
	}
	out.Conditions = copyConditions(in.Conditions)
}

// -----------------------------------------------------------------------------
// AgentProfile CRD
// -----------------------------------------------------------------------------

type AgentProfileSpec struct {
	PrivilegeProfile string                        `json:"privilegeProfile"`
	NamespaceScope   []string                      `json:"namespaceScope,omitempty"`
	Capabilities     map[string]bool               `json:"capabilities,omitempty"`
	AllowedRules     []AgentProfilePolicyRule      `json:"allowedRules,omitempty"`
	HostAccess       AgentProfileHostAccessSpec    `json:"hostAccess,omitempty"`
	NetworkEgress    AgentProfileNetworkEgressSpec `json:"networkEgress,omitempty"`
	Install          AgentProfileInstallSpec       `json:"install,omitempty"`
}

type AgentProfileInstallSpec struct {
	Image              string            `json:"image,omitempty"`
	ServiceAccountName string            `json:"serviceAccountName,omitempty"`
	PodLabels          map[string]string `json:"podLabels,omitempty"`
}

type AgentProfilePolicyRule struct {
	APIGroups     []string `json:"apiGroups,omitempty"`
	Resources     []string `json:"resources,omitempty"`
	Verbs         []string `json:"verbs,omitempty"`
	ResourceNames []string `json:"resourceNames,omitempty"`
}

type AgentProfileHostAccessSpec struct {
	HostNetwork      bool     `json:"hostNetwork,omitempty"`
	HostPID          bool     `json:"hostPID,omitempty"`
	HostPathPrefixes []string `json:"hostPathPrefixes,omitempty"`
}

type AgentProfileNetworkEgressSpec struct {
	Mode         string   `json:"mode,omitempty"`
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
}

type AgentProfileStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	LastReconciled     metav1.Time        `json:"lastReconciled,omitempty"`
	EffectiveRBAC      []string           `json:"effectiveRBAC,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type AgentProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentProfileSpec   `json:"spec,omitempty"`
	Status AgentProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProfile `json:"items"`
}

func (in *AgentProfile) DeepCopyInto(out *AgentProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *AgentProfile) DeepCopy() *AgentProfile {
	if in == nil {
		return nil
	}
	out := new(AgentProfile)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentProfile) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *AgentProfileList) DeepCopyInto(out *AgentProfileList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]AgentProfile, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *AgentProfileList) DeepCopy() *AgentProfileList {
	if in == nil {
		return nil
	}
	out := new(AgentProfileList)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentProfileList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *AgentProfileSpec) DeepCopyInto(out *AgentProfileSpec) {
	*out = *in
	out.NamespaceScope = copyStringSlice(in.NamespaceScope)
	if in.Capabilities != nil {
		out.Capabilities = make(map[string]bool, len(in.Capabilities))
		for k, v := range in.Capabilities {
			out.Capabilities[k] = v
		}
	}
	out.AllowedRules = copyAgentProfilePolicyRules(in.AllowedRules)
	out.HostAccess.HostPathPrefixes = copyStringSlice(in.HostAccess.HostPathPrefixes)
	out.NetworkEgress.AllowedCIDRs = copyStringSlice(in.NetworkEgress.AllowedCIDRs)
	out.NetworkEgress.AllowedHosts = copyStringSlice(in.NetworkEgress.AllowedHosts)
	out.Install.PodLabels = copyStringMap(in.Install.PodLabels)
}

func (in *AgentProfileStatus) DeepCopyInto(out *AgentProfileStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	out.EffectiveRBAC = copyStringSlice(in.EffectiveRBAC)
	out.Conditions = copyConditions(in.Conditions)
}

// -----------------------------------------------------------------------------
// GitOpsTarget CRD
// -----------------------------------------------------------------------------

type GitOpsTargetSelectorSpec struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	ClusterRefs []string          `json:"clusterRefs,omitempty"`
}

type GitOpsTargetApplicationSetSpec struct {
	TemplateRef string            `json:"templateRef,omitempty"`
	SourceRepo  string            `json:"sourceRepo,omitempty"`
	Path        string            `json:"path,omitempty"`
	Revision    string            `json:"revision,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

type GitOpsTargetBundleRef struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type GitOpsTargetSyncPolicy struct {
	Automated bool `json:"automated,omitempty"`
	Prune     bool `json:"prune,omitempty"`
	SelfHeal  bool `json:"selfHeal,omitempty"`
}

type GitOpsTargetSyncWindowSpec struct {
	Kind     string   `json:"kind"`
	Schedule string   `json:"schedule,omitempty"`
	Duration string   `json:"duration,omitempty"`
	Clusters []string `json:"clusters,omitempty"`
}

type GitOpsTargetSpec struct {
	Selector        GitOpsTargetSelectorSpec       `json:"selector,omitempty"`
	ProjectSelector LabelSelectorSpec              `json:"projectSelector,omitempty"`
	BundleRef       GitOpsTargetBundleRef          `json:"bundleRef,omitempty"`
	ApplicationSet  GitOpsTargetApplicationSetSpec `json:"applicationSet"`
	SyncPolicy      GitOpsTargetSyncPolicy         `json:"syncPolicy,omitempty"`
	SyncWindows     []GitOpsTargetSyncWindowSpec   `json:"syncWindows,omitempty"`
	Suspended       bool                           `json:"suspended,omitempty"`
}

type GitOpsTargetStatus struct {
	ObservedGeneration int64                   `json:"observedGeneration,omitempty"`
	Phase              string                  `json:"phase,omitempty"`
	LastReconciled     metav1.Time             `json:"lastReconciled,omitempty"`
	MatchedClusters    []string                `json:"matchedClusters,omitempty"`
	ApplicationSetName string                  `json:"applicationSetName,omitempty"`
	SyncStatus         string                  `json:"syncStatus,omitempty"`
	Health             string                  `json:"health,omitempty"`
	ApplicationCount   int32                   `json:"applicationCount,omitempty"`
	Applications       []ArgoApplicationStatus `json:"applications,omitempty"`
	Conditions         []metav1.Condition      `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type GitOpsTarget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitOpsTargetSpec   `json:"spec,omitempty"`
	Status GitOpsTargetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GitOpsTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitOpsTarget `json:"items"`
}

func (in *GitOpsTarget) DeepCopyInto(out *GitOpsTarget) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *GitOpsTarget) DeepCopy() *GitOpsTarget {
	if in == nil {
		return nil
	}
	out := new(GitOpsTarget)
	in.DeepCopyInto(out)
	return out
}

func (in *GitOpsTarget) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *GitOpsTargetList) DeepCopyInto(out *GitOpsTargetList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]GitOpsTarget, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *GitOpsTargetList) DeepCopy() *GitOpsTargetList {
	if in == nil {
		return nil
	}
	out := new(GitOpsTargetList)
	in.DeepCopyInto(out)
	return out
}

func (in *GitOpsTargetList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *GitOpsTargetSpec) DeepCopyInto(out *GitOpsTargetSpec) {
	*out = *in
	(*LabelSelectorSpec)(&in.Selector).DeepCopyInto((*LabelSelectorSpec)(&out.Selector))
	in.ProjectSelector.DeepCopyInto(&out.ProjectSelector)
	out.ApplicationSet.Parameters = copyStringMap(in.ApplicationSet.Parameters)
	if in.SyncWindows != nil {
		out.SyncWindows = make([]GitOpsTargetSyncWindowSpec, len(in.SyncWindows))
		copy(out.SyncWindows, in.SyncWindows)
		for i := range in.SyncWindows {
			out.SyncWindows[i].Clusters = copyStringSlice(in.SyncWindows[i].Clusters)
		}
	}
}

func (in *GitOpsTargetStatus) DeepCopyInto(out *GitOpsTargetStatus) {
	*out = *in
	in.LastReconciled.DeepCopyInto(&out.LastReconciled)
	out.MatchedClusters = copyStringSlice(in.MatchedClusters)
	out.Applications = copyArgoApplicationStatuses(in.Applications)
	out.Conditions = copyConditions(in.Conditions)
}

func copyClusterBaselineApplicationStatuses(in []ClusterBaselineApplicationStatus) []ClusterBaselineApplicationStatus {
	if in == nil {
		return nil
	}
	out := make([]ClusterBaselineApplicationStatus, len(in))
	copy(out, in)
	for i := range in {
		out[i].ChildApplications = copyArgoApplicationStatuses(in[i].ChildApplications)
	}
	return out
}

func copyArgoApplicationStatuses(in []ArgoApplicationStatus) []ArgoApplicationStatus {
	if in == nil {
		return nil
	}
	out := make([]ArgoApplicationStatus, len(in))
	copy(out, in)
	for i := range in {
		out[i].Resources = append([]ArgoApplicationResourceStatus(nil), in[i].Resources...)
	}
	return out
}

func copyClusterBaselineBundleRefs(in []ClusterBaselineBundleRef) []ClusterBaselineBundleRef {
	if in == nil {
		return nil
	}
	out := make([]ClusterBaselineBundleRef, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Enabled != nil {
			v := *in[i].Enabled
			out[i].Enabled = &v
		}
		out[i].Values = copyStringMap(in[i].Values)
		out[i].ValuesFrom = copyClusterBaselineValuesSources(in[i].ValuesFrom)
	}
	return out
}

func copyClusterBaselineValuesSources(in []ClusterBaselineValuesSource) []ClusterBaselineValuesSource {
	if in == nil {
		return nil
	}
	out := make([]ClusterBaselineValuesSource, len(in))
	copy(out, in)
	return out
}

func copyAgentProfilePolicyRules(in []AgentProfilePolicyRule) []AgentProfilePolicyRule {
	if in == nil {
		return nil
	}
	out := make([]AgentProfilePolicyRule, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].APIGroups = copyStringSlice(in[i].APIGroups)
		out[i].Resources = copyStringSlice(in[i].Resources)
		out[i].Verbs = copyStringSlice(in[i].Verbs)
		out[i].ResourceNames = copyStringSlice(in[i].ResourceNames)
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func copyConditions(in []metav1.Condition) []metav1.Condition {
	if in == nil {
		return nil
	}
	out := make([]metav1.Condition, len(in))
	for i := range in {
		in[i].DeepCopyInto(&out[i])
	}
	return out
}

// Compile-time assertion that the time package import survives. The package
// is imported transitively by metav1.Time; the var keeps `go vet` happy if a
// future refactor removes the metav1 path.
var _ = time.Now
