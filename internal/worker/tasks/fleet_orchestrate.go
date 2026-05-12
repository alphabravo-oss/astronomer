// Fleet orchestration selector evaluation.
//
// Sprint 056 introduced fleet operations: an orchestrator that runs a
// tool / drift-check / arbitrary task across a target set of clusters,
// where the target set is described by a JSONB selector. Today the
// selector reads matchLabels / matchExpressions (k8s-shaped label
// selector semantics over the clusters.labels JSONB).
//
// Sprint 066 adds a `group_id` branch: when the selector carries a
// group_id, the orchestrator expands it via the cluster_groups recursive
// CTE (ListClustersInGroupTree) and uses that as the target set. If
// BOTH a group_id AND a label selector are present, the resulting set
// is the INTERSECTION (group_id narrows the label match) — i.e. the
// operator can say "everything labelled tier=frontend in the prod-us
// subtree".
//
// This file owns the pure selector-evaluation logic so the orchestrator
// task itself stays free of database wiring; the queries adapter is
// supplied via a tiny interface that *sqlc.Queries satisfies for free.

package tasks

import (
	"context"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// FleetSelectorSpec is the wire shape for the spec.selector JSONB on a
// fleet_operations row. All fields are optional; the evaluator returns
// the empty set if every field is empty.
type FleetSelectorSpec struct {
	// MatchLabels is the simple k=v equality selector against
	// clusters.labels. Multiple entries AND together.
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	// MatchExpressions is the set-style selector. Only "In" / "NotIn" /
	// "Exists" / "DoesNotExist" are recognized — anything else is
	// silently ignored by the evaluator (the orchestrator's validator is
	// the canonical enforcement site).
	MatchExpressions []FleetSelectorRequirement `json:"matchExpressions,omitempty"`
	// GroupID, when set, expands to all clusters in the cluster_group
	// tree rooted at this id. Intersects with the label selector when
	// both are present.
	GroupID string `json:"group_id,omitempty"`
}

// FleetSelectorRequirement is the set-style selector entry used inside
// FleetSelectorSpec.MatchExpressions.
type FleetSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// FleetSelectorClusterRepo is the database surface needed to expand a
// selector into a concrete []uuid.UUID. *sqlc.Queries satisfies this
// for free.
type FleetSelectorClusterRepo interface {
	ListClustersInGroupTree(ctx context.Context, rootID uuid.UUID) ([]sqlc.ClusterInGroupRow, error)
}

// FleetSelectorLabelRepo is the surface for resolving the label-side of
// the selector. The production implementation lives elsewhere (sprint
// 056); the tests in this package supply a narrow fake. This separation
// keeps the group_id branch testable without dragging in the full label
// evaluator's wiring.
type FleetSelectorLabelRepo interface {
	ListClustersMatchingLabels(ctx context.Context, matchLabels map[string]string, matchExpressions []FleetSelectorRequirement) ([]uuid.UUID, error)
}

// ResolveFleetSelector returns the union of cluster IDs that match the
// supplied selector. The semantics:
//
//   - If GroupID is set AND labels are set -> intersect.
//   - If only GroupID is set -> ListClustersInGroupTree.
//   - If only labels are set -> ListClustersMatchingLabels.
//   - If neither is set -> empty slice (orchestrator should treat that
//     as a 400 / no-op; this function does NOT default to "every
//     cluster" because that would be a footgun.)
//
// Either repo may be nil when the corresponding selector branch is
// unused. If GroupID is set but groups is nil, the function returns nil
// (selector is unresolvable). Likewise for labels.
func ResolveFleetSelector(
	ctx context.Context,
	spec FleetSelectorSpec,
	groups FleetSelectorClusterRepo,
	labels FleetSelectorLabelRepo,
) ([]uuid.UUID, error) {
	hasGroup := spec.GroupID != ""
	hasLabel := len(spec.MatchLabels) > 0 || len(spec.MatchExpressions) > 0

	if !hasGroup && !hasLabel {
		return []uuid.UUID{}, nil
	}

	var groupSet []uuid.UUID
	if hasGroup {
		if groups == nil {
			return nil, nil
		}
		gid, err := uuid.Parse(spec.GroupID)
		if err != nil {
			return nil, err
		}
		rows, err := groups.ListClustersInGroupTree(ctx, gid)
		if err != nil {
			return nil, err
		}
		groupSet = make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			groupSet = append(groupSet, r.ID)
		}
	}

	var labelSet []uuid.UUID
	if hasLabel {
		if labels == nil {
			return nil, nil
		}
		ls, err := labels.ListClustersMatchingLabels(ctx, spec.MatchLabels, spec.MatchExpressions)
		if err != nil {
			return nil, err
		}
		labelSet = ls
	}

	switch {
	case hasGroup && hasLabel:
		return intersectUUIDs(groupSet, labelSet), nil
	case hasGroup:
		return groupSet, nil
	default:
		return labelSet, nil
	}
}

// intersectUUIDs returns the intersection of two id slices, preserving
// the order of `a`. Hot-path simple — selector sets are normally <100
// clusters so a map-and-walk is fine.
func intersectUUIDs(a, b []uuid.UUID) []uuid.UUID {
	if len(a) == 0 || len(b) == 0 {
		return []uuid.UUID{}
	}
	have := make(map[uuid.UUID]struct{}, len(b))
	for _, id := range b {
		have[id] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(a))
	for _, id := range a {
		if _, ok := have[id]; ok {
			out = append(out, id)
		}
	}
	return out
}
