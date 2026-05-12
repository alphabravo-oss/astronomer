package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeGroupRepo struct {
	subtree []sqlc.ClusterInGroupRow
}

func (f *fakeGroupRepo) ListClustersInGroupTree(_ context.Context, _ uuid.UUID) ([]sqlc.ClusterInGroupRow, error) {
	return f.subtree, nil
}

type fakeLabelRepo struct {
	matches []uuid.UUID
}

func (f *fakeLabelRepo) ListClustersMatchingLabels(_ context.Context, _ map[string]string, _ []FleetSelectorRequirement) ([]uuid.UUID, error) {
	return f.matches, nil
}

// TestFleetSelector_ResolvesGroupID verifies that a selector with only
// a group_id expands to the full subtree under that group.
func TestFleetSelector_ResolvesGroupID(t *testing.T) {
	groupID := uuid.New()
	c1, c2, c3 := uuid.New(), uuid.New(), uuid.New()
	groups := &fakeGroupRepo{subtree: []sqlc.ClusterInGroupRow{
		{ID: c1, Name: "c1"},
		{ID: c2, Name: "c2"},
		{ID: c3, Name: "c3"},
	}}
	spec := FleetSelectorSpec{GroupID: groupID.String()}
	got, err := ResolveFleetSelector(context.Background(), spec, groups, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 clusters in subtree, got %d", len(got))
	}
}

// TestFleetSelector_IntersectsGroupAndLabels verifies that when both
// group_id AND labels are present the result is the intersection (the
// group narrows the label match).
func TestFleetSelector_IntersectsGroupAndLabels(t *testing.T) {
	groupID := uuid.New()
	c1, c2, c3 := uuid.New(), uuid.New(), uuid.New()
	c4 := uuid.New()
	// Group contains c1, c2, c3.
	groups := &fakeGroupRepo{subtree: []sqlc.ClusterInGroupRow{
		{ID: c1}, {ID: c2}, {ID: c3},
	}}
	// Label selector matches c2, c3, c4. Intersection should be {c2, c3}.
	labels := &fakeLabelRepo{matches: []uuid.UUID{c2, c3, c4}}
	spec := FleetSelectorSpec{
		GroupID:     groupID.String(),
		MatchLabels: map[string]string{"tier": "frontend"},
	}
	got, err := ResolveFleetSelector(context.Background(), spec, groups, labels)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 clusters in intersection, got %d", len(got))
	}
	gotSet := map[uuid.UUID]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	if !gotSet[c2] || !gotSet[c3] {
		t.Errorf("intersection missing c2/c3: got %v", got)
	}
	if gotSet[c1] || gotSet[c4] {
		t.Errorf("intersection should not contain c1/c4: got %v", got)
	}
}

// TestFleetSelector_EmptySpecReturnsEmpty verifies that an empty
// selector (no group_id, no labels) resolves to an empty slice — NOT
// to "every cluster" — so the orchestrator can reject it explicitly
// instead of silently fanning out to the entire fleet.
func TestFleetSelector_EmptySpecReturnsEmpty(t *testing.T) {
	got, err := ResolveFleetSelector(context.Background(), FleetSelectorSpec{}, nil, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d entries", len(got))
	}
}

// TestFleetSelector_LabelOnly verifies that when only labels are set the
// group repo is not consulted (nil-safe).
func TestFleetSelector_LabelOnly(t *testing.T) {
	c1, c2 := uuid.New(), uuid.New()
	labels := &fakeLabelRepo{matches: []uuid.UUID{c1, c2}}
	spec := FleetSelectorSpec{MatchLabels: map[string]string{"env": "staging"}}
	got, err := ResolveFleetSelector(context.Background(), spec, nil, labels)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}
