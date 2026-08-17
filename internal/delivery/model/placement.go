package model

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	MaxPlacementIDs     = 10000
	MaxMatchLabels      = 256
	MaxMatchExpressions = 256
	MaxExpressionValues = 256
)

type LabelOperator string

const (
	OperatorIn           LabelOperator = "In"
	OperatorNotIn        LabelOperator = "NotIn"
	OperatorExists       LabelOperator = "Exists"
	OperatorDoesNotExist LabelOperator = "DoesNotExist"
)

type LabelExpression struct {
	Key      string        `json:"key"`
	Operator LabelOperator `json:"operator"`
	Values   []string      `json:"values,omitempty"`
}

func (e LabelExpression) Validate() error {
	var collector validationCollector
	if errs := utilvalidation.IsQualifiedName(e.Key); len(errs) != 0 {
		collector.add("key", CodeInvalid, "must be a Kubernetes qualified label key")
	}
	switch e.Operator {
	case OperatorIn, OperatorNotIn:
		if len(e.Values) == 0 {
			collector.add("values", CodeRequired, "must not be empty for In or NotIn")
		}
	case OperatorExists, OperatorDoesNotExist:
		if len(e.Values) != 0 {
			collector.add("values", CodeConflict, "must be empty for Exists or DoesNotExist")
		}
	default:
		collector.add("operator", CodeUnsupported, "must be In, NotIn, Exists, or DoesNotExist")
	}
	if len(e.Values) > MaxExpressionValues {
		collector.add("values", CodeLimitExceeded, fmt.Sprintf("must contain at most %d entries", MaxExpressionValues))
	}
	seen := make(map[string]struct{}, len(e.Values))
	for i, value := range e.Values {
		if errs := utilvalidation.IsValidLabelValue(value); len(errs) != 0 {
			collector.add(fmt.Sprintf("values[%d]", i), CodeInvalid, "must be a Kubernetes label value")
		}
		if _, exists := seen[value]; exists {
			collector.add(fmt.Sprintf("values[%d]", i), CodeConflict, "must not contain duplicates")
		}
		seen[value] = struct{}{}
	}
	return collector.err()
}

// Placement is a bounded selector. ProjectIDs restrict scope but are not a
// positive selector on their own. Empty selectors select nothing; selecting all
// requires the explicit AllClusters flag.
type Placement struct {
	ProjectIDs        []uuid.UUID       `json:"project_ids,omitempty"`
	ClusterIDs        []uuid.UUID       `json:"cluster_ids,omitempty"`
	ClusterGroupIDs   []uuid.UUID       `json:"cluster_group_ids,omitempty"`
	MatchLabels       map[string]string `json:"match_labels,omitempty"`
	MatchExpressions  []LabelExpression `json:"match_expressions,omitempty"`
	ExcludeClusterIDs []uuid.UUID       `json:"exclude_cluster_ids,omitempty"`
	AllClusters       bool              `json:"all_clusters"`
}

// IsEmpty reports whether placement has no positive selection mechanism.
// Project restrictions and exclusions cannot turn an empty selector into a
// broad selector.
func (p Placement) IsEmpty() bool {
	return !p.AllClusters && len(p.ClusterIDs) == 0 && len(p.ClusterGroupIDs) == 0 && len(p.MatchLabels) == 0 && len(p.MatchExpressions) == 0
}

func (p Placement) Validate() error {
	var collector validationCollector
	validateUUIDSet(&collector, "project_ids", p.ProjectIDs)
	validateUUIDSet(&collector, "cluster_ids", p.ClusterIDs)
	validateUUIDSet(&collector, "cluster_group_ids", p.ClusterGroupIDs)
	validateUUIDSet(&collector, "exclude_cluster_ids", p.ExcludeClusterIDs)
	if p.AllClusters && (len(p.ClusterIDs) != 0 || len(p.ClusterGroupIDs) != 0 || len(p.MatchLabels) != 0 || len(p.MatchExpressions) != 0) {
		collector.add("all_clusters", CodeConflict, "cannot be combined with cluster, group, or label selectors")
	}
	if len(p.MatchLabels) > MaxMatchLabels {
		collector.add("match_labels", CodeLimitExceeded, fmt.Sprintf("must contain at most %d entries", MaxMatchLabels))
	}
	for _, key := range sortedStringMapKeys(p.MatchLabels) {
		value := p.MatchLabels[key]
		if errs := utilvalidation.IsQualifiedName(key); len(errs) != 0 {
			collector.add("match_labels", CodeInvalid, "contains an invalid Kubernetes label key")
		}
		if errs := utilvalidation.IsValidLabelValue(value); len(errs) != 0 {
			collector.add("match_labels", CodeInvalid, "contains an invalid Kubernetes label value")
		}
	}
	if len(p.MatchExpressions) > MaxMatchExpressions {
		collector.add("match_expressions", CodeLimitExceeded, fmt.Sprintf("must contain at most %d entries", MaxMatchExpressions))
	}
	seenExpressions := make(map[string]struct{}, len(p.MatchExpressions))
	for i, expression := range p.MatchExpressions {
		collector.append(fmt.Sprintf("match_expressions[%d]", i), expression.Validate())
		key := string(expression.Operator) + "\x00" + expression.Key
		if _, exists := seenExpressions[key]; exists {
			collector.add(fmt.Sprintf("match_expressions[%d]", i), CodeConflict, "duplicates an operator/key expression")
		}
		seenExpressions[key] = struct{}{}
	}
	excluded := uuidSet(p.ExcludeClusterIDs)
	for i, id := range p.ClusterIDs {
		if _, exists := excluded[id]; exists {
			collector.add(fmt.Sprintf("cluster_ids[%d]", i), CodeConflict, "cannot also be explicitly excluded")
		}
	}
	return collector.err()
}

// Canonical returns a deep, deterministically ordered copy suitable for
// approval and preview digests.
func (p Placement) Canonical() (Placement, error) {
	if err := p.Validate(); err != nil {
		return Placement{}, err
	}
	canonical := p
	canonical.ProjectIDs = sortedUUIDs(p.ProjectIDs)
	canonical.ClusterIDs = sortedUUIDs(p.ClusterIDs)
	canonical.ClusterGroupIDs = sortedUUIDs(p.ClusterGroupIDs)
	canonical.ExcludeClusterIDs = sortedUUIDs(p.ExcludeClusterIDs)
	canonical.MatchLabels = cloneStringMap(p.MatchLabels)
	canonical.MatchExpressions = append([]LabelExpression(nil), p.MatchExpressions...)
	for i := range canonical.MatchExpressions {
		canonical.MatchExpressions[i].Values = append([]string(nil), canonical.MatchExpressions[i].Values...)
		sort.Strings(canonical.MatchExpressions[i].Values)
	}
	sort.Slice(canonical.MatchExpressions, func(i, j int) bool {
		left, right := canonical.MatchExpressions[i], canonical.MatchExpressions[j]
		if left.Key != right.Key {
			return left.Key < right.Key
		}
		return left.Operator < right.Operator
	})
	return canonical, nil
}

func (p Placement) CanonicalDigest() (Digest, error) {
	canonical, err := p.Canonical()
	if err != nil {
		return "", err
	}
	return CanonicalDigest(canonical)
}

func validateUUIDSet(collector *validationCollector, field string, values []uuid.UUID) {
	if len(values) > MaxPlacementIDs {
		collector.add(field, CodeLimitExceeded, fmt.Sprintf("must contain at most %d entries", MaxPlacementIDs))
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for i, value := range values {
		if value == uuid.Nil {
			collector.add(fmt.Sprintf("%s[%d]", field, i), CodeRequired, "must be a non-zero UUID")
		}
		if _, exists := seen[value]; exists {
			collector.add(fmt.Sprintf("%s[%d]", field, i), CodeConflict, "must not contain duplicates")
		}
		seen[value] = struct{}{}
	}
}

func uuidSet(values []uuid.UUID) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func sortedUUIDs(values []uuid.UUID) []uuid.UUID {
	result := append([]uuid.UUID(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sortedStringMapKeys(input map[string]string) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
