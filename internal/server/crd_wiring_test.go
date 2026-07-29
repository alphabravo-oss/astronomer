package server

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/crd"
)

func TestClusterAnnotationsWithAdoptionPolicy(t *testing.T) {
	got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
		Annotations: map[string]string{"existing": "kept"},
		AdoptionPolicy: crd.ClusterAdoptionPolicySpec{
			Mode:                   "auto",
			AllowedManagementModes: []string{"helm", "", "argocd"},
		},
	}, nil)

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

// TestClusterCRCannotWriteImpersonationMode pins the CRD path as a NON-writer of
// the downstream-impersonation flag.
//
// PRE-FIX: clusterAnnotationsWithAgentProfile copied spec.Annotations verbatim
// and UpdateCluster replaces clusters.annotations wholesale, so the CR was a
// second, ungated writer of the key that the REST path gates on superuser plus
// the agent capability advertisement — and it also silently cleared a
// superuser-set mode on the next sync.
func TestClusterCRCannotWriteImpersonationMode(t *testing.T) {
	t.Run("a CR cannot raise the mode", func(t *testing.T) {
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{
				"existing":              "kept",
				callerid.ModeAnnotation: string(callerid.ModeEnforce),
			},
		}, json.RawMessage(`{}`))

		if _, present := got[callerid.ModeAnnotation]; present {
			t.Fatalf("CR raised the impersonation mode: %+v", got)
		}
		if got["existing"] != "kept" {
			t.Fatalf("unrelated annotation lost: %+v", got)
		}
	})

	t.Run("a CR cannot clear a superuser-set mode", func(t *testing.T) {
		stored := json.RawMessage(`{"` + callerid.ModeAnnotation + `":"attribute"}`)
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{"existing": "kept"},
		}, stored)

		if got[callerid.ModeAnnotation] != string(callerid.ModeAttribute) {
			t.Fatalf("stored mode not preserved across CRD sync: %+v", got)
		}
	})

	t.Run("a CR cannot lower a superuser-set mode", func(t *testing.T) {
		stored := json.RawMessage(`{"` + callerid.ModeAnnotation + `":"enforce"}`)
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{callerid.ModeAnnotation: string(callerid.ModeOff)},
		}, stored)

		if got[callerid.ModeAnnotation] != string(callerid.ModeEnforce) {
			t.Fatalf("CR lowered the impersonation mode: %+v", got)
		}
	})
}
