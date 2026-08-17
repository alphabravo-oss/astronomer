// Package crd contains the retained Kubernetes-native management API.
// Delivery intent is represented by the first-party delivery API and Flux
// objects; the former Argo-specific baseline, bundle, and target CRDs are not
// part of this scheme.
package crd

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "management.astronomer.io", Version: "v1alpha1"}
var GroupVersionV1Beta1 = schema.GroupVersion{Group: "management.astronomer.io", Version: "v1beta1"}
var TrivyGroupVersion = schema.GroupVersion{Group: "aquasecurity.github.io", Version: "v1alpha1"}

const TrivyVulnerabilityReportKind = "VulnerabilityReport"

var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

func AddToScheme(s *runtime.Scheme) error { return SchemeBuilder.AddToScheme(s) }

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion,
		&Cluster{}, &ClusterList{},
		&Project{}, &ProjectList{},
		&AgentProfile{}, &AgentProfileList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

const (
	FinalizerCluster      = "management.astronomer.io/decommission"
	FinalizerProject      = "management.astronomer.io/cleanup"
	FinalizerAgentProfile = "management.astronomer.io/agentprofile-cleanup"
)

type ClusterSpec struct {
	Name           string                    `json:"name"`
	DisplayName    string                    `json:"displayName,omitempty"`
	Description    string                    `json:"description,omitempty"`
	Environment    string                    `json:"environment,omitempty"`
	Region         string                    `json:"region,omitempty"`
	Provider       string                    `json:"provider,omitempty"`
	Distribution   string                    `json:"distribution,omitempty"`
	Labels         map[string]string         `json:"labels,omitempty"`
	Annotations    map[string]string         `json:"annotations,omitempty"`
	ProjectRefs    []string                  `json:"projectRefs,omitempty"`
	Agent          ClusterAgentSpec          `json:"agent,omitempty"`
	AdoptionPolicy ClusterAdoptionPolicySpec `json:"adoptionPolicy,omitempty"`
}

type ClusterAgentSpec struct {
	PrivilegeProfile string `json:"privilegeProfile,omitempty"`
	ProfileRef       string `json:"profileRef,omitempty"`
}

type ClusterAdoptionPolicySpec struct {
	Mode                   string   `json:"mode,omitempty"`
	AllowedManagementModes []string `json:"allowedManagementModes,omitempty"`
}

type ClusterStatus struct {
	ClusterID           string             `json:"clusterId,omitempty"`
	Phase               string             `json:"phase,omitempty"`
	LastReconciled      metav1.Time        `json:"lastReconciled,omitempty"`
	AgentVersion        string             `json:"agentVersion,omitempty"`
	ObservedProjectRefs []string           `json:"observedProjectRefs,omitempty"`
	Conditions          []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSpec   `json:"spec,omitempty"`
	Status            ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

type ProjectResourceQuota struct {
	CPULimit    string `json:"cpuLimit,omitempty"`
	MemoryLimit string `json:"memoryLimit,omitempty"`
	PodCount    int32  `json:"podCount,omitempty"`
}

type ProjectSpec struct {
	Name               string               `json:"name"`
	DisplayName        string               `json:"displayName,omitempty"`
	Description        string               `json:"description,omitempty"`
	PodSecurityProfile string               `json:"podSecurityProfile,omitempty"`
	ResourceQuota      ProjectResourceQuota `json:"resourceQuota,omitempty"`
	NetworkPolicyMode  string               `json:"networkPolicyMode,omitempty"`
	Clusters           []string             `json:"clusters,omitempty"`
}

type ProjectStatus struct {
	ProjectID         string             `json:"projectId,omitempty"`
	Phase             string             `json:"phase,omitempty"`
	LastReconciled    metav1.Time        `json:"lastReconciled,omitempty"`
	ResolvedClusterID string             `json:"resolvedClusterId,omitempty"`
	ObservedClusters  []string           `json:"observedClusters,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ProjectSpec   `json:"spec,omitempty"`
	Status            ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

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
	Spec              AgentProfileSpec   `json:"spec,omitempty"`
	Status            AgentProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProfile `json:"items"`
}

func (in *Cluster) DeepCopyInto(out *Cluster) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}
func (in *Cluster) DeepCopy() *Cluster {
	if in == nil {
		return nil
	}
	out := new(Cluster)
	in.DeepCopyInto(out)
	return out
}
func (in *Cluster) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *ClusterList) DeepCopyInto(out *ClusterList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Cluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}
func (in *ClusterList) DeepCopy() *ClusterList {
	if in == nil {
		return nil
	}
	out := new(ClusterList)
	in.DeepCopyInto(out)
	return out
}
func (in *ClusterList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *ClusterSpec) DeepCopyInto(out *ClusterSpec) {
	*out = *in
	out.Labels = copyStringMap(in.Labels)
	out.Annotations = copyStringMap(in.Annotations)
	out.ProjectRefs = copyStringSlice(in.ProjectRefs)
	out.AdoptionPolicy.AllowedManagementModes = copyStringSlice(in.AdoptionPolicy.AllowedManagementModes)
}
func (in *ClusterStatus) DeepCopyInto(out *ClusterStatus) {
	*out = *in
	out.ObservedProjectRefs = copyStringSlice(in.ObservedProjectRefs)
	out.Conditions = copyConditions(in.Conditions)
}

func (in *Project) DeepCopyInto(out *Project) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}
func (in *Project) DeepCopy() *Project {
	if in == nil {
		return nil
	}
	out := new(Project)
	in.DeepCopyInto(out)
	return out
}
func (in *Project) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *ProjectList) DeepCopyInto(out *ProjectList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Project, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}
func (in *ProjectList) DeepCopy() *ProjectList {
	if in == nil {
		return nil
	}
	out := new(ProjectList)
	in.DeepCopyInto(out)
	return out
}
func (in *ProjectList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *ProjectSpec) DeepCopyInto(out *ProjectSpec) {
	*out = *in
	out.Clusters = copyStringSlice(in.Clusters)
}
func (in *ProjectStatus) DeepCopyInto(out *ProjectStatus) {
	*out = *in
	out.ObservedClusters = copyStringSlice(in.ObservedClusters)
	out.Conditions = copyConditions(in.Conditions)
}

func (in *AgentProfile) DeepCopyInto(out *AgentProfile) {
	*out = *in
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
func (in *AgentProfile) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *AgentProfileList) DeepCopyInto(out *AgentProfileList) {
	*out = *in
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
func (in *AgentProfileList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *AgentProfileSpec) DeepCopyInto(out *AgentProfileSpec) {
	*out = *in
	out.NamespaceScope = copyStringSlice(in.NamespaceScope)
	if in.Capabilities != nil {
		out.Capabilities = make(map[string]bool, len(in.Capabilities))
		for k, v := range in.Capabilities {
			out.Capabilities[k] = v
		}
	}
	if in.AllowedRules != nil {
		out.AllowedRules = make([]AgentProfilePolicyRule, len(in.AllowedRules))
		for i := range in.AllowedRules {
			out.AllowedRules[i] = in.AllowedRules[i]
			out.AllowedRules[i].APIGroups = copyStringSlice(in.AllowedRules[i].APIGroups)
			out.AllowedRules[i].Resources = copyStringSlice(in.AllowedRules[i].Resources)
			out.AllowedRules[i].Verbs = copyStringSlice(in.AllowedRules[i].Verbs)
			out.AllowedRules[i].ResourceNames = copyStringSlice(in.AllowedRules[i].ResourceNames)
		}
	}
	out.HostAccess.HostPathPrefixes = copyStringSlice(in.HostAccess.HostPathPrefixes)
	out.NetworkEgress.AllowedCIDRs = copyStringSlice(in.NetworkEgress.AllowedCIDRs)
	out.NetworkEgress.AllowedHosts = copyStringSlice(in.NetworkEgress.AllowedHosts)
	out.Install.PodLabels = copyStringMap(in.Install.PodLabels)
}
func (in *AgentProfileStatus) DeepCopyInto(out *AgentProfileStatus) {
	*out = *in
	out.EffectiveRBAC = copyStringSlice(in.EffectiveRBAC)
	out.Conditions = copyConditions(in.Conditions)
}

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
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
