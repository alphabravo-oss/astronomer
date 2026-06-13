package server

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
)

func TestClusterAnnotationsWithAdoptionPolicy(t *testing.T) {
	got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
		Annotations: map[string]string{"existing": "kept"},
		AdoptionPolicy: crd.ClusterAdoptionPolicySpec{
			Mode:                   "auto",
			AllowedManagementModes: []string{"helm", "", "argocd"},
		},
	})

	if got["existing"] != "kept" {
		t.Fatalf("existing annotation lost: %+v", got)
	}
	if got["management.astronomer.io/adoption-policy-mode"] != "auto" {
		t.Fatalf("adoption policy mode = %q", got["management.astronomer.io/adoption-policy-mode"])
	}
	if got["management.astronomer.io/allowed-management-modes"] != "argocd,helm" {
		t.Fatalf("allowed management modes = %q", got["management.astronomer.io/allowed-management-modes"])
	}
}
