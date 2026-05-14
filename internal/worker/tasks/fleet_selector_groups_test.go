package tasks

// T6.066 — matchGroupIDs branch tests.

import (
	"testing"

	"github.com/google/uuid"
)

func TestEvaluateFleetSelector_MatchGroupIDs(t *testing.T) {
	a := FleetClusterCandidate{ID: uuid.New(), Name: "alpha", GroupIDs: []string{"g1", "g2"}}
	b := FleetClusterCandidate{ID: uuid.New(), Name: "bravo", GroupIDs: []string{"g3"}}
	c := FleetClusterCandidate{ID: uuid.New(), Name: "ceci"} // no groups

	got := EvaluateFleetSelector(
		FleetSelector{MatchGroupIDs: []string{"g2", "g3"}},
		[]FleetClusterCandidate{a, b, c},
	)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (alpha, bravo), got %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	want := map[string]bool{"alpha": true, "bravo": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected cluster %q in result", n)
		}
	}
}

func TestEvaluateFleetSelector_GroupAndLabelsAreAND(t *testing.T) {
	a := FleetClusterCandidate{Name: "a", Labels: map[string]string{"tier": "prod"}, GroupIDs: []string{"g1"}}
	b := FleetClusterCandidate{Name: "b", Labels: map[string]string{"tier": "dev"}, GroupIDs: []string{"g1"}}
	c := FleetClusterCandidate{Name: "c", Labels: map[string]string{"tier": "prod"}, GroupIDs: []string{"g2"}}
	got := EvaluateFleetSelector(
		FleetSelector{
			MatchLabels:   map[string]string{"tier": "prod"},
			MatchGroupIDs: []string{"g1"},
		},
		[]FleetClusterCandidate{a, b, c},
	)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("expected only [a], got %+v", got)
	}
}

func TestFleetSelector_IsEmpty_GroupOnly(t *testing.T) {
	s := FleetSelector{MatchGroupIDs: []string{"g1"}}
	if s.IsEmpty() {
		t.Error("selector with groupIDs should not be empty")
	}
}
