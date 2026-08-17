package crd

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestSchemeContainsOnlyRetainedManagementKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil { t.Fatal(err) }
	for _, kind := range []string{"Cluster", "Project", "AgentProfile"} {
		if _, err := scheme.New(GroupVersion.WithKind(kind)); err != nil { t.Fatalf("%s not registered: %v", kind, err) }
	}
	for _, removed := range []string{"ClusterBaseline", "ComponentBundle", "GitOpsTarget"} {
		if _, err := scheme.New(GroupVersion.WithKind(removed)); err == nil { t.Fatalf("removed kind %s is still registered", removed) }
	}
}

func TestClusterDeepCopyDoesNotAliasCollections(t *testing.T) {
	original := &Cluster{Spec: ClusterSpec{Labels: map[string]string{"a":"b"}, ProjectRefs: []string{"p"}, AdoptionPolicy: ClusterAdoptionPolicySpec{AllowedManagementModes: []string{"flux"}}}}
	copy := original.DeepCopy()
	copy.Spec.Labels["a"] = "changed"
	copy.Spec.ProjectRefs[0] = "changed"
	copy.Spec.AdoptionPolicy.AllowedManagementModes[0] = "manual"
	if original.Spec.Labels["a"] != "b" || original.Spec.ProjectRefs[0] != "p" || original.Spec.AdoptionPolicy.AllowedManagementModes[0] != "flux" { t.Fatal("deep copy aliases source") }
}
