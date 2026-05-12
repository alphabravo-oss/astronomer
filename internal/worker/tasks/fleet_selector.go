// Fleet operation selector evaluator (migration 056).
//
// The orchestrator evaluates this exactly once at the pending → running
// transition; subsequent ticks read the persisted target list. The
// shape mirrors Kubernetes label selectors so an operator who already
// understands `kubectl get -l tier=prod` can author a fleet operation
// without learning a second predicate language.
//
// Supported in this slice:
//   - matchLabels    : { tier: "prod", region: "us-east" } — AND of equals
//   - matchExpressions: [{ key, operator: In|NotIn|Exists|DoesNotExist, values }]
//
// matchLabels is the workhorse — every example in the migration doc
// uses it. matchExpressions adds the In/NotIn/Exists set operators for
// the "all staging OR canary, but not us-west-3" kind of query. We
// deliberately do NOT support arbitrary JSONB predicates (jq, CEL) —
// the API surface stays narrow and predictable.
//
// An empty selector ({}) matches NO clusters. That's a load-bearing
// safety property: an operator who forgets to fill in the selector
// MUST NOT accidentally enqueue a drain across the entire fleet. The
// handler enforces a non-empty selector at create time as defense in
// depth.

package tasks

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// FleetSelector is the parsed shape of fleet_operations.selector.
// Both fields are optional individually; if both are empty the
// selector matches nothing.
type FleetSelector struct {
	MatchLabels      map[string]string         `json:"matchLabels,omitempty"`
	MatchExpressions []FleetSelectorExpression `json:"matchExpressions,omitempty"`
}

// FleetSelectorExpression is one row of the matchExpressions list.
// Operator is one of "In", "NotIn", "Exists", "DoesNotExist" — same
// alphabet as k8s LabelSelectorRequirement.
type FleetSelectorExpression struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// IsEmpty returns true when the selector has neither matchLabels nor
// matchExpressions set. Used by the handler validator to reject an
// empty selector at create time (so an operator can never accidentally
// fanout across the whole fleet).
func (s FleetSelector) IsEmpty() bool {
	return len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0
}

// ParseFleetSelector decodes the JSONB blob into the typed struct.
// Returns an empty selector when the blob is nil/empty — the caller
// then decides whether to treat that as a validation error.
func ParseFleetSelector(raw json.RawMessage) (FleetSelector, error) {
	var s FleetSelector
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return FleetSelector{}, fmt.Errorf("parse selector: %w", err)
	}
	return s, nil
}

// FleetClusterCandidate is the minimal projection the selector
// evaluator needs. Mirrors ListClustersForSelectorEvaluationRow but
// kept locally so the selector package doesn't import sqlc (which
// would create a cycle for callers in the handler package).
type FleetClusterCandidate struct {
	ID     uuid.UUID
	Name   string
	Labels map[string]string
}

// EvaluateFleetSelector returns the cluster IDs that match the
// selector. An empty selector matches NO clusters (load-bearing —
// see package doc). Order is preserved from the input slice so the
// orchestrator can rely on a deterministic dispatch order matching
// the SQL `ORDER BY name ASC` projection.
func EvaluateFleetSelector(sel FleetSelector, candidates []FleetClusterCandidate) []FleetClusterCandidate {
	if sel.IsEmpty() {
		return nil
	}
	out := make([]FleetClusterCandidate, 0, len(candidates))
	for _, c := range candidates {
		if matchesSelector(sel, c.Labels) {
			out = append(out, c)
		}
	}
	return out
}

// matchesSelector is the per-cluster predicate. Implemented as the
// AND of every matchLabel and every matchExpression — same semantics
// as the upstream Kubernetes selector.
func matchesSelector(sel FleetSelector, labels map[string]string) bool {
	for k, v := range sel.MatchLabels {
		if labels[k] != v {
			return false
		}
	}
	for _, expr := range sel.MatchExpressions {
		if !matchesExpression(expr, labels) {
			return false
		}
	}
	return true
}

// matchesExpression evaluates one matchExpressions entry. Unknown
// operators evaluate to false (defensive — an operator who hand-edits
// the JSONB to "EqualsIgnoreCase" gets zero matches rather than a
// silent fanout).
func matchesExpression(expr FleetSelectorExpression, labels map[string]string) bool {
	val, present := labels[expr.Key]
	switch expr.Operator {
	case "In":
		if !present {
			return false
		}
		for _, v := range expr.Values {
			if v == val {
				return true
			}
		}
		return false
	case "NotIn":
		if !present {
			// k8s semantics: NotIn matches when the key is absent.
			return true
		}
		for _, v := range expr.Values {
			if v == val {
				return false
			}
		}
		return true
	case "Exists":
		return present
	case "DoesNotExist":
		return !present
	default:
		return false
	}
}

// DecodeClusterLabels parses a clusters.labels JSONB blob into a
// string->string map. Tolerates the legacy non-string-valued labels
// (drops them) so a stray int in some old row doesn't break selector
// evaluation across the whole fleet.
func DecodeClusterLabels(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var any map[string]any
	if err := json.Unmarshal(raw, &any); err != nil {
		return out
	}
	for k, v := range any {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
