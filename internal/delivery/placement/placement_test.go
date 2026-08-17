package placement

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

var (
	projectA = mustUUID("10000000-0000-0000-0000-000000000001")
	projectB = mustUUID("10000000-0000-0000-0000-000000000002")
	groupA   = mustUUID("20000000-0000-0000-0000-000000000001")
	groupB   = mustUUID("20000000-0000-0000-0000-000000000002")
)

func TestEmptyPlacementSelectsNothing(t *testing.T) {
	t.Parallel()

	request := baseRequest()
	request.Candidates = []Candidate{candidate(1), candidate(2)}
	result, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedCount != 0 || result.ExcludedCount != 2 {
		t.Fatalf("counts = selected %d, excluded %d", result.SelectedCount, result.ExcludedCount)
	}
	for _, decision := range result.Decisions {
		if decision.Reason != ReasonExcludedSelector {
			t.Errorf("reason = %s, want %s", decision.Reason, ReasonExcludedSelector)
		}
	}
}

func TestKubernetesLabelExpressionSemantics(t *testing.T) {
	t.Parallel()

	clusters := []Candidate{
		withLabels(candidate(1), map[string]string{"region": "east"}),
		withLabels(candidate(2), map[string]string{"region": "west"}),
		withLabels(candidate(3), map[string]string{}),
	}
	tests := []struct {
		name       string
		expression model.LabelExpression
		selected   []uuid.UUID
	}{
		{"In", model.LabelExpression{Key: "region", Operator: model.OperatorIn, Values: []string{"east"}}, []uuid.UUID{clusterID(1)}},
		{"NotIn includes missing", model.LabelExpression{Key: "region", Operator: model.OperatorNotIn, Values: []string{"east"}}, []uuid.UUID{clusterID(2), clusterID(3)}},
		{"Exists", model.LabelExpression{Key: "region", Operator: model.OperatorExists}, []uuid.UUID{clusterID(1), clusterID(2)}},
		{"DoesNotExist", model.LabelExpression{Key: "region", Operator: model.OperatorDoesNotExist}, []uuid.UUID{clusterID(3)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := baseRequest()
			request.Candidates = clusters
			request.Placement.MatchExpressions = []model.LabelExpression{test.expression}
			result, err := Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			if got := selectedIDs(result); !slices.Equal(got, test.selected) {
				t.Fatalf("selected = %v, want %v", got, test.selected)
			}
		})
	}
}

func TestGroupAndLabelsAreANDGroupsAreORExplicitIDsAreUnion(t *testing.T) {
	t.Parallel()

	one := withLabels(candidate(1), map[string]string{"tier": "prod"})
	one.GroupIDs = []uuid.UUID{groupA}
	two := withLabels(candidate(2), map[string]string{"tier": "dev"})
	two.GroupIDs = []uuid.UUID{groupB}
	three := withLabels(candidate(3), map[string]string{"tier": "prod"})
	three.GroupIDs = []uuid.UUID{mustUUID("20000000-0000-0000-0000-000000000003")}

	request := baseRequest()
	request.Candidates = []Candidate{three, two, one}
	request.Placement = model.Placement{
		ClusterIDs: []uuid.UUID{two.ID}, ClusterGroupIDs: []uuid.UUID{groupA, groupB},
		MatchLabels: map[string]string{"tier": "prod"},
	}
	result, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := selectedIDs(result), []uuid.UUID{one.ID, two.ID}; !slices.Equal(got, want) {
		t.Fatalf("selected = %v, want %v", got, want)
	}
	if result.Decisions[0].MatchReasons[0] != MatchClusterGroup || result.Decisions[1].MatchReasons[0] != MatchExplicitCluster {
		t.Fatalf("match reasons = %+v", result.Decisions)
	}
}

func TestDispositionFiltersAndAuthorizationNonDisclosure(t *testing.T) {
	t.Parallel()

	selected := candidate(1)
	excluded := candidate(2)
	decommissioning := candidate(3)
	decommissioning.Decommissioning = true
	disconnected := candidate(4)
	disconnected.Connected = false
	incompatible := candidate(5)
	incompatible.Compatibility = CompatibilityIncompatible
	incompatible.CompatibilityReason = "kubernetes_too_old"
	missing := candidate(6)
	missing.Capabilities = map[string]string{}
	unauthorized := candidate(7)
	unauthorized.ProjectID = projectB
	hiddenUnauthorized := candidate(8)
	hiddenUnauthorized.ProjectID = projectB
	unknownID := clusterID(9)

	request := baseRequest()
	request.Candidates = []Candidate{selected, excluded, decommissioning, disconnected, incompatible, missing, unauthorized, hiddenUnauthorized}
	request.Placement = model.Placement{
		ClusterIDs:        []uuid.UUID{selected.ID, decommissioning.ID, disconnected.ID, incompatible.ID, missing.ID, unauthorized.ID, unknownID},
		ExcludeClusterIDs: []uuid.UUID{excluded.ID},
		MatchLabels:       map[string]string{"never": "matches"},
	}
	request.RequiredCapabilities = []model.CapabilityRequirement{{Name: "delivery.astronomer.io/helm", Constraint: ">=2.0.0"}}
	result, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]DecisionReason{
		selected.ID: ReasonSelected, excluded.ID: ReasonExcludedExplicitly,
		decommissioning.ID: ReasonDecommissioning, disconnected.ID: ReasonDisconnected,
		incompatible.ID: ReasonIncompatible, missing.ID: ReasonMissingCapability,
		unauthorized.ID: ReasonUnauthorized, unknownID: ReasonUnauthorized,
	}
	if len(result.Decisions) != len(want) {
		t.Fatalf("decisions = %+v, want %d; inaccessible non-explicit cluster may have leaked", result.Decisions, len(want))
	}
	for _, decision := range result.Decisions {
		if decision.Reason != want[decision.ClusterID] {
			t.Errorf("cluster %s reason = %s, want %s", decision.ClusterID, decision.Reason, want[decision.ClusterID])
		}
	}
}

func TestAllClustersSafetyAndProjectScope(t *testing.T) {
	t.Parallel()

	otherProject := candidate(2)
	otherProject.ProjectID = projectB
	request := baseRequest()
	request.AllowedProjectIDs = []uuid.UUID{projectA, projectB}
	request.Candidates = []Candidate{candidate(1), otherProject}
	request.Placement = model.Placement{AllClusters: true, ProjectIDs: []uuid.UUID{projectA}}
	result, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RequiresAllConfirmation {
		t.Fatal("all-clusters preview did not require enhanced confirmation")
	}
	if err := result.ValidateLaunch(result.PreviewDigest, false); !HasCode(err, CodeAllConfirmationRequired) {
		t.Fatalf("unconfirmed all-cluster launch = %v", err)
	}
	if err := result.ValidateLaunch(result.PreviewDigest, true); err != nil {
		t.Fatalf("confirmed all-cluster launch = %v", err)
	}
	if err := result.ValidateLaunch(digest('f'), true); !HasCode(err, CodePreviewStale) {
		t.Fatalf("stale launch = %v", err)
	}
	if got := selectedIDs(result); !slices.Equal(got, []uuid.UUID{clusterID(1)}) {
		t.Fatalf("selected = %v", got)
	}
	if err := (model.Placement{AllClusters: true, ClusterIDs: []uuid.UUID{clusterID(1)}}).Validate(); err == nil {
		t.Fatal("ambiguous all_clusters selector unexpectedly valid")
	}
}

func TestStableOrderDigestAndDuplicateProjectionMerge(t *testing.T) {
	t.Parallel()

	one := withLabels(candidate(1), map[string]string{"z": "last", "a": "first"})
	one.GroupIDs = []uuid.UUID{groupA}
	oneProjection := one
	oneProjection.GroupIDs = []uuid.UUID{groupB}
	two := candidate(2)

	request := baseRequest()
	request.Candidates = []Candidate{two, oneProjection, one}
	request.Placement = model.Placement{ClusterGroupIDs: []uuid.UUID{groupB, groupA}}
	first, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Candidates = []Candidate{one, two, oneProjection}
	request.Placement.ClusterGroupIDs = []uuid.UUID{groupA, groupB}
	second, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.PreviewDigest != second.PreviewDigest {
		t.Fatalf("digest depends on ordering: %s != %s", first.PreviewDigest, second.PreviewDigest)
	}
	if first.SelectedCount != 1 || first.Selected[0].ID != one.ID || len(first.Selected[0].GroupIDs) != 2 {
		t.Fatalf("duplicate projection not merged: %+v", first.Selected)
	}
	if !sortIsStable(first.Decisions) {
		t.Fatalf("decisions not stable-sorted: %+v", first.Decisions)
	}

	conflict := oneProjection
	conflict.Connected = false
	request.Candidates = []Candidate{one, conflict}
	_, err = Evaluate(request)
	var placementErr *Error
	if !errors.As(err, &placementErr) || placementErr.Code != CodeDuplicateClusterConflict {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
}

func TestPreviewDigestChangesWithMembershipTargetAndResolution(t *testing.T) {
	t.Parallel()

	request := baseRequest()
	request.Placement = model.Placement{AllClusters: true}
	request.Candidates = []Candidate{candidate(1)}
	baseline, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}

	mutations := []func(*Request){
		func(value *Request) { value.Candidates = append(value.Candidates, candidate(2)) },
		func(value *Request) { value.Identity.TargetGeneration++ },
		func(value *Request) { value.Identity.BundleSpecDigest = digest('e') },
		func(value *Request) { value.Identity.ResolvedRevision.Value = strings.Repeat("e", 40) },
	}
	for i, mutate := range mutations {
		copyRequest := request
		copyRequest.Candidates = append([]Candidate(nil), request.Candidates...)
		mutate(&copyRequest)
		changed, err := Evaluate(copyRequest)
		if err != nil {
			t.Fatalf("mutation %d: %v", i, err)
		}
		if changed.PreviewDigest == baseline.PreviewDigest {
			t.Errorf("mutation %d did not change preview digest", i)
		}
	}
}

func TestTenThousandCandidatesDeterministic(t *testing.T) {
	request := baseRequest()
	request.Placement = model.Placement{MatchLabels: map[string]string{"enabled": "true"}}
	request.Candidates = make([]Candidate, 10000)
	for i := range request.Candidates {
		value := candidate(i + 1)
		value.Labels = map[string]string{"enabled": "true"}
		request.Candidates[len(request.Candidates)-1-i] = value
	}
	result, err := Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedCount != 10000 || len(result.Decisions) != 10000 || !sortIsStable(result.Decisions) {
		t.Fatalf("10k result invalid: selected=%d decisions=%d stable=%t", result.SelectedCount, len(result.Decisions), sortIsStable(result.Decisions))
	}
}

func BenchmarkEvaluateTenThousand(b *testing.B) {
	request := baseRequest()
	request.Placement = model.Placement{MatchLabels: map[string]string{"enabled": "true"}}
	request.Candidates = make([]Candidate, 10000)
	for i := range request.Candidates {
		request.Candidates[i] = withLabels(candidate(i+1), map[string]string{"enabled": "true"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(request); err != nil {
			b.Fatal(err)
		}
	}
}

func baseRequest() Request {
	return Request{
		AllowedProjectIDs: []uuid.UUID{projectA},
		Identity: SnapshotIdentity{
			TargetGeneration: 1,
			BundleVersionID:  mustUUID("30000000-0000-0000-0000-000000000001"),
			BundleSpecDigest: digest('a'),
			ResolvedRevision: model.ImmutableRevision{
				Kind: model.RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: digest('c'),
			},
		},
	}
}

func candidate(number int) Candidate {
	return Candidate{
		ID: clusterID(number), ProjectID: projectA, Name: fmt.Sprintf("cluster-%05d", number),
		Capabilities: map[string]string{"delivery.astronomer.io/helm": "2.2.0"},
		Connected:    true, Compatibility: CompatibilityCompatible,
	}
}

func withLabels(candidate Candidate, values map[string]string) Candidate {
	candidate.Labels = values
	return candidate
}

func selectedIDs(result Result) []uuid.UUID {
	ids := make([]uuid.UUID, len(result.Selected))
	for i := range result.Selected {
		ids[i] = result.Selected[i].ID
	}
	return ids
}

func sortIsStable(decisions []Decision) bool {
	return slices.IsSortedFunc(decisions, func(left, right Decision) int {
		return strings.Compare(left.ClusterID.String(), right.ClusterID.String())
	})
}

func clusterID(number int) uuid.UUID {
	return mustUUID(fmt.Sprintf("40000000-0000-0000-0000-%012d", number))
}

func digest(fill byte) model.Digest {
	return model.Digest("sha256:" + strings.Repeat(string(fill), 64))
}

func mustUUID(value string) uuid.UUID { return uuid.MustParse(value) }
