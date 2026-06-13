// Package crd implements the CRD-mirror management API for astronomer-go.
//
// Two CustomResourceDefinitions —
//   - Cluster.management.astronomer.io/v1alpha1
//   - Project.management.astronomer.io/v1alpha1
//
// — are reconciled against the existing `clusters` and `projects` DB tables.
// The CRD spec is authoritative when present; the controller writes through to
// the DB via the same handler-layer surface the REST API uses. Status patches
// reflect DB-side state back to the cluster (poll-based, see controller.go).
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
var GroupVersion = schema.GroupVersion{Group: "management.astronomer.io", Version: "v1alpha1"}

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
	s.AddKnownTypes(GroupVersion,
		&Cluster{}, &ClusterList{},
		&Project{}, &ProjectList{},
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
	Baseline ClusterBaselineSpec `json:"baseline,omitempty"`

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

type ClusterBaselineSpec struct {
	// Profile names the platform baseline profile. Empty means platform default.
	Profile string `json:"profile,omitempty"`
}

type ClusterAgentSpec struct {
	// PrivilegeProfile is viewer | operator | admin. Empty means platform default.
	PrivilegeProfile string `json:"privilegeProfile,omitempty"`
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

// Compile-time assertion that the time package import survives. The package
// is imported transitively by metav1.Time; the var keeps `go vet` happy if a
// future refactor removes the metav1 path.
var _ = time.Now
