package argolabels

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

const (
	ArgoCDClusterSecretTypeLabel = "argocd.argoproj.io/secret-type"
	ArgoCDClusterSecretTypeValue = "cluster"

	LabelPrefix                 = "astronomer.io/label-"
	ManagedByLabelKey           = "astronomer.io/managed-by"
	ManagedByLabelValue         = "astronomer"
	ClusterIDLabelKey           = "astronomer.io/cluster-id"
	ClusterNameLabelKey         = "astronomer.io/cluster-name"
	EnvironmentLabelKey         = "astronomer.io/environment"
	IsLocalLabelKey             = "astronomer.io/is-local"
	RegionLabelKey              = "astronomer.io/region"
	ProviderLabelKey            = "astronomer.io/provider"
	DistributionLabelKey        = "astronomer.io/distribution"
	AgentProfileLabelKey        = "astronomer.io/agent-privilege-profile"
	AgentVersionLabelKey        = "astronomer.io/agent-version"
	KubernetesVersionLabelKey   = "astronomer.io/kubernetes-version"
	ProjectLabelKey             = "astronomer.io/project"
	ProjectIDLabelKey           = "astronomer.io/project-id"
	ProjectMembershipPrefix     = "astronomer.io/project."
	ProjectIDMembershipPrefix   = "astronomer.io/project-id."
	ProjectMembershipLabelValue = "true"
)

const maxLabelNameLen = 63

// ProjectLister is the optional database surface used to include project
// membership labels on Argo CD managed clusters.
type ProjectLister interface {
	ListProjectsByCluster(ctx context.Context, arg sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error)
}

// ProjectsForCluster loads project memberships for Argo CD cluster labeling.
// A query object that does not expose project membership support is tolerated
// for tests and narrow fakes.
func ProjectsForCluster(ctx context.Context, q any, clusterID uuid.UUID) ([]sqlc.Project, error) {
	lister, ok := q.(ProjectLister)
	if !ok {
		return nil, nil
	}
	return lister.ListProjectsByCluster(ctx, sqlc.ListProjectsByClusterParams{ClusterID: clusterID, Limit: 1000, Offset: 0})
}

// ManagedClusterLabels builds the Astronomer-owned label set stamped onto Argo
// CD cluster Secrets and mirrored into argocd_managed_clusters.labels.
func ManagedClusterLabels(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	labels := map[string]string{
		ManagedByLabelKey:    ManagedByLabelValue,
		ClusterIDLabelKey:    cluster.ID.String(),
		ClusterNameLabelKey:  cluster.Name,
		IsLocalLabelKey:      fmt.Sprintf("%t", cluster.IsLocal),
		AgentProfileLabelKey: ClusterAgentPrivilegeProfile(cluster.Annotations),
	}
	if cluster.Environment != "" {
		labels[EnvironmentLabelKey] = cluster.Environment
	}
	if cluster.Region != "" {
		labels[RegionLabelKey] = cluster.Region
	}
	if cluster.Provider != "" {
		labels[ProviderLabelKey] = cluster.Provider
	}
	if cluster.Distribution != "" {
		labels[DistributionLabelKey] = cluster.Distribution
	}
	if value := SanitizeLabelValue(cluster.AgentVersion); value != "" {
		labels[AgentVersionLabelKey] = value
	}
	if value := SanitizeLabelValue(cluster.KubernetesVersion); value != "" {
		labels[KubernetesVersionLabelKey] = value
	}
	ApplyClusterLabels(labels, cluster.Labels)
	ApplyProjectLabels(labels, projects)
	return labels
}

// SecretLabels returns ManagedClusterLabels plus Argo CD's cluster Secret type
// marker. The DB index row should store ManagedClusterLabels instead.
func SecretLabels(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	labels := ManagedClusterLabels(cluster, projects)
	labels[ArgoCDClusterSecretTypeLabel] = ArgoCDClusterSecretTypeValue
	return labels
}

// ApplyClusterLabels mirrors cluster labels under astronomer.io/label-*.
func ApplyClusterLabels(dst map[string]string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var clusterLabels map[string]string
	if err := json.Unmarshal(raw, &clusterLabels); err != nil {
		return
	}
	for k, v := range clusterLabels {
		key := SanitizeLabelKey(k)
		if key == "" {
			continue
		}
		full := LabelPrefix + key
		if _, exists := dst[full]; exists {
			continue
		}
		dst[full] = v
	}
}

// ApplyProjectLabels adds durable project targeting labels. The singular
// project/project-id labels are only emitted when the cluster has exactly one
// project. Per-project membership labels are emitted for every project.
func ApplyProjectLabels(dst map[string]string, projects []sqlc.Project) {
	clean := make([]sqlc.Project, 0, len(projects))
	for _, project := range projects {
		if project.ID == uuid.Nil && strings.TrimSpace(project.Name) == "" {
			continue
		}
		clean = append(clean, project)
	}
	if len(clean) == 1 {
		project := clean[0]
		if project.ID != uuid.Nil {
			dst[ProjectIDLabelKey] = project.ID.String()
		}
		if value := SanitizeLabelValue(project.Name); value != "" {
			dst[ProjectLabelKey] = value
		}
	}
	for _, project := range clean {
		if project.ID != uuid.Nil {
			dst[ProjectIDMembershipPrefix+project.ID.String()] = ProjectMembershipLabelValue
		}
		if key := ProjectNameMembershipLabel(project.Name); key != "" {
			dst[key] = ProjectMembershipLabelValue
		}
	}
}

func ProjectNameMembershipLabel(name string) string {
	suffix := SanitizeLabelKeyWithLimit(name, maxLabelNameLen-len("project."))
	if suffix == "" {
		return ""
	}
	return ProjectMembershipPrefix + suffix
}

func IsOwnedLabel(k string) bool {
	switch k {
	case ManagedByLabelKey,
		ClusterIDLabelKey,
		ClusterNameLabelKey,
		EnvironmentLabelKey,
		IsLocalLabelKey,
		RegionLabelKey,
		ProviderLabelKey,
		DistributionLabelKey,
		AgentProfileLabelKey,
		AgentVersionLabelKey,
		KubernetesVersionLabelKey,
		ProjectLabelKey,
		ProjectIDLabelKey:
		return true
	}
	return strings.HasPrefix(k, LabelPrefix) ||
		strings.HasPrefix(k, ProjectMembershipPrefix) ||
		strings.HasPrefix(k, ProjectIDMembershipPrefix)
}

func ClusterAgentPrivilegeProfile(raw json.RawMessage) string {
	// Absent/unparseable annotations mean "unspecified" — which now defaults to
	// full management control via NormalizePrivilegeProfile (Rancher-style).
	if len(raw) == 0 {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	return agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation])
}

func SanitizeLabelKey(in string) string {
	return SanitizeLabelKeyWithLimit(in, maxLabelNameLen)
}

func SanitizeLabelValue(in string) string {
	return SanitizeLabelKeyWithLimit(in, maxLabelNameLen)
}

func SanitizeLabelKeyWithLimit(in string, limit int) string {
	if in == "" || limit <= 0 {
		return ""
	}
	out := make([]byte, 0, len(in))
	lastDash := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
			lastDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-':
			out = append(out, c)
			lastDash = c == '-'
		default:
			if !lastDash && len(out) > 0 {
				out = append(out, '-')
				lastDash = true
			}
		}
	}
	start := 0
	for start < len(out) && !isLabelAlnum(out[start]) {
		start++
	}
	end := len(out)
	for end > start && !isLabelAlnum(out[end-1]) {
		end--
	}
	out = out[start:end]
	if len(out) > limit {
		out = out[:limit]
		for len(out) > 0 && !isLabelAlnum(out[len(out)-1]) {
			out = out[:len(out)-1]
		}
	}
	return string(out)
}

func isLabelAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
