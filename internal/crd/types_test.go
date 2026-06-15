package crd

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestClusterSpecDeepCopyCopiesAdoptionPolicyModes(t *testing.T) {
	in := ClusterSpec{
		Name: "prod-east",
		AdoptionPolicy: ClusterAdoptionPolicySpec{
			Mode:                   "auto",
			AllowedManagementModes: []string{"argocd", "helm"},
		},
	}
	out := ClusterSpec{}
	in.DeepCopyInto(&out)

	out.AdoptionPolicy.AllowedManagementModes[0] = "manual"
	if in.AdoptionPolicy.AllowedManagementModes[0] != "argocd" {
		t.Fatalf("DeepCopyInto aliased adoption policy modes: %+v", in.AdoptionPolicy.AllowedManagementModes)
	}
}

func TestManagementCRDsRegisterWithScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, obj := range []runtime.Object{
		&ClusterBaseline{},
		&ClusterBaselineList{},
		&ComponentBundle{},
		&ComponentBundleList{},
		&AgentProfile{},
		&AgentProfileList{},
		&GitOpsTarget{},
		&GitOpsTargetList{},
	} {
		kinds, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			t.Fatalf("ObjectKinds(%T): %v", obj, err)
		}
		if len(kinds) == 0 || kinds[0].GroupVersion() != GroupVersion {
			t.Fatalf("ObjectKinds(%T) = %+v, want group version %s", obj, kinds, GroupVersion)
		}
	}
}

func TestNewManagementCRDDeepCopiesDoNotAliasNestedSlicesAndMaps(t *testing.T) {
	enabled := true
	baseline := ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "baseline"},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{
				MatchLabels: map[string]string{"env": "prod"},
				ClusterRefs: []string{"prod-east"},
			},
			Bundles: []ClusterBaselineBundleRef{{
				Name:       "ingress",
				Enabled:    &enabled,
				Values:     map[string]string{"replicas": "2"},
				ValuesFrom: []ClusterBaselineValuesSource{{Type: "git", Path: "values/prod.yaml"}},
			}},
		},
		Status: ClusterBaselineStatus{
			TargetedClusters: []string{"prod-east"},
			Applications: []ClusterBaselineApplicationStatus{{
				Name:             "baseline-ingress",
				SyncStatus:       "Synced",
				Health:           "Healthy",
				ApplicationCount: 1,
				ChildApplications: []ArgoApplicationStatus{{
					Name:       "ingress-prod-east",
					SyncStatus: "Synced",
					Resources:  []ArgoApplicationResourceStatus{{Kind: "Deployment", Name: "ingress"}},
				}},
			}},
			Conditions: []metav1.Condition{{
				Type:   "Ready",
				Status: metav1.ConditionTrue,
			}},
		},
	}
	baselineCopy := baseline.DeepCopy()
	baselineCopy.Spec.ClusterSelector.MatchLabels["env"] = "stage"
	baselineCopy.Spec.ClusterSelector.ClusterRefs[0] = "stage-west"
	baselineCopy.Spec.Bundles[0].Values["replicas"] = "1"
	baselineCopy.Spec.Bundles[0].ValuesFrom[0].Path = "values/stage.yaml"
	*baselineCopy.Spec.Bundles[0].Enabled = false
	baselineCopy.Status.TargetedClusters[0] = "stage-west"
	baselineCopy.Status.Applications[0].SyncStatus = "OutOfSync"
	baselineCopy.Status.Applications[0].ChildApplications[0].Resources[0].Name = "mutated"
	baselineCopy.Status.Conditions[0].Status = metav1.ConditionFalse
	if baseline.Spec.ClusterSelector.MatchLabels["env"] != "prod" ||
		baseline.Spec.ClusterSelector.ClusterRefs[0] != "prod-east" ||
		baseline.Spec.Bundles[0].Values["replicas"] != "2" ||
		baseline.Spec.Bundles[0].ValuesFrom[0].Path != "values/prod.yaml" ||
		!*baseline.Spec.Bundles[0].Enabled ||
		baseline.Status.TargetedClusters[0] != "prod-east" ||
		baseline.Status.Applications[0].SyncStatus != "Synced" ||
		baseline.Status.Applications[0].ChildApplications[0].Resources[0].Name != "ingress" ||
		baseline.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("ClusterBaseline DeepCopy aliased nested fields: %+v", baseline)
	}

	bundle := ComponentBundle{
		Spec: ComponentBundleSpec{
			DefaultNamespace: "monitoring",
			Source: ComponentBundleSourceSpec{
				Type:       "helm",
				SecretRefs: []ComponentBundleSecretRef{{Name: "repo-creds", Key: "token"}},
			},
			CapabilityRequirements: []ComponentBundleRequirement{{Feature: "metrics"}},
			HealthChecks:           []ComponentBundleHealthCheck{{Type: "argocd", Path: "applications"}},
			Versions: []ComponentBundleVersionSpec{{
				Version: "2.0.0",
				Source: ComponentBundleSourceSpec{
					Type:       "helm",
					SecretRefs: []ComponentBundleSecretRef{{Name: "repo-creds-v2", Key: "token"}},
				},
				CapabilityRequirements: []ComponentBundleRequirement{{Feature: "logs"}},
				HealthChecks:           []ComponentBundleHealthCheck{{Type: "http", Path: "healthz"}},
			}},
		},
		Status: ComponentBundleStatus{
			AvailableVersions: []string{"1.0.0", "2.0.0"},
		},
	}
	bundleCopy := bundle.DeepCopy()
	bundleCopy.Spec.Source.SecretRefs[0].Name = "other-creds"
	bundleCopy.Spec.CapabilityRequirements[0].Feature = "logs"
	bundleCopy.Spec.HealthChecks[0].Type = "http"
	bundleCopy.Spec.Versions[0].Source.SecretRefs[0].Name = "other-version-creds"
	bundleCopy.Spec.Versions[0].CapabilityRequirements[0].Feature = "tracing"
	bundleCopy.Spec.Versions[0].HealthChecks[0].Type = "prometheus"
	bundleCopy.Status.AvailableVersions[0] = "9.9.9"
	if bundle.Spec.Source.SecretRefs[0].Name != "repo-creds" ||
		bundle.Spec.CapabilityRequirements[0].Feature != "metrics" ||
		bundle.Spec.HealthChecks[0].Type != "argocd" ||
		bundle.Spec.Versions[0].Source.SecretRefs[0].Name != "repo-creds-v2" ||
		bundle.Spec.Versions[0].CapabilityRequirements[0].Feature != "logs" ||
		bundle.Spec.Versions[0].HealthChecks[0].Type != "http" ||
		bundle.Status.AvailableVersions[0] != "1.0.0" {
		t.Fatalf("ComponentBundle DeepCopy aliased nested fields: %+v", bundle)
	}

	profile := AgentProfile{
		Spec: AgentProfileSpec{
			NamespaceScope: []string{"team-a"},
			Capabilities:   map[string]bool{"exec": true},
			AllowedRules: []AgentProfilePolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list"},
			}},
			HostAccess:    AgentProfileHostAccessSpec{HostPathPrefixes: []string{"/var/log"}},
			NetworkEgress: AgentProfileNetworkEgressSpec{Mode: "custom", AllowedHosts: []string{"registry.example.test"}},
			Install:       AgentProfileInstallSpec{PodLabels: map[string]string{"tier": "agent"}},
		},
		Status: AgentProfileStatus{EffectiveRBAC: []string{"get pods"}},
	}
	profileCopy := profile.DeepCopy()
	profileCopy.Spec.NamespaceScope[0] = "team-b"
	profileCopy.Spec.Capabilities["exec"] = false
	profileCopy.Spec.AllowedRules[0].Resources[0] = "deployments"
	profileCopy.Spec.AllowedRules[0].Verbs[0] = "watch"
	profileCopy.Spec.HostAccess.HostPathPrefixes[0] = "/host"
	profileCopy.Spec.NetworkEgress.AllowedHosts[0] = "other.example.test"
	profileCopy.Spec.Install.PodLabels["tier"] = "custom"
	profileCopy.Status.EffectiveRBAC[0] = "custom"
	if profile.Spec.NamespaceScope[0] != "team-a" ||
		!profile.Spec.Capabilities["exec"] ||
		profile.Spec.AllowedRules[0].Resources[0] != "pods" ||
		profile.Spec.AllowedRules[0].Verbs[0] != "get" ||
		profile.Spec.HostAccess.HostPathPrefixes[0] != "/var/log" ||
		profile.Spec.NetworkEgress.AllowedHosts[0] != "registry.example.test" ||
		profile.Spec.Install.PodLabels["tier"] != "agent" ||
		profile.Status.EffectiveRBAC[0] != "get pods" {
		t.Fatalf("AgentProfile DeepCopy aliased nested fields: %+v", profile)
	}

	target := GitOpsTarget{
		Spec: GitOpsTargetSpec{
			Selector:        GitOpsTargetSelectorSpec{MatchLabels: map[string]string{"env": "prod"}, ClusterRefs: []string{"prod-east"}},
			ProjectSelector: LabelSelectorSpec{MatchLabels: map[string]string{"team": "platform"}, ClusterRefs: []string{"platform-project"}},
			BundleRef:       GitOpsTargetBundleRef{Name: "observability", Version: "1.0.0"},
			ApplicationSet:  GitOpsTargetApplicationSetSpec{Parameters: map[string]string{"wave": "1"}},
			SyncWindows:     []GitOpsTargetSyncWindowSpec{{Kind: "allow", Clusters: []string{"prod-east"}}},
		},
		Status: GitOpsTargetStatus{
			MatchedClusters:  []string{"prod-east"},
			SyncStatus:       "Synced",
			Health:           "Healthy",
			ApplicationCount: 1,
			Applications: []ArgoApplicationStatus{{
				Name:       "observability-prod-east",
				SyncStatus: "Synced",
				Resources:  []ArgoApplicationResourceStatus{{Kind: "Deployment", Name: "prometheus"}},
			}},
		},
	}
	targetCopy := target.DeepCopy()
	targetCopy.Spec.Selector.MatchLabels["env"] = "stage"
	targetCopy.Spec.Selector.ClusterRefs[0] = "stage-west"
	targetCopy.Spec.ProjectSelector.MatchLabels["team"] = "billing"
	targetCopy.Spec.ProjectSelector.ClusterRefs[0] = "billing-project"
	targetCopy.Spec.ApplicationSet.Parameters["wave"] = "2"
	targetCopy.Spec.SyncWindows[0].Clusters[0] = "stage-west"
	targetCopy.Status.MatchedClusters[0] = "stage-west"
	targetCopy.Status.Applications[0].Resources[0].Name = "mutated"
	if target.Spec.Selector.MatchLabels["env"] != "prod" ||
		target.Spec.Selector.ClusterRefs[0] != "prod-east" ||
		target.Spec.ProjectSelector.MatchLabels["team"] != "platform" ||
		target.Spec.ProjectSelector.ClusterRefs[0] != "platform-project" ||
		target.Spec.ApplicationSet.Parameters["wave"] != "1" ||
		target.Spec.SyncWindows[0].Clusters[0] != "prod-east" ||
		target.Status.MatchedClusters[0] != "prod-east" ||
		target.Status.Applications[0].Resources[0].Name != "prometheus" {
		t.Fatalf("GitOpsTarget DeepCopy aliased nested fields: %+v", target)
	}
}
