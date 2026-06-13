package crd

import "testing"

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
