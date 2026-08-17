package rollout

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
)

// BuildCohorts freezes strategy membership and order. Input ordering never
// affects output. Partition overlap/unmatched membership is rejected because a
// silent first-match rule can unexpectedly promote a cluster to a riskier wave.
func BuildCohorts(strategy model.RolloutStrategy, candidates []placement.Candidate) ([]Cohort, []PlannedCluster, error) {
	if err := strategy.Validate(); err != nil {
		return nil, nil, &Error{Code: CodeInvalidInput, Field: "strategy", Cause: err}
	}
	ordered, byID, err := canonicalCandidates(candidates, strategy.ShuffleSeed)
	if err != nil {
		return nil, nil, err
	}
	if len(ordered) == 0 {
		return nil, nil, fail(CodeNoClusters, "clusters", "rollout selection is empty")
	}

	var cohorts []Cohort
	switch strategy.Type {
	case model.StrategyAllAtOnce:
		cohorts = []Cohort{newCohort(0, "all", ids(ordered), false, 0)}
	case model.StrategyRolling:
		cohorts = batchCohorts(ordered, int(strategy.MaxConcurrent), 0, "rolling")
	case model.StrategyCanary:
		canary, rest, buildErr := splitCanary(*strategy.Canary, ordered, byID, strategy.ShuffleSeed)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		cohorts = append(cohorts, newCohort(0, "canary", ids(canary), false, strategy.Canary.Soak))
		remaining := batchCohorts(rest, int(strategy.MaxConcurrent), 1, "rolling")
		if len(remaining) != 0 && strategy.Canary.ApprovalAfterCanary {
			remaining[0].ApprovalRequired = true
		}
		cohorts = append(cohorts, remaining...)
	case model.StrategyPartitioned:
		members := make([][]placement.Candidate, len(strategy.Partitions))
		for _, candidate := range ordered {
			matched := -1
			for index, partition := range strategy.Partitions {
				if partitionMatches(partition.Selector, candidate) {
					if matched != -1 {
						return nil, nil, fail(CodeInvalidCohorts, "partitions", fmt.Sprintf("cluster %s matches multiple partitions", candidate.ID))
					}
					matched = index
				}
			}
			if matched == -1 {
				return nil, nil, fail(CodeInvalidCohorts, "partitions", fmt.Sprintf("cluster %s matches no partition", candidate.ID))
			}
			members[matched] = append(members[matched], candidate)
		}
		for index, partition := range strategy.Partitions {
			if len(members[index]) == 0 {
				return nil, nil, fail(CodeInvalidCohorts, "partitions", fmt.Sprintf("partition %q is empty", partition.Name))
			}
			cohorts = append(cohorts, newCohort(index, partition.Name, ids(members[index]), partition.ApprovalRequired, partition.Soak))
		}
	default:
		return nil, nil, fail(CodeInvalidInput, "strategy.type", "unsupported strategy")
	}

	planned := make([]PlannedCluster, 0, len(ordered))
	order := 0
	for _, cohort := range cohorts {
		for _, clusterID := range cohort.ClusterIDs {
			planned = append(planned, PlannedCluster{ClusterID: clusterID, Cohort: cohort.Index, Order: order})
			order++
		}
	}
	return cohorts, planned, nil
}

func canonicalCandidates(input []placement.Candidate, seed string) ([]placement.Candidate, map[uuid.UUID]placement.Candidate, error) {
	result := append([]placement.Candidate(nil), input...)
	byID := make(map[uuid.UUID]placement.Candidate, len(input))
	for _, candidate := range result {
		if candidate.ID == uuid.Nil {
			return nil, nil, fail(CodeInvalidInput, "clusters", "contains a zero UUID")
		}
		if _, exists := byID[candidate.ID]; exists {
			return nil, nil, fail(CodeInvalidInput, "clusters", fmt.Sprintf("contains duplicate %s", candidate.ID))
		}
		byID[candidate.ID] = candidate
	}
	sort.Slice(result, func(i, j int) bool { return lessCandidate(seed, result[i].ID, result[j].ID) })
	return result, byID, nil
}

func lessCandidate(seed string, left, right uuid.UUID) bool {
	if seed == "" {
		return bytes.Compare(left[:], right[:]) < 0
	}
	leftHash := stableHash(seed, left)
	rightHash := stableHash(seed, right)
	if comparison := bytes.Compare(leftHash[:], rightHash[:]); comparison != 0 {
		return comparison < 0
	}
	return bytes.Compare(left[:], right[:]) < 0
}

func stableHash(seed string, id uuid.UUID) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(seed))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(id[:])
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func splitCanary(spec model.CanarySpec, ordered []placement.Candidate, byID map[uuid.UUID]placement.Candidate, seed string) ([]placement.Candidate, []placement.Candidate, error) {
	canarySet := make(map[uuid.UUID]struct{})
	if len(spec.ClusterIDs) != 0 {
		for _, id := range spec.ClusterIDs {
			if _, exists := byID[id]; !exists {
				return nil, nil, fail(CodeInvalidCohorts, "canary.cluster_ids", fmt.Sprintf("cluster %s is not in the frozen placement", id))
			}
			canarySet[id] = struct{}{}
		}
	} else {
		count := amountSize(spec.Size, len(ordered), true)
		if count > len(ordered) {
			return nil, nil, fail(CodeInvalidCohorts, "canary.size", "exceeds selected cluster count")
		}
		ranked := append([]placement.Candidate(nil), ordered...)
		canarySeed := seed
		if canarySeed == "" {
			canarySeed = "astronomer-canary-v1"
		} else {
			canarySeed = "astronomer-canary-v1\x00" + canarySeed
		}
		sort.Slice(ranked, func(i, j int) bool { return lessCandidate(canarySeed, ranked[i].ID, ranked[j].ID) })
		for _, candidate := range ranked[:count] {
			canarySet[candidate.ID] = struct{}{}
		}
	}
	canary := make([]placement.Candidate, 0, len(canarySet))
	rest := make([]placement.Candidate, 0, len(ordered)-len(canarySet))
	for _, candidate := range ordered {
		if _, selected := canarySet[candidate.ID]; selected {
			canary = append(canary, candidate)
		} else {
			rest = append(rest, candidate)
		}
	}
	return canary, rest, nil
}

func amountSize(amount model.Amount, total int, roundUp bool) int {
	if amount.Type == model.AmountCount {
		return int(amount.Value)
	}
	value := uint64(total) * uint64(amount.Value)
	if roundUp && value%100 != 0 {
		value += 100
	}
	return int(value / 100)
}

func batchCohorts(candidates []placement.Candidate, size, offset int, prefix string) []Cohort {
	if len(candidates) == 0 {
		return nil
	}
	result := make([]Cohort, 0, (len(candidates)+size-1)/size)
	for start := 0; start < len(candidates); start += size {
		end := min(start+size, len(candidates))
		index := offset + len(result)
		result = append(result, newCohort(index, fmt.Sprintf("%s-%04d", prefix, index), ids(candidates[start:end]), false, 0))
	}
	return result
}

func newCohort(index int, name string, clusterIDs []uuid.UUID, approval bool, soak model.Duration) Cohort {
	return Cohort{Index: index, Name: name, ClusterIDs: clusterIDs, ApprovalRequired: approval, SoakAfter: soak}
}

func ids(candidates []placement.Candidate) []uuid.UUID {
	result := make([]uuid.UUID, len(candidates))
	for index := range candidates {
		result[index] = candidates[index].ID
	}
	return result
}

func partitionMatches(selector model.Placement, candidate placement.Candidate) bool {
	if len(selector.ClusterGroupIDs) != 0 {
		groups := make(map[uuid.UUID]struct{}, len(candidate.GroupIDs))
		for _, id := range candidate.GroupIDs {
			groups[id] = struct{}{}
		}
		matched := false
		for _, id := range selector.ClusterGroupIDs {
			if _, exists := groups[id]; exists {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for key, value := range selector.MatchLabels {
		if candidate.Labels[key] != value {
			return false
		}
	}
	for _, expression := range selector.MatchExpressions {
		value, exists := candidate.Labels[expression.Key]
		contains := false
		for _, allowed := range expression.Values {
			if value == allowed {
				contains = true
				break
			}
		}
		switch expression.Operator {
		case model.OperatorIn:
			if !exists || !contains {
				return false
			}
		case model.OperatorNotIn:
			if exists && contains {
				return false
			}
		case model.OperatorExists:
			if !exists {
				return false
			}
		case model.OperatorDoesNotExist:
			if exists {
				return false
			}
		}
	}
	return true
}
